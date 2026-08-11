package platform

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestBridgeDiscoveryInjectsNativeAnnouncement(t *testing.T) {
	bridge := NewBridgeDiscovery("test-native")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan string, 1)
	if err := bridge.Start(ctx, "", func(_ context.Context, payload []byte) {
		received <- string(payload)
	}); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Inject([]byte(`{"node":"peer"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-received:
		if payload != `{"node":"peer"}` {
			t.Fatalf("unexpected announcement: %s", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("native announcement was not delivered")
	}
}

func TestDefaultHostDeclaresCurrentPlatform(t *testing.T) {
	host := NewDefaultHost(RuntimePaths{Config: "config.json"})
	capabilities := host.Capabilities()
	if capabilities.Name != runtime.GOOS || capabilities.RuntimeMode == "" {
		t.Fatalf("unexpected platform capabilities: %#v", capabilities)
	}
	if host.Paths().Config != "config.json" || host.Secrets() == nil ||
		host.Network() == nil || host.Tools() == nil || host.Discovery() == nil {
		t.Fatal("default host did not provide every platform boundary")
	}
}

func TestDisabledProcessToolsRejectSubprocess(t *testing.T) {
	_, err := (ProcessTools{Enabled: false}).Execute(
		context.Background(),
		ToolRequest{Command: "unused"},
	)
	if err == nil {
		t.Fatal("disabled process executor accepted a subprocess")
	}
}
