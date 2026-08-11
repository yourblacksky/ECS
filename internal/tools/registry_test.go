package tools

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
	"ecs/internal/schema"
)

type availabilityExecutor struct {
	status platform.ToolAvailability
}

func (e *availabilityExecutor) ToolAvailability(platform.ToolRequest) platform.ToolAvailability {
	return e.status
}

func (e *availabilityExecutor) Execute(
	context.Context,
	platform.ToolRequest,
) (platform.ToolResult, error) {
	return platform.ToolResult{Stdout: `{}`}, nil
}

func TestValidateArguments(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{"type": "string", "enum": []any{"上海", "北京"}},
		},
		"required":             []any{"city"},
		"additionalProperties": false,
	}
	if err := validateArguments(json.RawMessage(`{"city":"上海"}`), schema); err != nil {
		t.Fatalf("valid arguments rejected: %v", err)
	}
	for _, raw := range []string{`{}`, `{"city":1}`, `{"city":"深圳"}`, `{"city":"上海","extra":true}`} {
		if err := validateArguments(json.RawMessage(raw), schema); err == nil {
			t.Fatalf("invalid arguments accepted: %s", raw)
		}
	}
	if err := ValidateArguments(json.RawMessage(`{"city":"深圳"}`), schema); !IsArgumentValidationError(err) {
		t.Fatalf("expected typed argument validation error, got %T: %v", err, err)
	}
}

func TestValidateArgumentsSupportsStringOrEnumArray(t *testing.T) {
	pattern := map[string]any{
		"oneOf": []any{
			map[string]any{
				"type": "string",
				"enum": []any{"email", "date"},
			},
			map[string]any{
				"type":        "array",
				"minItems":    float64(1),
				"uniqueItems": true,
				"items": map[string]any{
					"type": "string",
					"enum": []any{"email", "date"},
				},
			},
		},
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": pattern,
		},
		"required":             []any{"pattern"},
		"additionalProperties": false,
	}
	for _, raw := range []string{
		`{"pattern":"email"}`,
		`{"pattern":["email","date"]}`,
	} {
		if err := validateArguments(json.RawMessage(raw), schema); err != nil {
			t.Fatalf("valid pattern rejected: raw=%s err=%v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"pattern":"email,date"}`,
		`{"pattern":[]}`,
		`{"pattern":["email","email"]}`,
		`{"pattern":["email","phone"]}`,
	} {
		if err := validateArguments(json.RawMessage(raw), schema); err == nil {
			t.Fatalf("invalid pattern accepted: %s", raw)
		}
	}
}

func TestValidateArgumentsSupportsNumericAndLengthBounds(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{
				"type": "integer", "minimum": float64(1), "maximum": float64(3),
			},
			"title": map[string]any{
				"type": "string", "minLength": float64(1), "maxLength": float64(4),
			},
		},
		"required":             []any{"count", "title"},
		"additionalProperties": false,
	}
	if err := validateArguments(json.RawMessage(`{"count":2,"title":"提醒"}`), schema); err != nil {
		t.Fatalf("valid bounded arguments rejected: %v", err)
	}
	for _, raw := range []string{
		`{"count":0,"title":"提醒"}`,
		`{"count":4,"title":"提醒"}`,
		`{"count":1.5,"title":"提醒"}`,
		`{"count":1,"title":""}`,
		`{"count":1,"title":"五个字符呀"}`,
	} {
		if err := validateArguments(json.RawMessage(raw), schema); err == nil {
			t.Fatalf("out-of-range arguments accepted: %s", raw)
		}
	}
}

func TestPrepareArgumentsUsesModelAndCanonicalSchemas(t *testing.T) {
	manifest := Manifest{
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []any{"text"}, "additionalProperties": false,
		},
		CanonicalSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"payload": map[string]any{
					"type":       "object",
					"properties": map[string]any{"text": map[string]any{"type": "string"}},
					"required":   []any{"text"}, "additionalProperties": false,
				},
			},
			"required": []any{"payload"}, "additionalProperties": false,
		},
		InputAdapter: schema.Adapter{
			Version:  "ecs.adapter/v1",
			Mappings: map[string]string{"payload.text": "text"},
		},
	}
	prepared, missing, err := PrepareArguments(
		json.RawMessage(`{"text":"hello"}`), manifest, schema.RuntimeContext{},
	)
	if err != nil || len(missing) != 0 || string(prepared) != `{"payload":{"text":"hello"}}` {
		t.Fatalf("unexpected prepared arguments: %s missing=%v err=%v", prepared, missing, err)
	}
}

func TestRegistryExecutesRegisteredBuiltin(t *testing.T) {
	directory := t.TempDir()
	manifest := `{
		"name":"catalog",
		"description":"test built-in",
		"command":"@builtin",
		"input_schema":{"type":"object","additionalProperties":false},
		"output_schema":{
			"type":"object",
			"properties":{"ok":{"type":"boolean"}},
			"required":["ok"]
		}
	}`
	if err := os.WriteFile(filepath.Join(directory, "catalog.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.ToolsConfig{
		Directory: directory, DefaultTimeout: config.Duration(time.Second),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterBuiltin(
		"catalog",
		func(context.Context, json.RawMessage) (string, error) {
			return `{"ok":true}`, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	output, err := registry.Execute(context.Background(), "catalog", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if output != `{"ok":true}` {
		t.Fatalf("unexpected built-in output: %s", output)
	}
}

func TestBuiltinAvailabilityCanReportConfiguredConnectorState(t *testing.T) {
	directory := t.TempDir()
	manifest := `{
		"name":"connector","description":"configured connector","command":"@builtin",
		"input_schema":{"type":"object","additionalProperties":false}
	}`
	if err := os.WriteFile(filepath.Join(directory, "connector.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.ToolsConfig{Directory: directory}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterBuiltinWithAvailability(
		"connector",
		func(context.Context, json.RawMessage) (string, error) { return `{}`, nil },
		func() platform.ToolAvailability {
			return platform.ToolAvailability{Reason: "connector_unconfigured"}
		},
	); err != nil {
		t.Fatal(err)
	}
	if got := registry.Availability("connector"); got.Available || got.Reason != "connector_unconfigured" {
		t.Fatalf("unexpected connector availability: %#v", got)
	}
	if _, err := registry.Execute(context.Background(), "connector", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "connector_unconfigured") {
		t.Fatalf("unavailable connector executed: %v", err)
	}
}

func TestRegistryFiltersToolsByPlatform(t *testing.T) {
	directory := t.TempDir()
	manifest := `{
		"name":"mobile_only",
		"description":"mobile hardware",
		"platforms":["android","ios"],
		"command":"@device",
		"input_schema":{"type":"object"}
	}`
	if err := os.WriteFile(filepath.Join(directory, "mobile.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := NewRegistry(config.ToolsConfig{Directory: directory}, logger)
	registry.SetPlatform("linux")
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(registry.All()) != 0 {
		t.Fatal("android/iOS tool was exposed on Linux")
	}
	registry.SetPlatform("Android")
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("mobile_only"); !ok {
		t.Fatal("Android tool was filtered from Android host")
	}
}

func TestRegistryFiltersAndRechecksDynamicAvailability(t *testing.T) {
	directory := t.TempDir()
	manifest := `{
		"name":"flashlight",
		"description":"mobile flashlight",
		"command":"@device",
		"args":["flashlight"],
		"input_schema":{"type":"object"}
	}`
	if err := os.WriteFile(filepath.Join(directory, "flashlight.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &availabilityExecutor{
		status: platform.ToolAvailability{Reason: "permission_denied"},
	}
	registry := NewRegistryWithExecutor(config.ToolsConfig{
		Directory: directory, DefaultTimeout: config.Duration(time.Second),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), executor)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := registry.List(nil, 10); len(got) != 0 {
		t.Fatalf("unavailable tool entered callable list: %#v", got)
	}
	if _, err := registry.Execute(context.Background(), "flashlight", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unavailable tool execution was not rejected")
	}
	executor.status = platform.ToolAvailability{Available: true}
	if got := registry.List(nil, 10); len(got) != 1 || got[0].Name != "flashlight" {
		t.Fatalf("newly available tool did not enter callable list: %#v", got)
	}
}
