package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"ecs/internal/protocol"

	"github.com/grandcat/zeroconf"
)

const (
	DefaultDiscoveryService = "_ecs._tcp"
	discoveryDomain         = "local."
	discoveryProtocol       = "ecs/1"
	mdnsBrowseRefresh       = 5 * time.Second
	mdnsBrowseRestartDelay  = 250 * time.Millisecond
)

// MDNSDiscovery implements the shared desktop/server discovery transport.
// Apple hosts consume the same service through Bonjour, Android through NSD,
// and HarmonyOS through NetworkKit mDNS.
type MDNSDiscovery struct {
	mu        sync.Mutex
	server    mdnsPublisher
	signature string
}

func (d *MDNSDiscovery) Name() string { return "mdns-dns-sd" }

func (d *MDNSDiscovery) Start(
	ctx context.Context,
	serviceType string,
	handler AnnouncementHandler,
) error {
	if handler == nil {
		return errors.New("discovery announcement handler is required")
	}
	serviceType = normalizeServiceType(serviceType)
	cycle, err := startMDNSBrowseCycle(ctx, serviceType)
	if err != nil {
		return err
	}
	go func() {
		defer d.shutdown()
		for {
			select {
			case <-ctx.Done():
				cycle.cancel()
				return
			case <-cycle.ctx.Done():
				cycle.cancel()
				if ctx.Err() != nil {
					return
				}
				next, ok := retryMDNSBrowse(ctx, serviceType)
				if !ok {
					return
				}
				cycle = next
			case entry, ok := <-cycle.entries:
				if !ok {
					cycle.cancel()
					next, restarted := retryMDNSBrowse(ctx, serviceType)
					if !restarted {
						return
					}
					cycle = next
					continue
				}
				payload, encodeErr := announcementFromService(entry)
				if encodeErr == nil {
					handler(ctx, payload)
				}
			}
		}
	}()
	return nil
}

type mdnsBrowseCycle struct {
	ctx     context.Context
	cancel  context.CancelFunc
	entries <-chan *zeroconf.ServiceEntry
}

func retryMDNSBrowse(
	ctx context.Context,
	serviceType string,
) (mdnsBrowseCycle, bool) {
	// zeroconf closes its multicast sockets asynchronously after cancellation.
	// Starting the replacement immediately can leave both sockets joined to the
	// same group and route the refresh response to the canceled browser.
	timer := time.NewTimer(mdnsBrowseRestartDelay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return mdnsBrowseCycle{}, false
	case <-timer.C:
	}
	for {
		cycle, err := startMDNSBrowseCycle(ctx, serviceType)
		if err == nil {
			return cycle, true
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return mdnsBrowseCycle{}, false
		case <-timer.C:
		}
	}
}

func startMDNSBrowseCycle(
	parent context.Context,
	serviceType string,
) (mdnsBrowseCycle, error) {
	resolver, err := zeroconf.NewResolver(zeroconf.SelectIPTraffic(zeroconf.IPv4AndIPv6))
	if err != nil {
		return mdnsBrowseCycle{}, fmt.Errorf("start mDNS resolver: %w", err)
	}
	// grandcat/zeroconf de-duplicates a service instance for the lifetime of a
	// Browse call, including TXT updates. A bounded browse deliberately starts
	// a clean query so an otherwise unchanged service renews its ECS lease.
	cycleCtx, cancel := context.WithTimeout(parent, mdnsBrowseRefresh)
	entries := make(chan *zeroconf.ServiceEntry)
	if err := resolver.Browse(cycleCtx, serviceType, discoveryDomain, entries); err != nil {
		cancel()
		return mdnsBrowseCycle{}, fmt.Errorf("browse mDNS services: %w", err)
	}
	return mdnsBrowseCycle{
		ctx:     cycleCtx,
		cancel:  cancel,
		entries: entries,
	}, nil
}

func (d *MDNSDiscovery) Announce(
	ctx context.Context,
	serviceType string,
	payload []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var announced protocol.NodeCapabilities
	if err := json.Unmarshal(payload, &announced); err != nil {
		return fmt.Errorf("decode discovery announcement: %w", err)
	}
	parsedURL, err := url.Parse(announced.URL)
	if err != nil {
		return fmt.Errorf("parse discovery URL: %w", err)
	}
	port, err := strconv.Atoi(parsedURL.Port())
	if err != nil || port < 1 || port > 65535 {
		return errors.New("discovery URL must include a valid port")
	}
	instance := trimTXT(announced.Node, 63)
	if instance == "" {
		return errors.New("discovery node name is required")
	}
	serviceType = normalizeServiceType(serviceType)
	text := serviceTXT(announced)
	signature := instance + "\x00" + serviceType + "\x00" + strconv.Itoa(port)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.server != nil && d.signature == signature {
		return d.server.SetText(text)
	}
	if d.server != nil {
		d.server.Shutdown()
		d.server = nil
		d.signature = ""
	}
	server, err := registerMDNSPublisher(
		ctx,
		instance,
		serviceType,
		discoveryDomain,
		port,
		text,
	)
	if err != nil {
		return fmt.Errorf("register mDNS service: %w", err)
	}
	d.server = server
	d.signature = signature
	return nil
}

func mdnsRegistrationDomain() string {
	// grandcat/zeroconf v1.0 compares a dot-trimmed hostname against the
	// domain verbatim. Passing "local." therefore turns a macOS hostname
	// such as "host.local" into the invalid "host.local.local.". The
	// dotless spelling produces the same DNS-SD names and keeps the SRV
	// target resolvable by stricter clients such as HarmonyOS NetworkKit.
	return strings.TrimSuffix(discoveryDomain, ".")
}

func mdnsProxyIdentity() (string, []string, []net.Interface, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", nil, nil, err
	}
	host = normalizeMDNSHost(host)
	if host == "" {
		return "", nil, nil, errors.New("host name is empty")
	}

	allInterfaces, err := net.Interfaces()
	if err != nil {
		return "", nil, nil, err
	}
	interfaces := make([]net.Interface, 0, len(allInterfaces))
	ipv4 := make([]string, 0)
	ipv6 := make([]string, 0)
	linkLocalIPv6 := make([]string, 0)
	seen := make(map[string]struct{})
	for _, current := range allInterfaces {
		if current.Flags&net.FlagUp == 0 || current.Flags&net.FlagMulticast == 0 {
			continue
		}
		interfaces = append(interfaces, current)
		addresses, addressErr := current.Addrs()
		if addressErr != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, parseErr := net.ParseCIDR(address.String())
			if parseErr != nil || ip == nil || ip.IsLoopback() {
				continue
			}
			value := ip.String()
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			switch {
			case ip.To4() != nil:
				ipv4 = append(ipv4, value)
			case ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast():
				ipv6 = append(ipv6, value)
			case ip.IsLinkLocalUnicast():
				linkLocalIPv6 = append(linkLocalIPv6, value)
			}
		}
	}
	if len(ipv6) == 0 {
		ipv6 = linkLocalIPv6
	}
	addresses := append(ipv4, ipv6...)
	if len(interfaces) == 0 || len(addresses) == 0 {
		return "", nil, nil, errors.New("no multicast network address is available")
	}
	return host, addresses, interfaces, nil
}

func normalizeMDNSHost(value string) string {
	value = strings.Trim(strings.TrimSpace(value), ".")
	for strings.HasSuffix(strings.ToLower(value), ".local") {
		value = strings.TrimSuffix(value, value[len(value)-len(".local"):])
		value = strings.Trim(value, ".")
	}
	if value == "" {
		return ""
	}
	return value + ".local."
}

func (d *MDNSDiscovery) shutdown() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.server != nil {
		d.server.Shutdown()
		d.server = nil
		d.signature = ""
	}
}

func normalizeServiceType(value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(value, "."))
	if value == "" {
		return DefaultDiscoveryService
	}
	return value
}

func serviceTXT(node protocol.NodeCapabilities) []string {
	return serviceTXTAt(node, time.Now())
}

func serviceTXTAt(node protocol.NodeCapabilities, renewedAt time.Time) []string {
	features := make([]string, 0, 1)
	for _, feature := range node.Features {
		if feature == "network_auth" {
			features = append(features, feature)
		}
	}
	return []string{
		"protocol=ecs1",
		// DNS-SD records commonly outlive ECS's discovery expiry. Keep a
		// protocol-opaque lease changing on every ECS announcement so browsers
		// observe the heartbeat even when capabilities do not change.
		"lease=" + strconv.FormatInt(renewedAt.UnixNano(), 10),
		"node=" + trimTXT(node.Node, 96),
		"node_id=" + trimTXT(node.NodeID, 80),
		"platform=" + trimTXT(node.Platform.Name, 32),
		"runtime_mode=" + trimTXT(node.Platform.RuntimeMode, 24),
		"topology_role=" + trimTXT(node.Platform.TopologyRole, 24),
		"accepts_children=" + boolTXT(node.Platform.AcceptsChildren),
		"supports_inbound=" + boolTXT(node.Platform.SupportsInbound),
		"supports_background=" + boolTXT(node.Platform.SupportsBackground),
		"supports_subprocess=" + boolTXT(node.Platform.SupportsSubprocess),
		"supports_multicast=" + boolTXT(node.Platform.SupportsMulticast),
		"supports_push_wakeup=" + boolTXT(node.Platform.SupportsPushWakeup),
		"groups=" + trimTXT(strings.Join(node.Groups, ","), 160),
		"features=" + strings.Join(features, ","),
		"capability_hash=" + trimTXT(node.CapabilityHash, 64),
	}
}

func announcementFromService(entry *zeroconf.ServiceEntry) ([]byte, error) {
	if entry == nil || entry.Port < 1 || entry.Port > 65535 {
		return nil, errors.New("invalid mDNS service entry")
	}
	fields := parseTXT(entry.Text)
	if fields["protocol"] != "ecs1" {
		return nil, errors.New("unsupported mDNS service protocol")
	}
	address := firstServiceAddress(entry)
	if address == nil {
		return nil, errors.New("mDNS service has no address")
	}
	nodeName := strings.TrimSpace(fields["node"])
	if nodeName == "" {
		nodeName = strings.TrimSuffix(entry.Instance, ".")
	}
	features := splitTXTList(fields["features"])
	if fields["network_auth"] == "1" && !containsString(features, "network_auth") {
		features = append(features, "network_auth")
	}
	announced := protocol.NodeCapabilities{
		Protocol: discoveryProtocol,
		NodeID:   strings.TrimSpace(fields["node_id"]),
		Node:     nodeName,
		URL:      "http://" + net.JoinHostPort(address.String(), strconv.Itoa(entry.Port)),
		Platform: protocol.PlatformCapabilities{
			Name:               defaultTXT(fields["platform"], "unknown"),
			RuntimeMode:        defaultTXT(fields["runtime_mode"], "service"),
			TopologyRole:       defaultTXT(fields["topology_role"], topologyRole(fields)),
			AcceptsChildren:    fields["accepts_children"] == "1",
			SupportsInbound:    fields["supports_inbound"] == "1",
			SupportsBackground: fields["supports_background"] == "1",
			SupportsSubprocess: fields["supports_subprocess"] == "1",
			SupportsMulticast:  fields["supports_multicast"] == "1",
			SupportsPushWakeup: fields["supports_push_wakeup"] == "1",
		},
		Groups:         splitTXTList(fields["groups"]),
		Features:       features,
		CapabilityHash: strings.TrimSpace(fields["capability_hash"]),
		Tools:          []protocol.ToolCapability{},
	}
	return json.Marshal(announced)
}

func firstServiceAddress(entry *zeroconf.ServiceEntry) net.IP {
	for _, address := range entry.AddrIPv4 {
		if address != nil && !address.IsLoopback() {
			return address
		}
	}
	for _, address := range entry.AddrIPv6 {
		if address != nil && !address.IsLoopback() && !address.IsLinkLocalUnicast() {
			return address
		}
	}
	if len(entry.AddrIPv4) > 0 {
		return entry.AddrIPv4[0]
	}
	if len(entry.AddrIPv6) > 0 {
		return entry.AddrIPv6[0]
	}
	return nil
}

func parseTXT(text []string) map[string]string {
	result := make(map[string]string, len(text))
	for _, item := range text {
		name, value, found := strings.Cut(item, "=")
		if found {
			result[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	return result
}

func splitTXTList(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func topologyRole(fields map[string]string) string {
	if fields["accepts_children"] == "1" {
		return "peer"
	}
	return "leaf"
}

func defaultTXT(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func boolTXT(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func trimTXT(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
