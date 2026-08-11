package mobile

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func mobileTestDocument(t *testing.T, dataDir string) map[string]any {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "config.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["listen"] = "127.0.0.1:0"
	document["discovery"].(map[string]any)["enabled"] = false
	document["nested"].(map[string]any)["enabled"] = false
	document["network_auth"].(map[string]any)["enabled"] = false
	document["agentteams"].(map[string]any)["enabled"] = false
	document["tools"].(map[string]any)["directory"] = filepath.Join(root, "tools.d")
	document["skills"].(map[string]any)["directory"] = filepath.Join(root, "skills.d")
	document["agent_roles"].(map[string]any)["directory"] = filepath.Join(root, "agents.d")
	document["agent_roles"].(map[string]any)["pipeline_directory"] = filepath.Join(root, "pipelines.d")
	document["agent"].(map[string]any)["system_prompt_file"] =
		filepath.Join(root, "prompts", "system.small.zh.txt")
	document["agent"].(map[string]any)["conversation_guidance_file"] =
		filepath.Join(root, "prompts", "conversation.default.zh.txt")
	document["llm"].(map[string]any)["api_key"] = ""
	document["llm"].(map[string]any)["api_key_secret"] = "llm.api_key"
	document["trace"].(map[string]any)["directory"] = filepath.Join(dataDir, "data", "traces")
	document["sessions"].(map[string]any)["directory"] = filepath.Join(dataDir, "data", "sessions")
	document["network_auth"].(map[string]any)["directory"] =
		filepath.Join(dataDir, "data", "network")
	return document
}

func TestMobileLifecycle(t *testing.T) {
	defer Stop()
	dataDir := t.TempDir()
	document := mobileTestDocument(t, dataDir)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := StartWithSecrets(
		string(encoded),
		dataDir,
		`{"llm.api_key":"memory-only-secret"}`,
	); err != nil {
		t.Fatal(err)
	}
	persisted, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(persisted) == "" ||
		bytes.Contains(persisted, []byte("memory-only-secret")) {
		t.Fatal("mobile secret was persisted into config.json")
	}
	var status struct {
		Running bool `json:"running"`
		Tools   int  `json:"tools"`
		Skills  int  `json:"skills"`
	}
	if err := json.Unmarshal([]byte(StatusJSON()), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.Tools != 16 || status.Skills != 15 {
		t.Fatalf("unexpected mobile status: %#v", status)
	}
	if err := Reload(); err != nil {
		t.Fatal(err)
	}
	if err := Stop(); err != nil {
		t.Fatal(err)
	}
	if StatusJSON() != `{"running":false}` {
		t.Fatalf("unexpected stopped status: %s", StatusJSON())
	}
}

func TestNativeDiscoveryBridgeFeedsRuntime(t *testing.T) {
	defer Stop()
	dataDir := t.TempDir()
	document := mobileTestDocument(t, dataDir)
	document["discovery"].(map[string]any)["enabled"] = true
	document["discovery"].(map[string]any)["advertise_url"] = "http://127.0.0.1:18090"
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := Start(string(encoded), dataDir); err != nil {
		t.Fatal(err)
	}
	announcement := `{
		"protocol":"ecs/1",
		"node_id":"ecs_0123456789abcdef0123456789abcdef",
		"node":"native-peer",
		"url":"http://192.0.2.20:18090",
		"model":"remote",
		"features":["network_auth"],
		"tools":[]
	}`
	if err := PushDiscoveryAnnouncement(announcement); err != nil {
		t.Fatal(err)
	}
	singleton.Lock()
	handler := singleton.runtime.Handler()
	singleton.Unlock()
	request := httptest.NewRequest(http.MethodGet, "/v1/nodes", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"node":"native-peer"`)) {
		t.Fatalf("native peer was not visible: %d %s", response.Code, response.Body.String())
	}
}

func TestStartRejectsInvalidConfiguration(t *testing.T) {
	if err := Start("{", t.TempDir()); err == nil {
		t.Fatal("invalid mobile configuration was accepted")
	}
}

func TestIOSCapabilitiesDescribeForegroundLeaf(t *testing.T) {
	capabilities := mobileCapabilities("ios")
	if capabilities.TopologyRole != "leaf" ||
		capabilities.AcceptsChildren ||
		!capabilities.SupportsInbound ||
		capabilities.SupportsBackground ||
		capabilities.SupportsSubprocess ||
		capabilities.SupportsMulticast {
		t.Fatalf("unexpected iOS capabilities: %#v", capabilities)
	}
}

func TestAndroidCapabilitiesRemainDiscoverablePeer(t *testing.T) {
	capabilities := mobileCapabilities("android")
	if capabilities.TopologyRole != "peer" ||
		!capabilities.AcceptsChildren ||
		!capabilities.SupportsInbound ||
		!capabilities.SupportsMulticast {
		t.Fatalf("unexpected Android capabilities: %#v", capabilities)
	}
}

func TestHarmonyOSCapabilitiesDescribeForegroundPeer(t *testing.T) {
	capabilities := mobileCapabilities("harmonyos")
	if capabilities.Name != "harmonyos" ||
		capabilities.RuntimeMode != "foreground" ||
		capabilities.TopologyRole != "peer" ||
		!capabilities.AcceptsChildren ||
		!capabilities.SupportsInbound ||
		capabilities.SupportsSubprocess ||
		!capabilities.SupportsMulticast {
		t.Fatalf("unexpected HarmonyOS capabilities: %#v", capabilities)
	}
}

func TestMobileDiscoveryNameIncludesHarmonyOS(t *testing.T) {
	if got := mobileDiscoveryName("harmonyos"); got != "harmony-discovery" {
		t.Fatalf("unexpected HarmonyOS discovery bridge name %q", got)
	}
}
