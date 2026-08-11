package core

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/skills"
	"ecs/internal/tools"
)

func TestCapabilitiesBuiltinReadsLiveRegistries(t *testing.T) {
	root := t.TempDir()
	toolsDirectory := filepath.Join(root, "tools")
	skillsDirectory := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDirectory, "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, manifest := range map[string]string{
		"capabilities.json": `{
			"name":"capabilities","version":"1.1.0",
			"description":"list capabilities","command":"@builtin",
			"input_schema":{"type":"object","additionalProperties":false}
		}`,
		"clock.json": `{
			"name":"clock","version":"1.1.0",
			"description":"read clock","command":"/usr/bin/true",
			"input_schema":{"type":"object","additionalProperties":false}
		}`,
	} {
		if err := os.MkdirAll(toolsDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(toolsDirectory, name), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	skillManifest := `{
		"schema_version":"ecs.skill/v1",
		"name":"catalog","version":"1.0.0",
		"description":"catalog skill",
		"when":{},"input_schema":{"type":"object"},
		"dependencies":{"tools":[{"name":"capabilities","version":">=1.1.0"}]},
		"workflow":[{"id":"catalog","tool":"capabilities","input":"$input"}],
		"prompt_card":{"use":"when capabilities are requested"}
	}`
	if err := os.WriteFile(
		filepath.Join(skillsDirectory, "catalog", "skill.json"),
		[]byte(skillManifest),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistry(config.ToolsConfig{
		Directory: toolsDirectory, DefaultTimeout: config.Duration(time.Second),
	}, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	skillRegistry := skills.NewRegistry(config.SkillsConfig{
		Directory: skillsDirectory, MaxPromptSkills: 10, MaxSteps: 3,
	}, toolRegistry, logger)
	if err := skillRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := registerBuiltinTools(toolRegistry, skillRegistry); err != nil {
		t.Fatal(err)
	}

	output, err := toolRegistry.Execute(
		context.Background(),
		"capabilities",
		json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		ToolCount  int                     `json:"tool_count"`
		SkillCount int                     `json:"skill_count"`
		Tools      []capabilityCatalogItem `json:"tools"`
		Skills     []capabilityCatalogItem `json:"skills"`
	}
	if err := json.Unmarshal([]byte(output), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.ToolCount != 2 || catalog.SkillCount != 1 {
		t.Fatalf("unexpected catalog counts: %#v", catalog)
	}
	if catalog.Tools[0].Name != "capabilities" ||
		catalog.Tools[1].Name != "clock" ||
		catalog.Skills[0].Name != "catalog" {
		t.Fatalf("catalog is not sorted or complete: %#v", catalog)
	}
}
