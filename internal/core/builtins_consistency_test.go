package core

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ecs/internal/config"
	"ecs/internal/skills"
	"ecs/internal/tools"
)

func TestRegisterBuiltinToolsRejectsManifestWithoutHandler(t *testing.T) {
	root := t.TempDir()
	toolDir := filepath.Join(root, "tools")
	skillDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(toolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(toolDir, "ghost.json"),
		[]byte(`{
			"name":"ghost","version":"1.0.0","description":"missing implementation",
			"command":"@builtin","input_schema":{"type":"object"}
		}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistry(config.ToolsConfig{Directory: toolDir}, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	skillRegistry := skills.NewRegistry(config.SkillsConfig{Directory: skillDir}, toolRegistry, logger)
	err := registerBuiltinTools(toolRegistry, skillRegistry)
	if err == nil || !strings.Contains(
		err.Error(),
		`tool "ghost" declares @builtin but has no registered implementation`,
	) {
		t.Fatalf("missing handler was not rejected: %v", err)
	}
}
