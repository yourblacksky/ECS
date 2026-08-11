package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ecs/internal/agent"
	"ecs/internal/authz"
	"ecs/internal/config"
	"ecs/internal/protocol"
	"ecs/internal/tools"
)

func TestRunDirectToolExecutesExactArgumentsWithoutModel(t *testing.T) {
	registry := directToolRegistry(t)
	server := &Server{
		cfg:      config.Config{Agent: config.AgentConfig{ApprovalMode: "off"}},
		registry: registry,
	}
	request := protocol.ReplyRequest{
		DirectTool: &protocol.ToolInvocation{
			Name:      "calculate",
			Arguments: json.RawMessage(`{"a":2,"b":3}`),
		},
	}
	response, err := server.runDirectTool(context.Background(), authz.Trusted(request))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != `{"result":5}` || len(response.ToolRuns) != 1 ||
		response.ToolRuns[0].Name != "calculate" ||
		string(response.ToolRuns[0].Arguments) != `{"a":2,"b":3}` {
		t.Fatalf("unexpected direct tool response: %#v", response)
	}
}

func TestRunDirectToolPreservesApprovalPolicy(t *testing.T) {
	registry := directToolRegistry(t)
	server := &Server{
		cfg:      config.Config{Agent: config.AgentConfig{ApprovalMode: "all"}},
		registry: registry,
	}
	request := protocol.ReplyRequest{
		DirectTool: &protocol.ToolInvocation{
			Name:      "calculate",
			Arguments: json.RawMessage(`{"a":2,"b":3}`),
		},
	}
	if _, err := server.runDirectTool(context.Background(), authz.Trusted(request)); err == nil {
		t.Fatal("unapproved direct tool was executed")
	} else {
		var approval *agent.ApprovalError
		if !errors.As(err, &approval) || approval.Tool != "calculate" {
			t.Fatalf("unexpected approval error: %v", err)
		}
	}
	request.ApprovedTools = []string{"calculate"}
	if _, err := server.runDirectTool(context.Background(), authz.Trusted(request)); err != nil {
		t.Fatalf("approved direct tool failed: %v", err)
	}
}

func directToolRegistry(t *testing.T) *tools.Registry {
	t.Helper()
	directory := t.TempDir()
	manifest := `{
		"name":"calculate",
		"description":"calculate",
		"command":"@builtin",
		"timeout":"1s",
		"risk":"low",
		"input_schema":{
			"type":"object",
			"properties":{"a":{"type":"number"},"b":{"type":"number"}},
			"required":["a","b"],
			"additionalProperties":false
		}
	}`
	if err := os.WriteFile(filepath.Join(directory, "calculate.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(config.ToolsConfig{
		Directory:      directory,
		DefaultTimeout: config.Duration(time.Second),
		MaxOutputBytes: 1024,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterBuiltin("calculate", func(
		_ context.Context,
		arguments json.RawMessage,
	) (string, error) {
		var input struct {
			A float64 `json:"a"`
			B float64 `json:"b"`
		}
		if err := json.Unmarshal(arguments, &input); err != nil {
			return "", err
		}
		result, _ := json.Marshal(map[string]float64{"result": input.A + input.B})
		return string(result), nil
	}); err != nil {
		t.Fatal(err)
	}
	return registry
}
