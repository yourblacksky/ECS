package adminauth

import (
	"os"
	"path/filepath"
	"testing"

	"ecs/internal/config"
)

func TestEnsureLocalTokenCreatesAndReusesPrivateCredential(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{Admin: config.AdminConfig{Enabled: true}}
	first, err := EnsureLocalToken(configPath, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Generated || cfg.AdminToken == "" || first.Fingerprint == "" {
		t.Fatalf("unexpected generated token result: %#v", first)
	}
	info, err := os.Stat(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %o, want 600", info.Mode().Perm())
	}

	firstToken := cfg.AdminToken
	cfg.AdminToken = ""
	second, err := EnsureLocalToken(configPath, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generated || cfg.AdminToken != firstToken {
		t.Fatalf("token was not reused: %#v", second)
	}
}

func TestEnsureLocalTokenDoesNotReplaceExplicitCredential(t *testing.T) {
	cfg := config.Config{
		AdminToken: "explicit-token",
		Admin:      config.AdminConfig{Enabled: true},
	}
	result, err := EnsureLocalToken(filepath.Join(t.TempDir(), "config.json"), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "" || cfg.AdminToken != "explicit-token" {
		t.Fatalf("explicit credential was changed: %#v", result)
	}
}
