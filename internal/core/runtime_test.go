package core

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/platform"
)

func TestResolvedLLMLogIncludesEffectiveRuntimeWithoutAPIKey(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logResolvedLLM(logger, config.LLMConfig{
		BaseURL:         "http://127.0.0.1:11434/v1",
		APIKey:          "must-not-be-logged",
		Model:           "qwen3:4b-q4_K_M",
		NativeTools:     true,
		ReasoningEffort: "none",
	})
	logged := output.String()
	for _, expected := range []string{
		"LLM runtime resolved",
		"http://127.0.0.1:11434/v1",
		"qwen3:4b-q4_K_M",
		`"native_tools":true`,
		`"reasoning_effort":"none"`,
	} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("resolved LLM log %q is missing %q", logged, expected)
		}
	}
	if strings.Contains(logged, "must-not-be-logged") {
		t.Fatalf("resolved LLM log exposed API key: %q", logged)
	}
}

func TestRuntimeLifecycleAndPlatformStatus(t *testing.T) {
	cfg := testConfig(t)
	host := platform.NewDefaultHost(platform.RuntimePaths{
		Config: "test-config.json",
		Data:   t.TempDir(),
		Cache:  t.TempDir(),
	})
	node, err := New(cfg, host)
	if err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 2; cycle++ {
		if err := node.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		status := node.Status()
		if status.Version != "0.3.0" || !status.Running || status.Tools != len(node.tools.Available()) ||
			status.Skills != len(node.skills.Available()) || status.Tools < 10 || status.Skills < 10 ||
			status.Agents != 15 || status.Pipelines != 1 ||
			status.Platform.Name == "" {
			t.Fatalf("unexpected runtime status: %#v", status)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := node.Stop(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if node.Status().Running {
			t.Fatal("runtime remained active after Stop")
		}
	}
}

func TestAllShippedToolsAreInProcess(t *testing.T) {
	cfg := testConfig(t)
	node, err := New(cfg, platform.NewDefaultHost(platform.RuntimePaths{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, manifest := range node.tools.All() {
		if manifest.Command != "@builtin" {
			t.Fatalf("tool %q still uses %q", manifest.Name, manifest.Command)
		}
		registration, configured := extraBuiltinRegistration(
			manifest.Name, []map[string]builtinRegistration{iotBuiltinRegistrations(node.iot)},
		)
		handler := registration.handler
		if !configured {
			handler = builtinToolHandler(manifest.Name, node.tools, node.skills)
		}
		if handler == nil {
			t.Fatalf("tool %q has a manifest but no builtin handler", manifest.Name)
		}
		availability := node.tools.Availability(manifest.Name)
		if configured {
			if availability.Available || availability.Reason != "connector_unconfigured" {
				t.Fatalf("unconfigured IoT tool %q has unexpected availability: %#v", manifest.Name, availability)
			}
			continue
		}
		if !availability.Available {
			t.Fatalf("tool %q is not callable: %s", manifest.Name, availability.Reason)
		}
	}
	output, err := node.tools.Execute(
		context.Background(),
		"calculate",
		json.RawMessage(`{"a":125,"op":"divide","b":8}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != `{"result":15.625}` {
		t.Fatalf("unexpected in-process result: %s", output)
	}
}

func TestShippedMeetingOfficeSkillsResolveToConnectorTools(t *testing.T) {
	cfg := testConfig(t)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Office.Directory = filepath.Join(root, "office.d")
	cfg.Office.OrderDirectory = filepath.Join(t.TempDir(), "orders")
	node, err := New(cfg, platform.NewDefaultHost(platform.RuntimePaths{}))
	if err != nil {
		t.Fatal(err)
	}
	manifest, ok := node.office.Get("meeting_followup")
	if !ok {
		t.Fatal("meeting_followup component was not loaded")
	}
	if len(manifest.Stages) != 4 ||
		len(manifest.Stages[1].Skills) != 1 ||
		len(manifest.Stages[2].Skills) != 1 ||
		len(manifest.Stages[3].Skills) != 2 {
		t.Fatalf("unexpected meeting Skill contracts: %#v", manifest.Stages)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
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
	document["trace"].(map[string]any)["directory"] = filepath.Join(t.TempDir(), "traces")
	document["sessions"].(map[string]any)["directory"] = filepath.Join(t.TempDir(), "sessions")
	document["network_auth"].(map[string]any)["directory"] = filepath.Join(t.TempDir(), "network")
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
