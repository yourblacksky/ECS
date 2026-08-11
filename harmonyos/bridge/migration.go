package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const harmonyNetworkMigration = ".harmony-network-v1"

const (
	deepSeekBaseURL      = "https://api.deepseek.com"
	legacyDeepSeekModel  = "deepseek-chat"
	defaultDeepSeekModel = "deepseek-v4-flash"
)

// migrateHarmonyNetworkDefaults upgrades installations created before the
// native NetworkKit discovery bridge existed. It runs once and only replaces
// the complete set of old HarmonyOS defaults, preserving custom configurations.
func migrateHarmonyNetworkDefaults(dataDir string) error {
	markerPath := filepath.Join(dataDir, harmonyNetworkMigration)
	if _, err := os.Stat(markerPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	configPath := filepath.Join(dataDir, "config.json")
	content, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		return err
	}
	adminTokenSecret, _ := document["admin_token_secret"].(string)
	discovery, hasDiscovery := document["discovery"].(map[string]any)
	discoveryEnabled, hasDiscoveryEnabled := discovery["enabled"].(bool)
	network, hasNetwork := document["network_auth"].(map[string]any)
	networkEnabled, hasNetworkEnabled := network["enabled"].(bool)
	legacyDefaults := document["listen"] == "127.0.0.1:18090" &&
		strings.TrimSpace(adminTokenSecret) == "" &&
		hasDiscovery && hasDiscoveryEnabled && !discoveryEnabled &&
		hasNetwork && hasNetworkEnabled && !networkEnabled
	if legacyDefaults {
		document["listen"] = "0.0.0.0:18090"
		document["admin_token_secret"] = "admin.token"
		discovery["enabled"] = true
		network["enabled"] = true
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		return err
	}
	return os.WriteFile(markerPath, []byte("native mDNS/DNS-SD enabled\n"), 0o600)
}

// migrateHarmonyLLMDefaults repairs the retired model name shipped in older
// installs. Custom providers and explicit custom models are never rewritten.
func migrateHarmonyLLMDefaults(dataDir string) error {
	configPath := filepath.Join(dataDir, "config.json")
	content, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		return err
	}
	llm, ok := document["llm"].(map[string]any)
	if !ok {
		return nil
	}
	baseURL, _ := llm["base_url"].(string)
	model, _ := llm["model"].(string)
	officialProvider := strings.EqualFold(strings.TrimRight(strings.TrimSpace(baseURL), "/"), deepSeekBaseURL)
	if !officialProvider || strings.TrimSpace(model) != legacyDeepSeekModel {
		return nil
	}
	llm["model"] = defaultDeepSeekModel
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(encoded, '\n'), 0o600)
}
