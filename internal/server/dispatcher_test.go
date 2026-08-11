package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ecs/internal/browsernode"
	"ecs/internal/config"
	"ecs/internal/discovery"
	"ecs/internal/networkauth"
	"ecs/internal/protocol"
)

type recordingBrowserRuntime struct {
	mu        sync.Mutex
	connected bool
	calls     []browsernode.TaskRequest
	result    browsernode.TaskResponse
	err       error
}

func (*recordingBrowserRuntime) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (r *recordingBrowserRuntime) Connected(string) bool {
	return r.connected
}

func (r *recordingBrowserRuntime) Dispatch(
	_ context.Context,
	_ string,
	request browsernode.TaskRequest,
) (browsernode.TaskResponse, error) {
	r.mu.Lock()
	r.calls = append(r.calls, request)
	r.mu.Unlock()
	return r.result, r.err
}

func TestNormalizeDirectToolProducesOneDeterministicTool(t *testing.T) {
	request := protocol.ReplyRequest{DirectTool: &protocol.ToolInvocation{
		Name:      " calculate ",
		Arguments: json.RawMessage(`{"a":2,"op":"add","b":3}`),
	}}
	if err := normalizeDirectTool(&request); err != nil {
		t.Fatal(err)
	}
	if request.DirectTool.Name != "calculate" || len(request.Tools) != 1 ||
		request.Tools[0] != "calculate" {
		t.Fatalf("direct tool was not normalized: %#v", request)
	}
	request = protocol.ReplyRequest{
		Skills:     []string{"calculate"},
		DirectTool: &protocol.ToolInvocation{Name: "calculate", Arguments: json.RawMessage(`{}`)},
	}
	if err := normalizeDirectTool(&request); err == nil {
		t.Fatal("direct tool combined with a skill was accepted")
	}
}

func TestNormalizeExpectedTextKeepsLiteralProbeIsolated(t *testing.T) {
	request := protocol.ReplyRequest{ExpectedText: "READY_QWEN06_WORKER1"}
	if err := normalizeExpectedText(&request); err != nil {
		t.Fatal(err)
	}
	request.DirectTool = &protocol.ToolInvocation{Name: "clock", Arguments: json.RawMessage(`{}`)}
	if err := normalizeExpectedText(&request); err == nil {
		t.Fatal("expected_text combined with direct_tool must be rejected")
	}
	request = protocol.ReplyRequest{ExpectedText: strings.Repeat("x", 513)}
	if err := normalizeExpectedText(&request); err == nil {
		t.Fatal("oversized expected_text must be rejected")
	}
}

func TestBrowserDispatcherConvertsTaskToStandardPeerResult(t *testing.T) {
	runtime := &recordingBrowserRuntime{
		connected: true,
		result: browsernode.TaskResponse{
			Result:   json.RawMessage(`{"result":5}`),
			Duration: "12ms",
		},
	}
	dispatcher := browserPeerDispatcher{runtime: runtime, timeout: time.Second}
	request := protocol.ReplyRequest{
		RequestID: "request-1",
		TraceID:   "trace-1",
		DirectTool: &protocol.ToolInvocation{
			Name:      "calculate",
			Arguments: json.RawMessage(`{"a":2,"op":"add","b":3}`),
		},
	}
	result := dispatcher.Dispatch(context.Background(), config.Peer{
		NodeID: "browser-1", Name: "browser", Transport: networkauth.TransportBrowserWebSocket,
	}, request)
	if result.Error != "" || result.Response == nil ||
		result.Response.Message.Content != `{"result":5}` ||
		len(result.Response.ToolRuns) != 1 ||
		result.Response.ToolRuns[0].Duration != 12 {
		t.Fatalf("unexpected browser peer result: %#v", result)
	}
}

func TestGroupDispatchIncludesAuthorizedConnectedBrowserInEveryMode(t *testing.T) {
	manager, nodeID := activeBrowserRelationship(t)
	runtime := &recordingBrowserRuntime{
		connected: true,
		result: browsernode.TaskResponse{
			Result:   json.RawMessage(`{"result":5}`),
			Duration: "1ms",
		},
	}
	cfg := config.Config{
		NodeName: "parent",
		Agent: config.AgentConfig{
			GroupDispatchMode: dispatchSingle,
			GroupRaceFanout:   2,
		},
		HTTP: config.HTTPConfig{
			MaxBodyBytes: 1 << 20,
			PeerTimeout:  config.Duration(2 * time.Second),
		},
	}
	server := &Server{
		cfg:           cfg,
		network:       manager,
		browserHub:    runtime,
		discovery:     discovery.New(cfg.Discovery, cfg.Nested, "parent", "", "", "off", nil, nil),
		peerScheduler: newPeerScheduler(),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if peers := server.groupPeersFor("workers", protocol.ReplyRequest{
		Tools: []string{"calculate"},
	}); len(peers) != 0 {
		t.Fatalf("natural-language request selected a browser without direct_tool: %#v", peers)
	}
	for _, mode := range []string{dispatchSingle, dispatchBroadcast, dispatchRace} {
		t.Run(mode, func(t *testing.T) {
			body := `{
				"request_id":"request-` + mode + `",
				"prompt":"计算 2 加 3",
				"direct_tool":{
					"name":"calculate",
					"arguments":{"a":2,"op":"add","b":3}
				},
				"dispatch_mode":"` + mode + `"
			}`
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/groups/workers/reply",
				bytes.NewBufferString(body),
			)
			request.SetPathValue("group", "workers")
			response := httptest.NewRecorder()
			server.groupReply(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("group dispatch status %d: %s", response.Code, response.Body.String())
			}
			var result protocol.GroupResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.DispatchMode != mode || result.Winner != "Browser worker" ||
				len(result.Candidates) != 1 || len(result.Results) != 1 ||
				result.Results[0].Response == nil ||
				result.Results[0].Response.Node != "Browser worker" {
				t.Fatalf("browser did not participate in %s: %#v", mode, result)
			}
		})
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.calls) != 3 {
		t.Fatalf("browser dispatch calls = %d, want 3", len(runtime.calls))
	}
	for _, call := range runtime.calls {
		if call.Tool != "calculate" || !strings.Contains(string(call.Arguments), `"op":"add"`) {
			t.Fatalf("unexpected browser task: %#v", call)
		}
	}
	if peers := manager.BrowserPeersFor("workers", []string{"clock"}); len(peers) != 0 {
		t.Fatalf("browser was returned for an unauthorized tool: %#v", peers)
	}
	if peers := manager.BrowserPeersFor("workers", []string{"calculate"}); len(peers) != 1 ||
		peers[0].NodeID != nodeID {
		t.Fatalf("authorized browser was not returned: %#v", peers)
	}
}

func TestMixedTransportRaceUsesBrowserWinnerAndCancelsHTTPPeer(t *testing.T) {
	manager, _ := activeBrowserRelationship(t)
	runtime := &recordingBrowserRuntime{
		connected: true,
		result: browsernode.TaskResponse{
			Result:   json.RawMessage(`{"result":5}`),
			Duration: "1ms",
		},
	}
	httpCanceled := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		close(httpCanceled)
		return nil, request.Context().Err()
	})}
	cfg := config.Config{
		NodeName: "parent",
		Agent: config.AgentConfig{
			GroupDispatchMode: dispatchSingle,
			GroupRaceFanout:   2,
		},
		HTTP: config.HTTPConfig{
			MaxBodyBytes: 1 << 20,
			PeerTimeout:  config.Duration(2 * time.Second),
		},
		Groups: map[string][]config.Peer{
			"workers": {{
				NodeID: "http-worker",
				Name:   "HTTP worker",
				URL:    "http://worker.test",
			}},
		},
	}
	server := &Server{
		cfg:           cfg,
		network:       manager,
		browserHub:    runtime,
		discovery:     discovery.New(cfg.Discovery, cfg.Nested, "parent", "", "", "off", nil, nil),
		peerScheduler: newPeerScheduler(),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:        client,
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/groups/workers/reply",
		bytes.NewBufferString(`{
			"request_id":"request-race",
			"prompt":"计算 2 加 3",
			"direct_tool":{
				"name":"calculate",
				"arguments":{"a":2,"op":"add","b":3}
			},
			"dispatch_mode":"race",
			"dispatch_fanout":2
		}`),
	)
	request.SetPathValue("group", "workers")
	response := httptest.NewRecorder()
	server.groupReply(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("mixed race status %d: %s", response.Code, response.Body.String())
	}
	var result protocol.GroupResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Winner != "Browser worker" || len(result.Candidates) != 2 ||
		len(result.Selected) != 2 || len(result.Results) != 2 {
		t.Fatalf("unexpected mixed race result: %#v", result)
	}
	select {
	case <-httpCanceled:
	case <-time.After(time.Second):
		t.Fatal("HTTP loser was not canceled after browser success")
	}
}

func activeBrowserRelationship(t *testing.T) (*networkauth.Manager, string) {
	t.Helper()
	manager, err := networkauth.New(config.NetworkAuthConfig{
		Enabled:       true,
		Directory:     t.TempDir(),
		LeaseTimeout:  config.Duration(time.Minute),
		EnrollmentTTL: config.Duration(time.Minute),
	}, "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	nodeID := "ecs_0123456789abcdef0123456789abcdef"
	capabilities := protocol.NodeCapabilities{
		Protocol: "ecs/1",
		NodeID:   nodeID,
		Node:     "Browser worker",
		URL:      "browser://" + nodeID,
		Platform: protocol.PlatformCapabilities{
			Name:         "browser",
			RuntimeMode:  "client",
			TopologyRole: "leaf",
		},
		Features: []string{"browser_leaf", "websocket_tasks"},
		Tools: []protocol.ToolCapability{{
			Name:        "calculate",
			Version:     "1.0.0",
			Description: "calculate",
			Risk:        "low",
			InputSchema: map[string]any{"type": "object"},
		}},
	}
	connect := networkauth.BrowserConnectRequest{
		NodeID:        nodeID,
		NodeName:      capabilities.Node,
		PairingCode:   "123456",
		PublicKeyHash: strings.Repeat("a", 64),
		Capabilities:  capabilities,
	}
	pending, _, err := manager.ConnectBrowser(connect)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Approve(pending.EnrollmentID, networkauth.Approval{
		Groups:       []string{"workers"},
		AllowedTools: []string{"calculate"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ConnectBrowser(connect); err != nil {
		t.Fatal(err)
	}
	return manager, nodeID
}
