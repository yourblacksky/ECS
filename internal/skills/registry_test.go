package skills

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/platform"
	"ecs/internal/tools"
)

type unavailableToolExecutor struct{}

func (unavailableToolExecutor) ToolAvailability(platform.ToolRequest) platform.ToolAvailability {
	return platform.ToolAvailability{Reason: "permission_denied"}
}

func (unavailableToolExecutor) Execute(
	context.Context,
	platform.ToolRequest,
) (platform.ToolResult, error) {
	return platform.ToolResult{}, nil
}

func TestRegistrySelectsAndRunsSkill(t *testing.T) {
	temp := t.TempDir()
	toolsDir := filepath.Join(temp, "tools")
	skillsDir := filepath.Join(temp, "skills")
	knowledgeDir := filepath.Join(skillsDir, "_knowledge")
	if err := os.MkdirAll(filepath.Join(skillsDir, "echo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(knowledgeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	toolManifest := `{
		"name":"echo_value",
		"version":"1.0.0",
		"description":"echo JSON input",
		"command":"/bin/sh",
		"args":["-c","cat"],
		"input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}
	}`
	if err := os.WriteFile(filepath.Join(toolsDir, "echo.json"), []byte(toolManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	skillManifest := `{
		"schema_version":"ecs.skill/v1",
		"name":"echo",
		"version":"1.0.0",
		"description":"echo a value",
		"when":{"keywords":["echo"]},
		"never_keywords":["do not echo"],
		"agents":{"roles":["worker"]},
		"input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false},
		"output_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]},
		"dependencies":{"tools":[{"name":"echo_value","version":">=1.0.0"}]},
		"workflow":[{"id":"echo","tool":"echo_value","input":"$input","on_failure":"fail"}],
		"risk":{"level":"low"},
		"prompt_card":{"use":"when echo is requested","avoid":"otherwise"}
	}`
	if err := os.WriteFile(filepath.Join(skillsDir, "echo", "skill.json"), []byte(skillManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	skillDocumentation := `---
name: echo
description: Grounded instructions for the echo Skill.
---

# Echo

Only echo the explicitly supplied value. Never invent a missing value.`
	if err := os.WriteFile(
		filepath.Join(skillsDir, "echo", "SKILL.md"),
		[]byte(skillDocumentation),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	knowledgeDocumentation := `---
name: echo-policy
title: Echo handling policy
summary: Evidence for echo requests.
keywords: [evidence]
intents: [echo policy]
---

Echo evidence must preserve the exact supplied value and source.`
	if err := os.WriteFile(
		filepath.Join(knowledgeDir, "echo-policy.md"),
		[]byte(knowledgeDocumentation),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistry(config.ToolsConfig{
		Directory:      toolsDir,
		DefaultTimeout: config.Duration(time.Second),
		MaxOutputBytes: 1024,
	}, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.SkillsConfig{
		Directory:             skillsDir,
		KnowledgeDirectory:    knowledgeDir,
		MaxPromptSkills:       3,
		MaxSteps:              3,
		DocumentationRequired: true,
		GroundingEnabled:      true,
		MaxDocumentChars:      4000,
		MaxGroundingChars:     800,
		MaxKnowledgeDocuments: 2,
	}, toolRegistry, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	selected := registry.List(nil, SelectionContext{AgentRole: "worker", Prompt: "please echo"}, 3)
	if len(selected) != 1 || selected[0].Name != "echo" {
		t.Fatalf("unexpected selection: %#v", selected)
	}
	if selected[0].Documentation.Source != "SKILL.md" ||
		!strings.Contains(selected[0].Documentation.Content, "Never invent") ||
		selected[0].Documentation.ContentHash == "" {
		t.Fatalf("SKILL.md was not loaded: %#v", selected[0].Documentation)
	}
	grounding, references := registry.Grounding(
		map[string]Manifest{"echo": selected[0]},
		"please echo",
		nil,
	)
	if len(references) != 1 || references[0].Skill != "echo" ||
		!strings.Contains(grounding, "不能扩大权限") ||
		!strings.Contains(grounding, "Never invent") {
		t.Fatalf("unexpected grounding: %q %#v", grounding, references)
	}
	if value, refs := registry.Grounding(
		map[string]Manifest{"echo": selected[0]},
		"unrelated request",
		nil,
	); value != "" || len(refs) != 0 {
		t.Fatalf("irrelevant documentation leaked into grounding: %q %#v", value, refs)
	}
	if value, refs := registry.Grounding(
		map[string]Manifest{"echo": selected[0]},
		"unrelated request",
		[]string{"echo"},
	); value == "" || len(refs) != 1 || !refs[0].Explicit {
		t.Fatalf("explicitly authorized Skill was not grounded: %q %#v", value, refs)
	}
	if value, refs := registry.Grounding(
		nil,
		"find the evidence policy",
		nil,
	); value == "" || len(refs) != 1 || refs[0].Kind != "knowledge" ||
		refs[0].Document != "echo-policy" ||
		!strings.Contains(value, "exact supplied value") {
		t.Fatalf("relevant knowledge was not grounded: %q %#v", value, refs)
	}
	if value, refs := registry.Grounding(
		nil,
		"completely unrelated",
		nil,
	); value != "" || len(refs) != 0 {
		t.Fatalf("irrelevant knowledge leaked into grounding: %q %#v", value, refs)
	}
	if got := registry.List(nil, SelectionContext{AgentRole: "summarizer", Prompt: "echo"}, 3); len(got) != 0 {
		t.Fatalf("role restriction ignored: %#v", got)
	}
	if got := registry.List(nil, SelectionContext{AgentRole: "worker", Prompt: "do not echo"}, 3); len(got) != 0 {
		t.Fatalf("negative condition ignored: %#v", got)
	}
	run, err := NewRunner(registry, toolRegistry).Execute(
		context.Background(),
		"echo",
		json.RawMessage(`{"value":"hello"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "success" || run.Output != `{"value":"hello"}` || len(run.ToolRuns) != 1 {
		t.Fatalf("unexpected run: %#v", run)
	}
	invalidRun, err := NewRunner(registry, toolRegistry).Execute(
		context.Background(),
		"echo",
		json.RawMessage(`{"value":1}`),
	)
	if err == nil || !tools.IsArgumentValidationError(err) {
		t.Fatalf("skill input error must remain identifiable for correction: run=%#v err=%v", invalidRun, err)
	}
	if invalidRun.Status != "failed" || invalidRun.Error == "" || len(invalidRun.ToolRuns) != 0 {
		t.Fatalf("invalid skill input must fail before tool execution: %#v", invalidRun)
	}
	mismatchedDocumentation := strings.Replace(skillDocumentation, "name: echo", "name: other", 1)
	if err := os.WriteFile(
		filepath.Join(skillsDir, "echo", "SKILL.md"),
		[]byte(mismatchedDocumentation),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := registry.Reload(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched SKILL.md name was accepted: %v", err)
	}
	if _, ok := registry.Get("echo"); !ok {
		t.Fatal("failed documentation reload replaced the last valid registry")
	}
}

func TestRegistryRejectsMissingToolDependency(t *testing.T) {
	temp := t.TempDir()
	toolsDir := filepath.Join(temp, "tools")
	skillsDir := filepath.Join(temp, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "broken"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{
		"schema_version":"ecs.skill/v1",
		"name":"broken",
		"version":"1.0.0",
		"description":"broken skill",
		"when":{},
		"input_schema":{"type":"object"},
		"dependencies":{"tools":[{"name":"missing"}]},
		"workflow":[{"id":"run","tool":"missing"}],
		"prompt_card":{"use":"test"}
	}`
	if err := os.WriteFile(filepath.Join(skillsDir, "broken", "skill.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistry(config.ToolsConfig{Directory: toolsDir}, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.SkillsConfig{
		Directory:       skillsDir,
		MaxPromptSkills: 3,
		MaxSteps:        3,
	}, toolRegistry, logger)
	if err := registry.Reload(); err == nil {
		t.Fatal("missing dependency must fail reload")
	}
}

func TestRegistryFiltersSkillsBeforeDependencyValidation(t *testing.T) {
	temp := t.TempDir()
	toolsDir := filepath.Join(temp, "tools")
	skillsDir := filepath.Join(temp, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "mobile"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{
		"schema_version":"ecs.skill/v1",
		"name":"mobile",
		"version":"1.0.0",
		"description":"mobile only",
		"platforms":["android","ios"],
		"when":{},
		"input_schema":{"type":"object"},
		"dependencies":{"tools":[{"name":"mobile_tool"}]},
		"workflow":[{"id":"run","tool":"mobile_tool"}],
		"prompt_card":{"use":"on mobile"}
	}`
	if err := os.WriteFile(filepath.Join(skillsDir, "mobile", "skill.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistry(config.ToolsConfig{Directory: toolsDir}, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.SkillsConfig{
		Directory:       skillsDir,
		MaxPromptSkills: 3,
		MaxSteps:        3,
	}, toolRegistry, logger)
	registry.SetPlatform("linux")
	if err := registry.Reload(); err != nil {
		t.Fatalf("filtered skill must not validate unavailable dependencies: %v", err)
	}
	if len(registry.All()) != 0 {
		t.Fatal("mobile skill was exposed on Linux")
	}
}

func TestRegistryFiltersSkillWithUnavailableToolDependency(t *testing.T) {
	temp := t.TempDir()
	toolsDir := filepath.Join(temp, "tools")
	skillsDir := filepath.Join(temp, "skills", "flashlight")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	toolManifest := `{
		"name":"flashlight","version":"1.0.0","description":"flashlight",
		"command":"@device","args":["flashlight"],"input_schema":{"type":"object"}
	}`
	skillManifest := `{
		"schema_version":"ecs.skill/v1","name":"flashlight","version":"1.0.0",
		"description":"flashlight skill","when":{"keywords":["light"]},
		"input_schema":{"type":"object"},
		"dependencies":{"tools":[{"name":"flashlight"}]},
		"workflow":[{"id":"run","tool":"flashlight"}],
		"prompt_card":{"use":"control light"}
	}`
	if err := os.WriteFile(filepath.Join(toolsDir, "flashlight.json"), []byte(toolManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "skill.json"), []byte(skillManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistryWithExecutor(
		config.ToolsConfig{Directory: toolsDir},
		logger,
		unavailableToolExecutor{},
	)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.SkillsConfig{
		Directory: filepath.Join(temp, "skills"), MaxPromptSkills: 3, MaxSteps: 3,
	}, toolRegistry, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := registry.List(nil, SelectionContext{Prompt: "light"}, 3); len(got) != 0 {
		t.Fatalf("skill with unavailable dependency was selected: %#v", got)
	}
	if got := registry.Capabilities(); len(got) != 0 {
		t.Fatalf("skill with unavailable dependency was advertised: %#v", got)
	}
	status := registry.Availability("flashlight")
	if status.Available || status.Reason != "tool_dependency_unavailable" {
		t.Fatalf("unexpected skill availability: %#v", status)
	}
}
