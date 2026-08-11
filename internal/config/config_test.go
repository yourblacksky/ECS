package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

type testSecrets map[string]string

func (s testSecrets) Get(name string) (string, error) {
	value, ok := s[name]
	if !ok {
		return "", os.ErrNotExist
	}
	return value, nil
}

func validDefaults() Config {
	cfg := defaults()
	cfg.LLM.Model = "test-model"
	return cfg
}

func TestDefaultConfigRequiresExplicitLLMModel(t *testing.T) {
	cfg := defaults()
	if cfg.LLM.Model != "" {
		t.Fatalf("default model must remain empty, got %q", cfg.LLM.Model)
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "llm.model is required") {
		t.Fatalf("missing explicit model was not rejected: %v", err)
	}
}

func TestPlatformSecretsResolveBeforeValidation(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Listen = "0.0.0.0:8090"
	cfg.Network.Enabled = true
	cfg.Discovery.AdvertiseURL = "http://192.0.2.10:8090"
	cfg.AdminTokenSecret = "admin.token"
	cfg.LLM.APIKeySecret = "llm.api_key"
	if err := applySecrets(&cfg, testSecrets{
		"admin.token": "admin-secret",
		"llm.api_key": "llm-secret",
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.AdminToken != "admin-secret" || cfg.LLM.APIKey != "llm-secret" {
		t.Fatalf("secrets were not resolved: %#v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSecretBackedPeerTokenSurvivesMissingEnvironmentOverride(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Groups["workers"] = []Peer{{
		Name:        "worker",
		URL:         "http://127.0.0.1:8091",
		TokenEnv:    "ECS_TEST_PEER_TOKEN",
		TokenSecret: "peer.token",
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ECS_TEST_PEER_TOKEN", "")
	loaded, err := LoadWithSecrets(path, testSecrets{"peer.token": "secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Groups["workers"][0].Token != "secret-value" {
		t.Fatal("empty environment override cleared a resolved platform secret")
	}
	t.Setenv("ECS_TEST_PEER_TOKEN", "environment-value")
	loaded, err = LoadWithSecrets(path, testSecrets{"peer.token": "secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Groups["workers"][0].Token != "environment-value" {
		t.Fatal("non-empty environment value did not override platform secret")
	}
}

func TestPublicListenRequiresToken(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Listen = "0.0.0.0:8090"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected public listener without token to be rejected")
	}
	cfg.AuthToken = "secret"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "admin_token") {
		t.Fatalf("shared node token unexpectedly authorized public Admin: %v", err)
	}
	cfg.AdminToken = "admin-secret"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected separate node and Admin tokens to allow public listener: %v", err)
	}
}

func TestPublicNetworkListenerAcceptsSeparateAdminToken(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Listen = "0.0.0.0:8090"
	cfg.AuthToken = ""
	cfg.AdminToken = "admin-secret"
	cfg.Network.Enabled = true
	cfg.Discovery.AdvertiseURL = "http://192.0.2.10:8090"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("network listener with separate admin token was rejected: %v", err)
	}
	cfg.Admin.Enabled = false
	if err := cfg.Validate(); err == nil {
		t.Fatal("network authentication without approval interface was accepted")
	}
}

func TestNetworkAuthenticationAlwaysRequiresAdminCredential(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Network.Enabled = true
	cfg.Discovery.AdvertiseURL = "http://127.0.0.1:8090"
	cfg.AuthToken = ""
	cfg.AdminToken = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("network authentication without an admin credential was accepted")
	}
}

func TestNetworkRouteOverridesRequireHTTPURLs(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Network.Enabled = true
	cfg.AdminToken = "admin-secret"
	cfg.Discovery.AdvertiseURL = "http://127.0.0.1:8090"
	cfg.Network.RouteOverrides["ecs_mobile"] = "http://127.0.0.1:18092"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid route override was rejected: %v", err)
	}
	cfg.Network.RouteOverrides["ecs_mobile"] = "file:///tmp/ecs.sock"
	if err := cfg.Validate(); err == nil {
		t.Fatal("non-HTTP route override was accepted")
	}
}

func TestReasoningEffortIsValidated(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.LLM.ReasoningEffort = "turbo"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid reasoning effort must fail")
	}
	cfg.LLM.ReasoningEffort = "none"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("none reasoning effort rejected: %v", err)
	}
}

func TestIoTProfilesRequireBoundedDestinations(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.IoT.HTTP["lab"] = IoTHTTPProfile{
		BaseURL:             "http://192.0.2.20:8080/api",
		AllowedMethods:      []string{"GET", "POST"},
		AllowedPathPrefixes: []string{"/device/"},
		HeadersEnv:          map[string]string{"Authorization": "ECS_IOT_LAB_TOKEN"},
		Timeout:             Duration(5 * time.Second),
	}
	cfg.IoT.MQTT["lab"] = IoTMQTTProfile{
		BrokerURL:            "mqtts://broker.example.test:8883",
		PasswordEnv:          "ECS_IOT_LAB_PASSWORD",
		AllowedTopicPrefixes: []string{"ecs/device/"},
		Timeout:              Duration(5 * time.Second),
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid IoT profiles rejected: %v", err)
	}
	cfg.IoT.HTTP["lab"] = IoTHTTPProfile{
		BaseURL: "file:///etc", AllowedMethods: []string{"GET"},
		AllowedPathPrefixes: []string{"/"}, Timeout: Duration(time.Second),
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("non-HTTP IoT endpoint was accepted")
	}
}

func TestIoTProfilesRejectBroadOrEmbeddedSecrets(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.IoT.MQTT["lab"] = IoTMQTTProfile{
		BrokerURL:            "mqtt://user:password@broker.example.test:1883",
		AllowedTopicPrefixes: []string{"#"}, Timeout: Duration(time.Second),
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("embedded MQTT credentials and wildcard prefix were accepted")
	}
}

func TestGroupDispatchConfigurationIsValidated(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Agent.GroupDispatchMode = "random"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid group dispatch mode must fail")
	}
	cfg.Agent.GroupDispatchMode = "race"
	cfg.Agent.GroupRaceFanout = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("zero race fanout must fail")
	}
	cfg.Agent.GroupRaceFanout = 2
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid race dispatch configuration rejected: %v", err)
	}
}

func TestAgentTeamsRequiresWorkerAndPromptBudget(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.AgentTeams.Enabled = true
	cfg.AgentTeams.WorkerName = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing AgentTeams worker name to fail")
	}
	cfg.AgentTeams.WorkerName = "ecs-worker"
	cfg.AgentTeams.MaxTaskPromptChars = 499
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected too-small task prompt budget to fail")
	}
	cfg.AgentTeams.MaxTaskPromptChars = 500
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid AgentTeams config: %v", err)
	}
}

func TestAgentTeamsMatrixRequiresRuntimeConfig(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.AgentTeams.Enabled = true
	cfg.AgentTeams.WorkerName = "ecs-worker"
	cfg.AgentTeams.MatrixURL = "http://matrix.local"
	cfg.AgentTeams.RuntimeConfigFile = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("Matrix mode without runtime config must fail")
	}
	cfg.AgentTeams.RuntimeConfigFile = "runtime.yaml"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid Matrix config rejected: %v", err)
	}
}

func TestSkillLimitsAreBounded(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Skills.MaxSteps = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("zero skill step limit must fail")
	}
	cfg.Skills.MaxSteps = 4
	cfg.Skills.MaxPromptSkills = 21
	if err := cfg.Validate(); err == nil {
		t.Fatal("excessive prompt skill limit must fail")
	}
}

func TestDurationMarshalsAsReadableString(t *testing.T) {
	data, err := json.Marshal(Duration(3 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"3s"` {
		t.Fatalf("unexpected duration JSON: %s", data)
	}
}

func TestRegistryDiscoveryRequiresNetworkIdentityAndController(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Discovery.AdvertiseURL = "http://127.0.0.1:8090"
	cfg.Discovery.Registry.Enabled = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("registry discovery without network identity was accepted")
	}
	cfg.Network.Enabled = true
	cfg.AdminToken = "admin-secret"
	if err := cfg.Validate(); err == nil {
		t.Fatal("registry discovery without a controller URL was accepted")
	}
	cfg.Discovery.Registry.ControllerURLs = []string{"https://registry.example.test"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid registry discovery configuration rejected: %v", err)
	}
}

func TestDiscoveryRequiresDNSServiceType(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.AdvertiseURL = "http://127.0.0.1:8090"
	cfg.Discovery.ServiceType = "ecs"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid DNS-SD service type was accepted")
	}
	cfg.Discovery.ServiceType = "_ecs._tcp"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid DNS-SD service type rejected: %v", err)
	}
}

func TestRegistryControllerCanRunWithoutJoiningAnotherRegistry(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Discovery.Registry.Serve = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("standalone registry controller configuration rejected: %v", err)
	}
}

func TestTraceConfigurationRequiresBoundedValues(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Trace.MaxValueChars = 199
	if err := cfg.Validate(); err == nil {
		t.Fatal("too-small trace value limit must fail")
	}
	cfg.Trace.MaxValueChars = 4000
	cfg.Trace.Directory = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("enabled trace requires a directory")
	}
}

func TestAdminLogCapacityIsBounded(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Admin.LogCapacity = 99
	if err := cfg.Validate(); err == nil {
		t.Fatal("too-small admin log capacity must fail")
	}
}

func TestOfficeAttemptLeaseAndReaperAreBounded(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Office.AttemptLease = Duration(9 * time.Second)
	if err := cfg.Validate(); err == nil {
		t.Fatal("too-short Office attempt lease must fail")
	}
	cfg = validDefaults()
	cfg.Discovery.Enabled = false
	cfg.Office.ReaperInterval = cfg.Office.AttemptLease + Duration(time.Second)
	if err := cfg.Validate(); err == nil {
		t.Fatal("Office reaper interval longer than the attempt lease must fail")
	}
}

func TestAgentRoleDirectoryIsRequired(t *testing.T) {
	cfg := validDefaults()
	cfg.Discovery.Enabled = false
	cfg.AgentRoles.Directory = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("agent role directory must be required")
	}
	cfg = validDefaults()
	cfg.Discovery.Enabled = false
	cfg.AgentRoles.PipelineDirectory = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("agent pipeline directory must be required")
	}
	cfg = validDefaults()
	cfg.Discovery.Enabled = false
	cfg.AgentRoles.DefaultPipeline = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("default pipeline must be required")
	}
}
