package platform

import (
	"context"
	"encoding/json"
	"net"
	"slices"
	"testing"
	"time"

	"ecs/internal/protocol"

	"github.com/grandcat/zeroconf"
)

func TestMDNSAnnouncementRoundTrip(t *testing.T) {
	source := protocol.NodeCapabilities{
		Protocol:       discoveryProtocol,
		NodeID:         "ecs_0123456789abcdef0123456789abcdef",
		Node:           "ecs-mac-primary",
		URL:            "http://192.0.2.10:8090",
		Groups:         []string{"lab", "office"},
		Features:       []string{"network_auth", "skills"},
		CapabilityHash: "a1b2c3d4",
		Platform: protocol.PlatformCapabilities{
			Name:               "darwin",
			RuntimeMode:        "service",
			TopologyRole:       "peer",
			AcceptsChildren:    true,
			SupportsInbound:    true,
			SupportsBackground: true,
			SupportsSubprocess: true,
			SupportsMulticast:  true,
		},
	}
	entry := &zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.NewServiceEntry(
			source.Node,
			DefaultDiscoveryService,
			discoveryDomain,
		).ServiceRecord,
		Port:     8090,
		Text:     serviceTXT(source),
		AddrIPv4: []net.IP{net.ParseIP("192.0.2.10")},
	}
	payload, err := announcementFromService(entry)
	if err != nil {
		t.Fatal(err)
	}
	var announced protocol.NodeCapabilities
	if err := json.Unmarshal(payload, &announced); err != nil {
		t.Fatal(err)
	}
	if announced.Node != source.Node || announced.NodeID != source.NodeID ||
		announced.URL != source.URL || announced.CapabilityHash != source.CapabilityHash {
		t.Fatalf("unexpected mDNS identity: %#v", announced)
	}
	if announced.Platform != source.Platform {
		t.Fatalf("unexpected mDNS platform: %#v", announced.Platform)
	}
	if !slices.Equal(announced.Groups, source.Groups) ||
		!slices.Equal(announced.Features, []string{"network_auth"}) {
		t.Fatalf("unexpected mDNS lists: groups=%v features=%v", announced.Groups, announced.Features)
	}
}

func TestMDNSAnnouncementAcceptsMobileCompatibilityFields(t *testing.T) {
	entry := &zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.NewServiceEntry(
			"ecs-ios",
			DefaultDiscoveryService,
			discoveryDomain,
		).ServiceRecord,
		Port:     18090,
		AddrIPv4: []net.IP{net.ParseIP("192.0.2.20")},
		Text: []string{
			"protocol=ecs1",
			"node=ecs-ios",
			"node_id=ecs_abcdef0123456789abcdef0123456789",
			"platform=ios",
			"network_auth=1",
			"accepts_children=0",
			"supports_inbound=1",
		},
	}
	payload, err := announcementFromService(entry)
	if err != nil {
		t.Fatal(err)
	}
	var announced protocol.NodeCapabilities
	if err := json.Unmarshal(payload, &announced); err != nil {
		t.Fatal(err)
	}
	if announced.Platform.TopologyRole != "leaf" ||
		!slices.Contains(announced.Features, "network_auth") {
		t.Fatalf("mobile compatibility fields were not preserved: %#v", announced)
	}
}

func TestMDNSAnnouncementRejectsForeignProtocol(t *testing.T) {
	_, err := announcementFromService(&zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.NewServiceEntry(
			"foreign",
			DefaultDiscoveryService,
			discoveryDomain,
		).ServiceRecord,
		Port:     8090,
		AddrIPv4: []net.IP{net.ParseIP("192.0.2.30")},
		Text:     []string{"protocol=other"},
	})
	if err == nil {
		t.Fatal("foreign DNS-SD service was accepted")
	}
}

func TestMDNSRegistrationDomainHasNoTrailingDot(t *testing.T) {
	if got := mdnsRegistrationDomain(); got != "local" {
		t.Fatalf("unexpected mDNS registration domain: %q", got)
	}
}

func TestMDNSServiceTXTChangesLeaseWithoutChangingIdentity(t *testing.T) {
	node := protocol.NodeCapabilities{
		Node: "ecs-mac",
		Platform: protocol.PlatformCapabilities{
			Name: "darwin",
		},
	}
	first := serviceTXTAt(node, time.Unix(100, 1))
	second := serviceTXTAt(node, time.Unix(100, 2))
	if slices.Equal(first, second) {
		t.Fatal("mDNS lease did not change between announcements")
	}
	if first[0] != second[0] || !slices.Equal(first[2:], second[2:]) {
		t.Fatalf("mDNS renewal changed fields other than lease:\nfirst=%v\nsecond=%v", first, second)
	}
}

func TestMDNSBrowseContextUsesRefreshDeadline(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	cycle, err := startMDNSBrowseCycle(parent, DefaultDiscoveryService)
	if err != nil {
		t.Fatal(err)
	}
	defer cycle.cancel()
	deadline, ok := cycle.ctx.Deadline()
	if !ok {
		t.Fatal("mDNS browse does not have a refresh deadline")
	}
	remaining := time.Until(deadline)
	if remaining < mdnsBrowseRefresh-time.Second || remaining > mdnsBrowseRefresh {
		t.Fatalf("unexpected mDNS browse refresh deadline: %s", remaining)
	}
}

func TestNormalizeMDNSHost(t *testing.T) {
	tests := map[string]string{
		"ecs-mac":               "ecs-mac.local.",
		"ecs-mac.local":         "ecs-mac.local.",
		"ecs-mac.local.":        "ecs-mac.local.",
		"ecs-mac.local.local.":  "ecs-mac.local.",
		"ECS-Mac.LOCAL.LOCAL.":  "ECS-Mac.local.",
		"  ecs-mac.local.local": "ecs-mac.local.",
		".":                     "",
	}
	for input, want := range tests {
		if got := normalizeMDNSHost(input); got != want {
			t.Errorf("normalizeMDNSHost(%q) = %q, want %q", input, got, want)
		}
	}
}
