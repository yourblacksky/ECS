package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/tools"
)

func TestToolFileUpdateReloadsAndRollsBack(t *testing.T) {
	directory := t.TempDir()
	original := []byte(`{
		"name":"demo","version":"1.0.0","description":"before",
		"command":"@ecs","input_schema":{"type":"object"}
	}`)
	path := filepath.Join(directory, "demo.json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistry(config.ToolsConfig{
		Directory: directory, DefaultTimeout: config.Duration(time.Second), MaxOutputBytes: 1024,
	}, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		cfg:      config.Config{Tools: config.ToolsConfig{Directory: directory}},
		registry: registry,
		logger:   logger,
	}
	updated := []byte(`{
		"name":"demo","version":"1.0.0","description":"after",
		"command":"@ecs","input_schema":{"type":"object"}
	}`)
	if err := server.updateToolFile("demo", updated, false); err != nil {
		t.Fatal(err)
	}
	manifest, ok := registry.Get("demo")
	if !ok || manifest.Description != "after" {
		t.Fatalf("tool was not hot reloaded: %#v", manifest)
	}
	if err := server.updateToolFile("demo", []byte(`{"broken":`), false); err == nil {
		t.Fatal("invalid update must fail")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var restored tools.Manifest
	if err := json.Unmarshal(data, &restored); err != nil || restored.Description != "after" {
		t.Fatalf("failed update was not rolled back: %v %s", err, data)
	}
}

func TestConfigRedactionHidesNestedSecrets(t *testing.T) {
	value := map[string]any{
		"auth_token": "secret",
		"llm":        map[string]any{"api_key": "key", "model": "test"},
		"peers":      []any{map[string]any{"token": "peer-secret", "name": "peer"}},
	}
	redactSecrets(value)
	data, _ := json.Marshal(value)
	text := string(data)
	for _, secret := range []string{"secret", "peer-secret", `"key"`} {
		if contains := stringContains(text, secret); contains {
			t.Fatalf("secret %q leaked in %s", secret, text)
		}
	}
}

func TestAdminPeerResolutionOnlyAllowsConfiguredTargets(t *testing.T) {
	server := &Server{cfg: config.Config{Nested: config.NestedConfig{Children: []config.Peer{{
		Name: "worker", URL: "http://worker.local", Token: "secret",
	}}}}}
	peer, ok := server.resolveAdminPeer("configured:worker")
	if !ok || peer.URL != "http://worker.local" || peer.Token != "secret" {
		t.Fatalf("configured peer not resolved: %#v %v", peer, ok)
	}
	if _, ok := server.resolveAdminPeer("configured:not-present"); ok {
		t.Fatal("unknown configured node must be rejected")
	}
	if _, ok := server.resolveAdminPeer("http://arbitrary.example"); ok {
		t.Fatal("arbitrary URLs must not be accepted")
	}
}

func stringContains(value string, target string) bool {
	for index := 0; index+len(target) <= len(value); index++ {
		if value[index:index+len(target)] == target {
			return true
		}
	}
	return false
}
