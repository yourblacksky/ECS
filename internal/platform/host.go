package platform

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"ecs/internal/protocol"
)

type RuntimePaths struct {
	Config string
	Data   string
	Cache  string
}

type SecretStore interface {
	Get(string) (string, error)
	Set(string, string) error
	Delete(string) error
}

type NetworkProvider interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
	Interfaces() ([]net.Interface, error)
	HTTPTransport() http.RoundTripper
}

type ToolRequest struct {
	Command        string
	Args           []string
	Input          []byte
	MaxOutputBytes int64
}

type ToolResult struct {
	Stdout string
	Stderr string
}

// ToolAvailability describes whether a platform executor can currently run a
// manifest. Availability is advisory and Execute must still recheck it because
// hardware and permissions may change between planning and execution.
type ToolAvailability struct {
	Available bool
	Reason    string
}

type ToolExecutor interface {
	Execute(context.Context, ToolRequest) (ToolResult, error)
}

type ToolAvailabilityProvider interface {
	ToolAvailability(ToolRequest) ToolAvailability
}

type AnnouncementHandler func(context.Context, []byte)

// DiscoveryProvider is payload-oriented: ECS owns announcement semantics while
// each host owns transport permissions and lifecycle.
type DiscoveryProvider interface {
	Name() string
	Start(context.Context, string, AnnouncementHandler) error
	Announce(context.Context, string, []byte) error
}

type Host interface {
	Paths() RuntimePaths
	Secrets() SecretStore
	Network() NetworkProvider
	Tools() ToolExecutor
	Discovery() DiscoveryProvider
	Capabilities() protocol.PlatformCapabilities
}

type DefaultHost struct {
	paths        RuntimePaths
	secrets      SecretStore
	network      NetworkProvider
	tools        ToolExecutor
	discovery    DiscoveryProvider
	capabilities protocol.PlatformCapabilities
}

func NewDefaultHost(paths RuntimePaths) *DefaultHost {
	mobile := runtime.GOOS == "android" || runtime.GOOS == "ios"
	inbound := runtime.GOOS != "js"
	return NewHost(HostOptions{
		Paths: paths,
		Capabilities: protocol.PlatformCapabilities{
			Name:               runtime.GOOS,
			RuntimeMode:        defaultRuntimeMode(mobile),
			TopologyRole:       "peer",
			AcceptsChildren:    true,
			SupportsInbound:    inbound,
			SupportsBackground: !mobile && inbound,
			SupportsSubprocess: !mobile,
			SupportsMulticast:  !mobile && inbound,
			SupportsPushWakeup: false,
		},
	})
}

// HostOptions lets a platform host replace only the boundaries it owns.
// Nil providers fall back to the portable desktop/server implementations.
type HostOptions struct {
	Paths        RuntimePaths
	Secrets      SecretStore
	Network      NetworkProvider
	Tools        ToolExecutor
	Discovery    DiscoveryProvider
	Capabilities protocol.PlatformCapabilities
}

func NewHost(options HostOptions) *DefaultHost {
	mobile := runtime.GOOS == "android" || runtime.GOOS == "ios"
	secrets := options.Secrets
	if secrets == nil {
		secrets = EnvironmentSecrets{}
	}
	network := options.Network
	if network == nil {
		network = SystemNetwork{}
	}
	tools := options.Tools
	if tools == nil {
		tools = ProcessTools{Enabled: !mobile}
	}
	discovery := options.Discovery
	if discovery == nil {
		discovery = &MDNSDiscovery{}
	}
	capabilities := options.Capabilities
	if capabilities.Name == "" {
		inbound := runtime.GOOS != "js"
		capabilities = protocol.PlatformCapabilities{
			Name:               runtime.GOOS,
			RuntimeMode:        defaultRuntimeMode(mobile),
			TopologyRole:       "peer",
			AcceptsChildren:    true,
			SupportsInbound:    inbound,
			SupportsBackground: !mobile && inbound,
			SupportsSubprocess: !mobile,
			SupportsMulticast:  !mobile && inbound,
		}
	}
	return &DefaultHost{
		paths:        options.Paths,
		secrets:      secrets,
		network:      network,
		tools:        tools,
		discovery:    discovery,
		capabilities: capabilities,
	}
}

func defaultRuntimeMode(mobile bool) string {
	if mobile {
		return "foreground"
	}
	if runtime.GOOS == "js" {
		return "client"
	}
	return "service"
}

func (h *DefaultHost) Paths() RuntimePaths          { return h.paths }
func (h *DefaultHost) Secrets() SecretStore         { return h.secrets }
func (h *DefaultHost) Network() NetworkProvider     { return h.network }
func (h *DefaultHost) Tools() ToolExecutor          { return h.tools }
func (h *DefaultHost) Discovery() DiscoveryProvider { return h.discovery }
func (h *DefaultHost) Capabilities() protocol.PlatformCapabilities {
	return h.capabilities
}

type EnvironmentSecrets struct{}

func (EnvironmentSecrets) Get(name string) (string, error) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return "", os.ErrNotExist
	}
	return value, nil
}

func (EnvironmentSecrets) Set(string, string) error {
	return errors.New("environment secret store is read-only")
}

func (EnvironmentSecrets) Delete(string) error {
	return errors.New("environment secret store is read-only")
}

// MemorySecrets is used by mobile bindings to pass values retrieved from a
// platform Keychain/Keystore without persisting those values in config.json.
type MemorySecrets struct {
	mu     sync.RWMutex
	values map[string]string
}

func NewMemorySecrets(values map[string]string) *MemorySecrets {
	store := &MemorySecrets{values: make(map[string]string, len(values))}
	for name, value := range values {
		store.values[name] = value
	}
	return store
}

func (s *MemorySecrets) Get(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[name]
	if !ok {
		return "", os.ErrNotExist
	}
	return value, nil
}

func (s *MemorySecrets) Set(name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[name] = value
	return nil
}

func (s *MemorySecrets) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, name)
	return nil
}

type SystemNetwork struct{}

func (SystemNetwork) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, network, host)
}

func (SystemNetwork) Interfaces() ([]net.Interface, error) {
	return net.Interfaces()
}

func (SystemNetwork) HTTPTransport() http.RoundTripper {
	return http.DefaultTransport.(*http.Transport).Clone()
}

type ProcessTools struct {
	Enabled bool
}

func (p ProcessTools) ToolAvailability(request ToolRequest) ToolAvailability {
	if !p.Enabled {
		return ToolAvailability{Reason: "executor_unavailable"}
	}
	if _, err := exec.LookPath(request.Command); err != nil {
		return ToolAvailability{Reason: "executable_not_found"}
	}
	return ToolAvailability{Available: true}
}

func (p ProcessTools) Execute(ctx context.Context, request ToolRequest) (ToolResult, error) {
	if !p.Enabled {
		return ToolResult{}, errors.New("this platform does not support subprocess tools")
	}
	command := exec.CommandContext(ctx, request.Command, request.Args...)
	command.Stdin = bytes.NewReader(request.Input)
	var stdout, stderr limitedBuffer
	stdout.limit = request.MaxOutputBytes
	stderr.limit = request.MaxOutputBytes
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return ToolResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int64
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - int64(b.buffer.Len())
	if remaining > 0 {
		if int64(len(value)) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return original, nil
}

func (b *limitedBuffer) String() string {
	return strings.TrimSpace(b.buffer.String())
}
