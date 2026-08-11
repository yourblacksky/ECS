package networkauth

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/protocol"
)

func TestBrowserEnrollmentApprovalHeartbeatAndDisconnect(t *testing.T) {
	manager, err := New(config.NetworkAuthConfig{
		Enabled:       true,
		Directory:     t.TempDir(),
		LeaseTimeout:  config.Duration(45 * time.Second),
		EnrollmentTTL: config.Duration(time.Minute),
	}, "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := browserTestCapabilities("ecs_0123456789abcdef0123456789abcdef", "Browser test")
	request := BrowserConnectRequest{
		NodeID:        capabilities.NodeID,
		NodeName:      capabilities.Node,
		PairingCode:   "123456",
		PublicKeyHash: strings.Repeat("a", 64),
		Capabilities:  capabilities,
	}
	pending, created, err := manager.ConnectBrowser(request)
	if err != nil {
		t.Fatal(err)
	}
	if !created || pending.Status != StatusPending || pending.Transport != "browser_websocket" {
		t.Fatalf("unexpected pending relationship: %#v", pending)
	}
	if _, err := manager.Approve(pending.EnrollmentID, Approval{}); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	if manager.doc.Relationships[0].Token != "" {
		manager.mu.Unlock()
		t.Fatal("browser approval minted an HTTP bearer credential")
	}
	// Simulate a credential persisted by an older ECS version.
	manager.doc.Relationships[0].Token = "legacy-browser-token"
	manager.mu.Unlock()
	if _, ok := manager.Authenticate("legacy-browser-token", "reply:execute"); ok {
		t.Fatal("browser transport authenticated through the HTTP bearer path")
	}
	active, created, err := manager.ConnectBrowser(request)
	if err != nil {
		t.Fatal(err)
	}
	if created || active.Status != StatusActive {
		t.Fatalf("unexpected active relationship: %#v", active)
	}
	manager.mu.Lock()
	legacyToken := manager.doc.Relationships[0].Token
	manager.mu.Unlock()
	if legacyToken != "" {
		t.Fatal("browser reconnect did not erase the legacy HTTP credential")
	}
	if _, allowed := manager.BrowserAllows(request.NodeID, "calculate"); !allowed {
		t.Fatal("approved browser tool was not authorized")
	}
	if _, allowed := manager.BrowserAllows(request.NodeID, "unknown"); allowed {
		t.Fatal("undeclared browser tool was authorized")
	}
	relationshipFile, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat, err := manager.BrowserHeartbeat(request.NodeID, request.PublicKeyHash)
	if err != nil || heartbeat.Status != StatusActive {
		t.Fatalf("heartbeat failed: %#v %v", heartbeat, err)
	}
	manager.BrowserDisconnected(request.NodeID, request.PublicKeyHash)
	relationshipFileAfter, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(relationshipFile, relationshipFileAfter) {
		t.Fatal("browser heartbeat or disconnect rewrote the relationship file")
	}
	if got := manager.List()[0].Status; got != StatusOffline {
		t.Fatalf("disconnect status = %q, want offline", got)
	}
}

func TestBrowserCapabilitiesMustBeLeafOnly(t *testing.T) {
	manager, err := New(config.NetworkAuthConfig{
		Enabled:       true,
		Directory:     t.TempDir(),
		LeaseTimeout:  config.Duration(time.Minute),
		EnrollmentTTL: config.Duration(time.Minute),
	}, "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := browserTestCapabilities("ecs_0123456789abcdef0123456789abcdef", "Browser test")
	capabilities.Platform.AcceptsChildren = true
	_, _, err = manager.ConnectBrowser(BrowserConnectRequest{
		NodeID:        capabilities.NodeID,
		NodeName:      capabilities.Node,
		PairingCode:   "123456",
		PublicKeyHash: strings.Repeat("a", 64),
		Capabilities:  capabilities,
	})
	if err == nil || !strings.Contains(err.Error(), "client-only leaf") {
		t.Fatalf("expected leaf validation error, got %v", err)
	}
}

func TestGenericJoinCannotClaimBrowserTransport(t *testing.T) {
	manager, err := New(config.NetworkAuthConfig{
		Enabled:       true,
		Directory:     t.TempDir(),
		LeaseTimeout:  config.Duration(time.Minute),
		EnrollmentTTL: config.Duration(time.Minute),
	}, "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := browserTestCapabilities("ecs_0123456789abcdef0123456789abcdef", "Browser test")
	hash, err := CapabilityHash(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.CreateJoin(JoinRequest{
		NodeID:         capabilities.NodeID,
		NodeName:       capabilities.Node,
		URL:            capabilities.URL,
		Transport:      "browser_websocket",
		PublicKeyHash:  strings.Repeat("a", 64),
		PairingCode:    "123456",
		Capabilities:   capabilities,
		CapabilityHash: hash,
	})
	if err == nil || !strings.Contains(err.Error(), "verified public-key proof") {
		t.Fatalf("unverified browser enrollment error = %v", err)
	}
}

func browserTestCapabilities(nodeID, name string) protocol.NodeCapabilities {
	return protocol.NodeCapabilities{
		Protocol: "ecs/1",
		NodeID:   nodeID,
		Node:     name,
		URL:      "browser://" + nodeID,
		Platform: protocol.PlatformCapabilities{
			Name:              "browser",
			RuntimeMode:       "client",
			TopologyRole:      "leaf",
			AcceptsChildren:   false,
			SupportsInbound:   false,
			SupportsMulticast: false,
		},
		Features: []string{"browser_leaf"},
		Tools: []protocol.ToolCapability{{
			Name:        "calculate",
			Version:     "1.0.0",
			Description: "calculate",
			Risk:        "low",
			InputSchema: map[string]any{"type": "object"},
		}},
	}
}
