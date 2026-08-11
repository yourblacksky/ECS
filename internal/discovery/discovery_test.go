package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/platform"
	"ecs/internal/protocol"
	"ecs/internal/tools"
)

type discoveryAvailabilityExecutor struct {
	status platform.ToolAvailability
}

func (e *discoveryAvailabilityExecutor) ToolAvailability(
	platform.ToolRequest,
) platform.ToolAvailability {
	return e.status
}

func (e *discoveryAvailabilityExecutor) Execute(
	context.Context,
	platform.ToolRequest,
) (platform.ToolResult, error) {
	return platform.ToolResult{}, nil
}

func TestCompactAnnouncementOmitsDetailedCapabilities(t *testing.T) {
	full := protocol.NodeCapabilities{
		Protocol:       protocolVersion,
		Node:           "worker",
		URL:            "http://127.0.0.1:8091",
		Model:          "test-model",
		Groups:         []string{"lab"},
		Features:       []string{"skills"},
		CapabilityHash: "abc123",
		Tools: []protocol.ToolCapability{{
			Name:        "large-tool",
			Description: "a long description that does not belong in multicast",
			InputSchema: map[string]any{"type": "object"},
		}},
		Skills: []protocol.SkillCapability{{Name: "large-skill"}},
		Agents: []protocol.AgentCapability{{Name: "executor"}},
	}
	encoded, err := json.Marshal(compactAnnouncement(full))
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= 1024 {
		t.Fatalf("announcement should remain small, got %d bytes", len(encoded))
	}
	var announced protocol.NodeCapabilities
	if err := json.Unmarshal(encoded, &announced); err != nil {
		t.Fatal(err)
	}
	if announced.CapabilityHash != full.CapabilityHash || len(announced.Tools) != 0 ||
		len(announced.Skills) != 0 || len(announced.Agents) != 0 {
		t.Fatalf("unexpected compact announcement: %#v", announced)
	}
}

type recordingDiscoveryProvider struct {
	started   chan struct{}
	announced chan []byte
	handler   platform.AnnouncementHandler
}

func (p *recordingDiscoveryProvider) Name() string { return "recording" }

func (p *recordingDiscoveryProvider) Start(
	_ context.Context,
	_ string,
	handler platform.AnnouncementHandler,
) error {
	p.handler = handler
	close(p.started)
	return nil
}

func (p *recordingDiscoveryProvider) Announce(
	_ context.Context,
	_ string,
	payload []byte,
) error {
	p.announced <- append([]byte(nil), payload...)
	return nil
}

func TestDiscoveryProviderIsReplaceable(t *testing.T) {
	provider := &recordingDiscoveryProvider{
		started:   make(chan struct{}),
		announced: make(chan []byte, 1),
	}
	manager := NewWithProvider(
		config.DiscoveryConfig{
			Enabled:      true,
			ServiceType:  "_custom._tcp",
			AdvertiseURL: "http://127.0.0.1:8090",
			Interval:     config.Duration(time.Hour),
			Expiry:       config.Duration(2 * time.Hour),
		},
		config.NestedConfig{},
		"portable-node",
		"test-model",
		"",
		"off",
		tools.NewRegistry(config.ToolsConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil))),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		provider,
	)
	manager.SetPlatformCapabilities(protocol.PlatformCapabilities{
		Name:        "test-os",
		RuntimeMode: "foreground",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("custom discovery provider did not start")
	}
	select {
	case payload := <-provider.announced:
		var announced protocol.NodeCapabilities
		if err := json.Unmarshal(payload, &announced); err != nil {
			t.Fatal(err)
		}
		if announced.Platform.Name != "test-os" ||
			announced.Platform.RuntimeMode != "foreground" {
			t.Fatalf("platform capability was not announced: %#v", announced.Platform)
		}
	case <-time.After(time.Second):
		t.Fatal("custom discovery provider did not announce")
	}
}

func TestCapabilitiesAdvertiseNetworkAuthentication(t *testing.T) {
	manager := &Manager{
		cfg:      config.DiscoveryConfig{AdvertiseURL: "http://127.0.0.1:8090"},
		nodeName: "local",
		nodeID:   "ecs_0123456789abcdef0123456789abcdef",
		registry: tools.NewRegistry(config.ToolsConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil))),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		nodes:    make(map[string]protocol.NodeCapabilities),
		hydrated: make(map[string]string),
	}
	if !slices.Contains(manager.Capabilities().Features, "network_auth") {
		t.Fatal("network-authenticated node did not advertise automatic enrollment support")
	}
}

func TestDiscoveryWithoutSharedTokenIsVisibleButNotSchedulable(t *testing.T) {
	now := time.Now()
	manager := &Manager{
		cfg:    config.DiscoveryConfig{Expiry: config.Duration(time.Minute)},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		nodes: map[string]protocol.NodeCapabilities{
			"worker": {
				NodeID: "ecs_0123456789abcdef0123456789abcdef",
				Node:   "worker", URL: "http://worker.local", Groups: []string{"lab"},
				LastSeen: &now,
			},
		},
		hydrated:    make(map[string]string),
		unavailable: make(map[string]string),
	}
	if snapshot := manager.Snapshot(); len(snapshot) != 1 {
		t.Fatalf("discovered topology disappeared: %#v", snapshot)
	}
	if peers := manager.Peers("lab"); len(peers) != 0 {
		t.Fatalf("identity-free discovery entered an execution pool: %#v", peers)
	}
	manager.authToken = "shared-token"
	if peers := manager.Peers("lab"); len(peers) != 1 ||
		peers[0].Token != "shared-token" {
		t.Fatalf("explicit shared-token discovery was not schedulable: %#v", peers)
	}
}

func TestCapabilitiesTrackCurrentToolAvailability(t *testing.T) {
	directory := t.TempDir()
	manifest := `{
		"name":"flashlight","version":"1.0.0","description":"flashlight",
		"command":"@device","args":["flashlight"],"input_schema":{"type":"object"}
	}`
	if err := os.WriteFile(filepath.Join(directory, "flashlight.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &discoveryAvailabilityExecutor{
		status: platform.ToolAvailability{Reason: "permission_denied"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistryWithExecutor(
		config.ToolsConfig{Directory: directory},
		logger,
		executor,
	)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	manager := New(
		config.DiscoveryConfig{AdvertiseURL: "http://127.0.0.1:8090"},
		config.NestedConfig{},
		"mobile",
		"test-model",
		"",
		"off",
		registry,
		logger,
	)
	unavailable := manager.Capabilities()
	if len(unavailable.Tools) != 0 ||
		len(unavailable.Availability) != 1 ||
		unavailable.Availability[0].Name != "flashlight" ||
		unavailable.Availability[0].Available ||
		unavailable.Availability[0].Reason != "permission_denied" {
		t.Fatalf("unavailable hardware was advertised: %#v", unavailable)
	}
	executor.status = platform.ToolAvailability{Available: true}
	available := manager.Capabilities()
	if len(available.Tools) != 1 || available.Tools[0].Name != "flashlight" ||
		len(available.Availability) != 0 {
		t.Fatalf("available hardware was not advertised: %#v", available)
	}
	if unavailable.CapabilityHash == available.CapabilityHash {
		t.Fatal("capability hash did not change with device availability")
	}
}

func TestAnnouncementHydratesCapabilitiesOverHTTP(t *testing.T) {
	const token = "shared-token"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/capabilities" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.NodeCapabilities{
			Protocol:       protocolVersion,
			Node:           "worker",
			URL:            server.URL,
			Model:          "test-model",
			CapabilityHash: "abc123",
			Tools:          []protocol.ToolCapability{{Name: "clock"}},
		})
	}))
	defer server.Close()

	manager := &Manager{
		cfg:       config.DiscoveryConfig{Expiry: config.Duration(20 * time.Second)},
		nodeName:  "local",
		authToken: token,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:    server.Client(),
		nodes:     make(map[string]protocol.NodeCapabilities),
		hydrated:  make(map[string]string),
	}
	payload, err := json.Marshal(protocol.NodeCapabilities{
		Protocol:       protocolVersion,
		Node:           "worker",
		URL:            server.URL,
		Model:          "test-model",
		CapabilityHash: "abc123",
		Tools:          []protocol.ToolCapability{},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.acceptAnnouncement(context.Background(), payload)
	nodes := manager.Snapshot()
	if len(nodes) != 1 || len(nodes[0].Tools) != 1 || nodes[0].Tools[0].Name != "clock" {
		t.Fatalf("capabilities were not hydrated: %#v", nodes)
	}
}

func TestDiscoveryUsesStableNodeIDWhenURLChanges(t *testing.T) {
	manager := &Manager{
		cfg:         config.DiscoveryConfig{Expiry: config.Duration(20 * time.Second)},
		nodeName:    "local",
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:      &http.Client{Timeout: time.Second},
		nodes:       make(map[string]protocol.NodeCapabilities),
		hydrated:    make(map[string]string),
		unavailable: make(map[string]string),
	}
	nodeID := "ecs_0123456789abcdef0123456789abcdef"
	announcement := protocol.NodeCapabilities{
		Protocol: protocolVersion,
		NodeID:   nodeID,
		Node:     "worker",
		URL:      "http://127.0.0.1:8091",
		Tools:    []protocol.ToolCapability{{Name: "clock"}},
	}
	first, _ := json.Marshal(announcement)
	manager.acceptAnnouncement(context.Background(), first)
	announcement.URL = "http://127.0.0.1:9091"
	second, _ := json.Marshal(announcement)
	manager.acceptAnnouncement(context.Background(), second)

	nodes := manager.Snapshot()
	if len(nodes) != 1 || nodes[0].NodeID != nodeID ||
		nodes[0].URL != "http://127.0.0.1:9091" {
		t.Fatalf("node identity was tied to its old URL: %#v", nodes)
	}
}

func TestAuthenticatedDiscoveryDoesNotProbeWithoutCredential(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	manager := &Manager{
		cfg:         config.DiscoveryConfig{Expiry: config.Duration(20 * time.Second)},
		nodeName:    "local",
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:      server.Client(),
		nodes:       make(map[string]protocol.NodeCapabilities),
		hydrated:    make(map[string]string),
		unavailable: make(map[string]string),
	}
	payload, _ := json.Marshal(protocol.NodeCapabilities{
		Protocol:       protocolVersion,
		NodeID:         "ecs_0123456789abcdef0123456789abcdef",
		Node:           "protected",
		URL:            server.URL,
		Features:       []string{"network_auth"},
		CapabilityHash: "hash-1",
	})
	manager.acceptAnnouncement(context.Background(), payload)
	manager.acceptAnnouncement(context.Background(), payload)
	if requests.Load() != 0 {
		t.Fatalf("protected discovery endpoint was probed %d times without a credential", requests.Load())
	}
	if nodes := manager.Snapshot(); len(nodes) != 1 {
		t.Fatalf("compact authenticated announcement was not retained: %#v", nodes)
	}
}

func TestFailedHydrationIsSuppressedUntilCapabilityChanges(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	manager := &Manager{
		cfg:         config.DiscoveryConfig{Expiry: config.Duration(20 * time.Second)},
		nodeName:    "local",
		authToken:   "wrong-shared-token",
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:      server.Client(),
		nodes:       make(map[string]protocol.NodeCapabilities),
		hydrated:    make(map[string]string),
		unavailable: make(map[string]string),
	}
	announcement := protocol.NodeCapabilities{
		Protocol:       protocolVersion,
		NodeID:         "ecs_0123456789abcdef0123456789abcdef",
		Node:           "protected",
		URL:            server.URL,
		Features:       []string{"network_auth"},
		CapabilityHash: "hash-1",
	}
	payload, _ := json.Marshal(announcement)
	manager.acceptAnnouncement(context.Background(), payload)
	manager.acceptAnnouncement(context.Background(), payload)
	if requests.Load() != 1 {
		t.Fatalf("unchanged failed hydration was attempted %d times", requests.Load())
	}
	announcement.CapabilityHash = "hash-2"
	changed, _ := json.Marshal(announcement)
	manager.acceptAnnouncement(context.Background(), changed)
	if requests.Load() != 2 {
		t.Fatalf("changed capabilities did not trigger a new hydration attempt")
	}
}

func TestCoversCapabilitiesRequiresEveryRequestedItem(t *testing.T) {
	node := protocol.NodeCapabilities{
		Tools:  []protocol.ToolCapability{{Name: "clock"}},
		Skills: []protocol.SkillCapability{{Name: "calculate"}},
	}
	if !coversCapabilities(node, []string{"clock"}, []string{"calculate"}) {
		t.Fatal("declared capabilities were rejected")
	}
	if coversCapabilities(node, []string{"clock", "shell"}, nil) {
		t.Fatal("missing tool capability was accepted")
	}
	if coversCapabilities(node, nil, []string{"text_extract"}) {
		t.Fatal("missing skill capability was accepted")
	}
}

func TestRegistryControllerAndClientShareDiscoveryWithoutGrantingTrust(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	controller := NewWithProvider(
		config.DiscoveryConfig{
			Registry: config.RegistryConfig{
				Serve:             true,
				Token:             "registry-secret",
				HeartbeatInterval: config.Duration(15 * time.Second),
				LeaseTimeout:      config.Duration(45 * time.Second),
				RequestTimeout:    config.Duration(5 * time.Second),
			},
		},
		config.NestedConfig{},
		"controller",
		"test-model",
		"",
		"off",
		tools.NewRegistry(config.ToolsConfig{}, logger),
		logger,
		nil,
	)
	server := httptest.NewServer(controller.RegistryHandler())
	defer server.Close()

	peer := registryRequest{
		Node: protocol.NodeCapabilities{
			Protocol: protocolVersion,
			NodeID:   "ecs_11111111111111111111111111111111",
			Node:     "remote-worker",
			URL:      "http://192.0.2.20:8090",
			Groups:   []string{"workers"},
			Features: []string{"network_auth"},
		},
		LeaseSeconds: 45,
	}
	body, _ := json.Marshal(peer)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/registry/register", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer registry-secret")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("registry rejected valid node: %s", response.Status)
	}

	client := NewWithProvider(
		config.DiscoveryConfig{
			AdvertiseURL: "http://192.0.2.10:8090",
			Expiry:       config.Duration(30 * time.Second),
			Registry: config.RegistryConfig{
				Enabled:           true,
				ControllerURLs:    []string{server.URL},
				Token:             "registry-secret",
				HeartbeatInterval: config.Duration(15 * time.Second),
				LeaseTimeout:      config.Duration(45 * time.Second),
				RequestTimeout:    config.Duration(5 * time.Second),
			},
		},
		config.NestedConfig{},
		"local-worker",
		"test-model",
		"",
		"off",
		tools.NewRegistry(config.ToolsConfig{}, logger),
		logger,
		nil,
	)
	client.SetNodeID("ecs_22222222222222222222222222222222")
	client.registryClient = server.Client()
	if err := client.syncRegistry(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	nodes := client.Snapshot()
	if len(nodes) != 1 || nodes[0].NodeID != peer.Node.NodeID ||
		nodes[0].Node != peer.Node.Node {
		t.Fatalf("registry node was not merged into discovery: %#v", nodes)
	}
}

func TestRegistryControllerRequiresConfiguredCredential(t *testing.T) {
	controller := newRegistryController(config.RegistryConfig{
		Token:             "registry-secret",
		HeartbeatInterval: config.Duration(15 * time.Second),
		LeaseTimeout:      config.Duration(45 * time.Second),
		RequestTimeout:    config.Duration(5 * time.Second),
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/registry/nodes", nil)
	response := httptest.NewRecorder()
	controller.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated registry request returned %d", response.Code)
	}
}
