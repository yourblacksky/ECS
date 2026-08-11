package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateHarmonyNetworkDefaults(t *testing.T) {
	dataDir := t.TempDir()
	writeMigrationConfig(t, dataDir, map[string]any{
		"listen":       "127.0.0.1:18090",
		"discovery":    map[string]any{"enabled": false},
		"network_auth": map[string]any{"enabled": false},
	})
	if err := migrateHarmonyNetworkDefaults(dataDir); err != nil {
		t.Fatal(err)
	}
	document := readMigrationConfig(t, dataDir)
	if document["listen"] != "0.0.0.0:18090" ||
		document["admin_token_secret"] != "admin.token" ||
		!document["discovery"].(map[string]any)["enabled"].(bool) ||
		!document["network_auth"].(map[string]any)["enabled"].(bool) {
		t.Fatalf("legacy HarmonyOS configuration was not migrated: %#v", document)
	}
	if _, err := os.Stat(filepath.Join(dataDir, harmonyNetworkMigration)); err != nil {
		t.Fatalf("migration marker was not written: %v", err)
	}
}

func TestMigrateHarmonyNetworkDefaultsPreservesCustomConfiguration(t *testing.T) {
	dataDir := t.TempDir()
	writeMigrationConfig(t, dataDir, map[string]any{
		"listen":             "127.0.0.1:19090",
		"admin_token_secret": "custom.admin",
		"discovery":          map[string]any{"enabled": false},
		"network_auth":       map[string]any{"enabled": false},
	})
	if err := migrateHarmonyNetworkDefaults(dataDir); err != nil {
		t.Fatal(err)
	}
	document := readMigrationConfig(t, dataDir)
	if document["listen"] != "127.0.0.1:19090" ||
		document["admin_token_secret"] != "custom.admin" ||
		document["discovery"].(map[string]any)["enabled"].(bool) ||
		document["network_auth"].(map[string]any)["enabled"].(bool) {
		t.Fatalf("custom HarmonyOS configuration was changed: %#v", document)
	}
}

func TestMigrateHarmonyLLMDefaults(t *testing.T) {
	dataDir := t.TempDir()
	writeMigrationConfig(t, dataDir, map[string]any{
		"llm": map[string]any{
			"base_url": "https://api.deepseek.com/",
			"model":    "deepseek-chat",
		},
	})
	if err := migrateHarmonyLLMDefaults(dataDir); err != nil {
		t.Fatal(err)
	}
	llm := readMigrationConfig(t, dataDir)["llm"].(map[string]any)
	if llm["model"] != "deepseek-v4-flash" {
		t.Fatalf("legacy DeepSeek model was not migrated: %#v", llm)
	}
}

func TestMigrateHarmonyLLMDefaultsPreservesCustomConfiguration(t *testing.T) {
	tests := []map[string]any{
		{"base_url": "https://provider.example/v1", "model": "deepseek-chat"},
		{"base_url": "https://api.deepseek.com", "model": "custom-model"},
	}
	for _, llm := range tests {
		dataDir := t.TempDir()
		writeMigrationConfig(t, dataDir, map[string]any{"llm": llm})
		if err := migrateHarmonyLLMDefaults(dataDir); err != nil {
			t.Fatal(err)
		}
		got := readMigrationConfig(t, dataDir)["llm"].(map[string]any)
		if got["base_url"] != llm["base_url"] || got["model"] != llm["model"] {
			t.Fatalf("custom LLM configuration was changed: %#v", got)
		}
	}
}

func writeMigrationConfig(t *testing.T, dataDir string, document map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readMigrationConfig(t *testing.T, dataDir string) map[string]any {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	return document
}
