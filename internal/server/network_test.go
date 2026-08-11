package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ecs/internal/browsernode"
	"ecs/internal/config"
	"ecs/internal/networkauth"
	"ecs/internal/protocol"
)

type fakeBrowserRuntime struct {
	result    browsernode.TaskResponse
	err       error
	connected bool
}

func (f fakeBrowserRuntime) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (f fakeBrowserRuntime) Dispatch(
	context.Context,
	string,
	browsernode.TaskRequest,
) (browsernode.TaskResponse, error) {
	return f.result, f.err
}

func (f fakeBrowserRuntime) Connected(string) bool { return f.connected }

func TestRelationshipAuthenticationIsScopedAndCannotAccessAdmin(t *testing.T) {
	manager, token := activeNetworkRelationship(t)
	server := &Server{
		cfg:     config.Config{AdminToken: "admin-secret"},
		network: manager,
	}
	okHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/reply", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.withAuthScope("reply:execute", okHandler)(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("relationship token was rejected: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/traces/demo", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	server.withAuthScope("trace:read", okHandler)(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("relationship exceeded its scope: %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/admin/network/relationships", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	server.withAdminAuth(okHandler)(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("relationship token accessed admin API: %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/admin/network/relationships", nil)
	request.Header.Set("Authorization", "Bearer admin-secret")
	response = httptest.NewRecorder()
	server.withAdminAuth(okHandler)(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("admin token was rejected: %d", response.Code)
	}
}

func TestAdminAuthenticationFailsClosedAndRejectsNodeToken(t *testing.T) {
	okHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)

	server := &Server{cfg: config.Config{AuthToken: "node-secret"}}
	response := httptest.NewRecorder()
	server.withAdminAuth(okHandler)(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("empty Admin token did not fail closed: %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)
	request.Header.Set("Authorization", "Bearer node-secret")
	response = httptest.NewRecorder()
	server.withAdminAuth(okHandler)(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("shared node token accessed Admin API: %d", response.Code)
	}
}

func TestNetworkPolicyConstrainsRequestedCapabilities(t *testing.T) {
	relationship := networkauth.PublicRelationship{
		AllowedTools:  []string{"clock"},
		AllowedSkills: []string{"clock"},
		PeerNodeID:    "ecs_0123456789abcdef0123456789abcdef",
		Role:          networkauth.RoleChild,
		PolicyVersion: 3,
	}
	ctx := context.WithValue(context.Background(), networkRelationshipKey{}, relationship)
	request := protocol.ReplyRequest{Prompt: "现在几点？", Model: "remote-model"}
	if err := applyNetworkPolicy(ctx, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 1 || request.Tools[0] != "clock" ||
		len(request.Skills) != 1 || request.Skills[0] != "clock" ||
		request.Model != "" || !request.RestrictTools || !request.RestrictSkills ||
		request.Metadata["ecs.auth.peer_node_id"] != relationship.PeerNodeID {
		t.Fatalf("network policy was not applied: %#v", request)
	}
	request = protocol.ReplyRequest{Prompt: "run", Skills: []string{"calculate"}}
	if err := applyNetworkPolicy(ctx, &request); err == nil {
		t.Fatal("unauthorized skill request was accepted")
	}
	request = protocol.ReplyRequest{
		Prompt: "run",
		DirectTool: &protocol.ToolInvocation{
			Name:      "calculate",
			Arguments: json.RawMessage(`{"a":1,"op":"add","b":2}`),
		},
	}
	if err := applyNetworkPolicy(ctx, &request); err == nil {
		t.Fatal("unauthorized direct tool request was accepted")
	}
}

func TestRoutedPeerUsesNodeSpecificTransportOverride(t *testing.T) {
	server := &Server{cfg: config.Config{
		Network: config.NetworkAuthConfig{RouteOverrides: map[string]string{
			"ecs_android_emulator": "http://127.0.0.1:18092/",
		}},
	}}
	original := config.Peer{
		NodeID: "ecs_android_emulator",
		Name:   "ecs-android",
		URL:    "http://192.168.232.2:18090",
		Token:  "relationship-token",
	}
	routed := server.routedPeer(original)
	if routed.URL != "http://127.0.0.1:18092" ||
		routed.Name != original.Name || routed.Token != original.Token {
		t.Fatalf("unexpected routed peer: %#v", routed)
	}
	if original.URL != "http://192.168.232.2:18090" {
		t.Fatalf("route override mutated relationship identity: %#v", original)
	}
}

func TestCallPeerUsesTransportOverride(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/reply" ||
			r.Header.Get("Authorization") != "Bearer relationship-token" {
			t.Fatalf("unexpected routed request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		writeJSON(w, http.StatusOK, protocol.ReplyResponse{
			RequestID: "route-test",
			Node:      "ecs-android",
			Message:   protocol.Message{Role: "assistant", Content: "ok"},
		})
	}))
	defer target.Close()

	server := &Server{
		cfg: config.Config{
			Network: config.NetworkAuthConfig{RouteOverrides: map[string]string{
				"ecs_android_emulator": target.URL,
			}},
			HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20},
		},
		client: target.Client(),
	}
	result := server.callPeer(context.Background(), config.Peer{
		NodeID: "ecs_android_emulator",
		Name:   "ecs-android",
		URL:    "http://192.0.2.1:18090",
		Token:  "relationship-token",
	}, protocol.ReplyRequest{RequestID: "route-test", Prompt: "ping"})
	if result.Error != "" || result.Response == nil || result.Response.Node != "ecs-android" {
		t.Fatalf("transport override was not used: %#v", result)
	}
}

func TestAdminCanUpdateRelationshipPermissions(t *testing.T) {
	manager, token := activeNetworkRelationship(t)
	relationship := manager.List()[0]
	server := &Server{
		cfg:     config.Config{HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20}},
		network: manager,
	}
	body, _ := json.Marshal(networkauth.Approval{
		AllowedTools:  []string{"clock"},
		AllowedSkills: []string{"clock"},
		Scopes:        []string{"heartbeat:write", "recursive:execute"},
	})
	request := httptest.NewRequest(
		http.MethodPut,
		"/v1/admin/network/relationships/"+relationship.EnrollmentID+"/permissions",
		bytes.NewReader(body),
	)
	request.SetPathValue("enrollment", relationship.EnrollmentID)
	response := httptest.NewRecorder()
	server.adminNetworkPermissions(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("permission update failed: %d %s", response.Code, response.Body.String())
	}
	if _, ok := manager.Authenticate(token, "reply:execute"); ok {
		t.Fatal("removed permission remained valid after admin update")
	}
	if _, ok := manager.Authenticate(token, "recursive:execute"); ok {
		t.Fatal("rotated credential remained valid after admin update")
	}
	heartbeat, err := manager.RecordHeartbeat(token, networkauth.Heartbeat{
		NodeID:         relationship.PeerNodeID,
		CapabilityHash: relationship.CapabilityHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.Token == "" {
		t.Fatal("heartbeat did not deliver the replacement credential")
	}
	if _, ok := manager.Authenticate(heartbeat.Token, "recursive:execute"); !ok {
		t.Fatal("replacement credential did not receive the new permission")
	}
	disabled := false
	body, _ = json.Marshal(networkauth.Approval{
		AllowedTools:         []string{"clock"},
		AllowedSkills:        []string{"clock"},
		Scopes:               []string{"heartbeat:write", "recursive:execute"},
		AllowUpstreamExecute: &disabled,
	})
	request = httptest.NewRequest(
		http.MethodPut,
		"/v1/admin/network/relationships/"+relationship.EnrollmentID+"/permissions",
		bytes.NewReader(body),
	)
	request.SetPathValue("enrollment", relationship.EnrollmentID)
	response = httptest.NewRecorder()
	server.adminNetworkPermissions(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("upstream permission update failed: %d %s", response.Code, response.Body.String())
	}
	if _, ok := manager.Authenticate(heartbeat.Token, "recursive:execute"); ok {
		t.Fatal("explicit upstream execution denial was not enforced")
	}
	if _, ok := manager.Authenticate(heartbeat.Token, "heartbeat:write"); !ok {
		t.Fatal("upstream execution denial incorrectly blocked relationship maintenance")
	}
}

func TestAdminBrowserTaskUsesUnprocessableStatusForToolFailure(t *testing.T) {
	server := &Server{
		cfg: config.Config{HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20}},
		browserHub: fakeBrowserRuntime{result: browsernode.TaskResponse{
			TaskID: "task-1",
			NodeID: "ecs_0123456789abcdef0123456789abcdef",
			Tool:   "calculate",
			Error:  "op is outside the allowed enum",
		}},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/admin/browser/nodes/ecs_0123456789abcdef0123456789abcdef/tasks",
		bytes.NewBufferString(`{
			"tool":"calculate",
			"arguments":{"a":1,"op":"invented","b":2}
		}`),
	)
	request.SetPathValue("node", "ecs_0123456789abcdef0123456789abcdef")
	response := httptest.NewRecorder()
	server.adminBrowserTask(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result browsernode.TaskResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error == "" || result.TaskID != "task-1" {
		t.Fatalf("tool failure details were lost: %#v", result)
	}
}

func activeNetworkRelationship(t *testing.T) (*networkauth.Manager, string) {
	t.Helper()
	manager, err := networkauth.New(config.NetworkAuthConfig{
		Enabled:           true,
		Directory:         t.TempDir(),
		HeartbeatInterval: config.Duration(time.Second),
		LeaseTimeout:      config.Duration(3 * time.Second),
		EnrollmentTTL:     config.Duration(10 * time.Minute),
	}, "parent", "http://parent.local")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := protocol.NodeCapabilities{
		Protocol: "ecs/1",
		NodeID:   "ecs_0123456789abcdef0123456789abcdef",
		Node:     "child",
		URL:      "http://child.local",
		Model:    "test",
		Features: []string{"relationship_token_rotation"},
		Tools:    []protocol.ToolCapability{{Name: "clock"}},
		Skills:   []protocol.SkillCapability{{Name: "clock"}},
	}
	hash, err := networkauth.CapabilityHash(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	capabilities.CapabilityHash = hash
	enrollment, err := manager.CreateJoin(networkauth.JoinRequest{
		NodeID:         capabilities.NodeID,
		NodeName:       capabilities.Node,
		URL:            capabilities.URL,
		PairingCode:    "482913",
		Capabilities:   capabilities,
		CapabilityHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Approve(enrollment.EnrollmentID, networkauth.Approval{}); err != nil {
		t.Fatal(err)
	}
	claim, err := manager.Claim(enrollment.EnrollmentID, enrollment.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	return manager, claim.Token
}
