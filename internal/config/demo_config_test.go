package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThreeNodeDemoConfigsLoadAndValidate(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	expected := map[string]string{
		"controller.json": "ecs-controller",
		"worker-a.json":   "ecs-worker-a",
		"worker-b.json":   "ecs-worker-b",
	}
	for file, node := range expected {
		t.Run(node, func(t *testing.T) {
			cfg, err := Load(filepath.Join(root, "demo", "three-node", file))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.NodeName != node || cfg.Listen != "0.0.0.0:8090" ||
				cfg.LLM.Model != "ecs-mock" {
				t.Fatalf("unexpected demo config: %#v", cfg)
			}
			for _, path := range []string{
				cfg.Agent.SystemPromptFile,
				cfg.AgentRoles.Directory,
				cfg.Tools.Directory,
				cfg.Skills.Directory,
				cfg.Office.Directory,
			} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("demo runtime resource %s is unavailable: %v", path, err)
				}
			}
			if node == "ecs-controller" {
				if len(cfg.Groups["demo"]) != 2 || len(cfg.Nested.Children) != 2 {
					t.Fatalf("controller topology is incomplete: %#v", cfg)
				}
			}
		})
	}
}
