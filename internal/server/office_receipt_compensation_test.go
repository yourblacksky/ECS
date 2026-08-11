package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/office"
	"ecs/internal/protocol"
	"ecs/internal/toolbox"
	"ecs/internal/tools"
)

func TestOfficeAndAgentTeamsUseSameReceiptContract(t *testing.T) {
	root := t.TempDir()
	manifests := map[string]string{
		"dispatch.json": `{
			"name":"office_action_dispatch","version":"1.0.0","description":"dispatch",
			"capabilities":["office.task.write"],"command":"@builtin",
			"input_schema":{"type":"object"},"output_schema":{"type":"object"}
		}`,
		"verify.json": `{
			"name":"execution_receipt_verify","version":"1.0.0","description":"verify",
			"command":"@builtin","input_schema":{"type":"object"},"output_schema":{"type":"object"}
		}`,
		"compensate.json": `{
			"name":"office_action_compensate","version":"1.0.0","description":"compensate",
			"capabilities":["office.task.write"],"command":"@builtin",
			"input_schema":{"type":"object","required":["action","work_order","task_id","idempotency_key"],
			"properties":{"action":{"type":"string"},"work_order":{"type":"string"},"task_id":{"type":"string"},
			"idempotency_key":{"type":"string"},"receipts":{"type":"array"}}},
			"output_schema":{"type":"object"}
		}`,
	}
	for name, value := range manifests {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistry(config.ToolsConfig{
		Directory: root, DefaultTimeout: config.Duration(time.Second), MaxOutputBytes: 4096,
	}, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"office_action_dispatch", "execution_receipt_verify", "office_action_compensate"} {
		if err := registry.RegisterBuiltin(name, toolbox.Handler(name)); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{
		registry: registry,
		cfg:      config.Config{HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20}},
	}
	normalTools := toolsForConnectors(map[string][]string{
		"office.task.write": {"office_action_dispatch", "office_action_compensate"},
	}, []string{"office.task.write"})
	if len(normalTools) != 1 || normalTools[0] != "office_action_dispatch" {
		t.Fatalf("compensation tool leaked into normal execution permissions: %#v", normalTools)
	}
	task := office.Task{
		ID: "office_1.dispatch", StageID: "dispatch",
		Connectors: []string{"office.task.write"},
		Produces:   []office.ArtifactSpec{{Name: "dispatch_receipts", Required: true}},
		Compensation: &office.Compensation{
			Action: "cancel_created_items", Connector: "office.task.write",
		},
	}
	idempotencyKey := officeTaskIdempotencyKey("office_1", task.ID)
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	dispatchOutput, _ := json.Marshal(map[string]any{"receipts": []map[string]string{{
		"receipt_id": "receipt-1", "action": "create task", "target": "QA",
		"result": "created", "external_reference": "task://1",
		"idempotency_key": idempotencyKey + ":0", "created_at": createdAt,
	}}})
	response := protocol.ReplyResponse{SkillRuns: []protocol.SkillRun{
		{
			Name: "office.action.dispatch", Status: "success",
			ToolRuns: []protocol.ToolRun{{
				Name: "office_action_dispatch", Output: string(dispatchOutput),
			}},
		},
		{
			Name: "execution.receipt.verify", Status: "success",
			ToolRuns: []protocol.ToolRun{{
				Name: "execution_receipt_verify", Output: `{"valid":true,"count":1,"invalid_receipts":[]}`,
			}},
		},
	}}
	request := officeTaskExecutionRequest{
		WorkOrder: "office_1", TaskID: task.ID,
		Connectors: task.Connectors, IdempotencyKey: idempotencyKey,
		Approval: &office.ApprovalRecord{PolicyVersion: "policy-v1"},
	}
	for _, path := range []string{"office_dispatch", "agentteams_office"} {
		t.Run(path, func(t *testing.T) {
			receipts, verification, err := server.validateOfficeReceipts(
				request, task, response, time.Now().UTC(),
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(receipts) != 1 || receipts[0].ApprovalPolicyVersion != "policy-v1" ||
				verification == nil || verification.Status != "verified" {
				t.Fatalf("receipt evidence was not strongly bound: %#v %#v", receipts, verification)
			}
		})
	}

	badRequest := request
	badRequest.IdempotencyKey = "office:another-order:dispatch"
	if _, _, err := server.validateOfficeReceipts(
		badRequest, task, response, time.Now().UTC(),
	); err == nil {
		t.Fatal("receipt from another work order was accepted")
	}

	order := office.WorkOrder{ID: "office_1"}
	compensation := server.executeLocalOfficeCompensation(context.Background(), order, task)
	if compensation.Status != "compensated" || len(compensation.Receipts) != 1 ||
		compensation.Receipts[0].Result != "compensated" {
		t.Fatalf("real compensation receipt was not returned: %#v", compensation)
	}

	worker := httptest.NewServer(http.HandlerFunc(server.executeOfficeCompensation))
	defer worker.Close()
	server.client = worker.Client()
	remoteCompensation := server.callOfficeCompensation(
		context.Background(), config.Peer{Name: "worker", URL: worker.URL},
		order, task,
		map[string][]string{"office.task.write": {"office_action_compensate"}},
	)
	if remoteCompensation.Status != "compensated" ||
		len(remoteCompensation.Receipts) != 1 {
		t.Fatalf("Office Dispatch compensation path failed: %#v", remoteCompensation)
	}
}
