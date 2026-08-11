package office

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/roles"
	"ecs/internal/skills"
	"ecs/internal/tools"
)

func TestCompilePersistsStructuredWorkOrder(t *testing.T) {
	root := t.TempDir()
	components := filepath.Join(root, "office.d")
	orders := filepath.Join(root, "orders")
	if err := os.MkdirAll(components, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := validTestManifest()
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(components, "brief.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.OfficeConfig{
		Directory: components, OrderDirectory: orders, MaxStages: 8, MaxContextEntries: 8,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	order, err := registry.Compile("brief", CompileRequest{
		Input:           json.RawMessage(`{"topic":"edge AI"}`),
		Context:         map[string]string{"tenant": "demo"},
		RequestedBy:     "tester",
		OwnerPeerNodeID: "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.Ready || len(order.MissingConnectors) != 1 {
		t.Fatalf("expected missing connector, got ready=%v missing=%v", order.Ready, order.MissingConnectors)
	}
	if len(order.Tasks) != 3 || len(order.Handoffs) != 2 {
		t.Fatalf("unexpected graph: tasks=%d handoffs=%d", len(order.Tasks), len(order.Handoffs))
	}
	loaded, err := registry.LoadOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Component != "brief" || loaded.Tasks[2].AgentRole != "verifier" {
		t.Fatalf("unexpected persisted order: %+v", loaded)
	}
	summaries, err := registry.ListOrders("brief", "planned", "peer-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].ID != order.ID ||
		summaries[0].Tasks != 3 || summaries[0].Handoffs != 2 {
		t.Fatalf("unexpected work order summaries: %+v", summaries)
	}
	otherOwner, err := registry.ListOrders("brief", "planned", "peer-b", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherOwner) != 0 {
		t.Fatalf("work order leaked to another owner: %+v", otherOwner)
	}
}

func TestReloadRejectsOfficeTeamWithoutInstalledAgentPrincipal(t *testing.T) {
	root := t.TempDir()
	components := filepath.Join(root, "office.d")
	if err := os.MkdirAll(components, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := validTestManifest()
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(components, "brief.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.OfficeConfig{
		Directory: components, OrderDirectory: filepath.Join(root, "orders"), MaxStages: 8,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	registry.SetAgentRegistry(roles.NewRegistry(config.AgentRolesConfig{}, slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err := registry.Reload(); err == nil || !strings.Contains(err.Error(), `team agent "planner" is not installed`) {
		t.Fatalf("missing Office Agent principal did not fail closed: %v", err)
	}
}

func TestLegacyOfficeWorkOrderIsLocalOnly(t *testing.T) {
	if !OwnedBy(WorkOrder{}, "local") {
		t.Fatal("legacy work order should remain available to the local owner")
	}
	if OwnedBy(WorkOrder{}, "peer-a") {
		t.Fatal("legacy work order must not be exposed to a remote peer")
	}
}

func TestAgentTeamsTaskMapsToOfficeStageAndAdvancesDependencies(t *testing.T) {
	registry := newTestRegistry(t, validTestManifest())
	order, err := registry.Compile("brief", CompileRequest{
		Input: json.RawMessage(`{"topic":"edge AI"}`), OwnerPeerNodeID: "peer-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.StartAgentTeamsStage(
		order.ID, "research", "at-research", "project-1", "trace-research", "peer-a",
	); err == nil {
		t.Fatal("dependent stage must not start before its dependency completes")
	}
	mapping, err := registry.StartAgentTeamsStage(
		order.ID, "plan", "at-plan", "project-1", "trace-plan", "peer-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.OfficeTaskID != order.ID+".plan" || mapping.Status != "running" {
		t.Fatalf("unexpected running mapping: %#v", mapping)
	}
	if _, err := registry.StartAgentTeamsStage(
		order.ID, "plan", "another-task", "project-1", "trace-other", "peer-a",
	); err == nil {
		t.Fatal("stage must not be remapped to another AgentTeams task")
	}
	mapping, err = registry.FinishAgentTeamsStage(
		order.ID, "plan", "at-plan", "peer-a", json.RawMessage(`{"result":"planned"}`), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.Status != "completed" {
		t.Fatalf("unexpected completed mapping: %#v", mapping)
	}
	verifiedAt := time.Now().UTC()
	if _, err := registry.SetTaskReceipts(
		order.ID, "plan", "peer-a",
		[]ExecutionReceipt{{
			ReceiptID: "receipt-1", IdempotencyKey: "office:" + order.ID + ":plan:0",
			VerifiedAt: verifiedAt,
		}},
		&ReceiptVerification{
			Status: "verified", ExpectedPrefix: "office:" + order.ID + ":plan:",
			Count: 1, VerifiedAt: verifiedAt,
		},
	); err != nil {
		t.Fatal(err)
	}
	mapping, err = registry.StartAgentTeamsStage(
		order.ID, "research", "at-research", "project-1", "trace-research", "peer-a",
	)
	if err != nil || mapping.Status != "running" {
		t.Fatalf("dependent stage did not start: mapping=%#v err=%v", mapping, err)
	}
	mapping, err = registry.FinishAgentTeamsStage(
		order.ID, "research", "at-research", "peer-a", nil, errors.New("evidence missing"),
	)
	if err != nil || mapping.Status != "failed" {
		t.Fatalf("blocked task status was not mapped: mapping=%#v err=%v", mapping, err)
	}
	loaded, err := registry.LoadOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AgentTeamsProjectID != "project-1" ||
		loaded.Tasks[0].AgentTeamsTaskID != "at-plan" ||
		loaded.Tasks[0].TraceID != "trace-plan" ||
		len(loaded.Tasks[0].Receipts) != 1 ||
		loaded.Tasks[0].ReceiptVerification == nil ||
		loaded.Status != "failed" {
		t.Fatalf("cross-system IDs were not persisted: %#v", loaded)
	}
}

func TestOfficeApprovalLifecyclePersistsDecisionAndExpiry(t *testing.T) {
	manifest := validTestManifest()
	manifest.Stages[0].Approval = ApprovalPolicy{
		Mode: "human", Risk: "high", RequiredScopes: []string{"task:write"},
	}
	registry := newTestRegistry(t, manifest)
	order, err := registry.Compile("brief", CompileRequest{Input: json.RawMessage(`{"topic":"ECS"}`)})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	order, pending, approved, err := registry.EnsureTaskApproval(
		order.ID, "plan", "controller", nil, nil, nil, now,
	)
	if err != nil || approved || pending.Status != "pending" ||
		order.Status != "pending_approval" || order.Tasks[0].Status != "pending_approval" {
		t.Fatalf("approval was not persisted as pending: order=%#v record=%#v approved=%v err=%v", order, pending, approved, err)
	}
	_, duplicate, approved, err := registry.EnsureTaskApproval(
		order.ID, "plan", "controller", nil, nil, nil, now.Add(time.Minute),
	)
	if err != nil || approved || duplicate.ID != pending.ID {
		t.Fatalf("pending approval was not idempotent: %#v approved=%v err=%v", duplicate, approved, err)
	}
	order, err = registry.DecideTaskApproval(
		order.ID, "plan", pending.ID, "approve", "李经理", "同意执行", now.Add(2*time.Minute),
	)
	if err != nil || order.Tasks[0].ApprovalRecord == nil ||
		order.Tasks[0].ApprovalRecord.Status != "approved" ||
		order.Tasks[0].ApprovalRecord.DecidedBy != "李经理" ||
		order.Tasks[0].ApprovalRecord.Opinion != "同意执行" {
		t.Fatalf("approval decision was not persisted: order=%#v err=%v", order, err)
	}
	if err := ValidateApprovalRecord(
		order.Tasks[0].ApprovalRecord, nil, nil, nil, now.Add(3*time.Minute),
	); err != nil {
		t.Fatalf("valid approval record was rejected: %v", err)
	}
	_, _, approved, err = registry.EnsureTaskApproval(
		order.ID, "plan", "controller", nil, nil, nil, now.Add(3*time.Minute),
	)
	if err != nil || !approved {
		t.Fatalf("approved task could not continue: approved=%v err=%v", approved, err)
	}

	expiringOrder, err := registry.Compile("brief", CompileRequest{Input: json.RawMessage(`{"topic":"expiry"}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, expiring, _, err := registry.EnsureTaskApproval(
		expiringOrder.ID, "plan", "controller", nil, nil, nil, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	expiringOrder, err = registry.DecideTaskApproval(
		expiringOrder.ID, "plan", expiring.ID, "approve", "late-admin", "",
		now.Add(16*time.Minute),
	)
	if !errors.Is(err, ErrApprovalExpired) ||
		expiringOrder.Tasks[0].ApprovalRecord.Status != "expired" {
		t.Fatalf("expired approval was not blocked and persisted: order=%#v err=%v", expiringOrder, err)
	}
}

func TestReloadRejectsCycle(t *testing.T) {
	root := t.TempDir()
	manifest := validTestManifest()
	manifest.Stages[0].DependsOn = []string{"verify"}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "bad.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.OfficeConfig{
		Directory: root, OrderDirectory: filepath.Join(root, "orders"), MaxStages: 8, MaxContextEntries: 8,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := registry.Reload(); err == nil {
		t.Fatal("expected cyclic component to be rejected")
	}
}

func TestCapabilityBecomesReadyWhenConnectorToolIsAvailable(t *testing.T) {
	root := t.TempDir()
	toolDir := filepath.Join(root, "tools")
	componentDir := filepath.Join(root, "office")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := map[string]any{
		"name": "search_connector", "description": "test", "command": "true",
		"capabilities": []string{"office.knowledge.search"},
		"input_schema": map[string]any{"type": "object"},
	}
	data, _ := json.Marshal(tool)
	if err := os.WriteFile(filepath.Join(toolDir, "search.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistry(config.ToolsConfig{Directory: toolDir}, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	manifest := validTestManifest()
	data, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(componentDir, "brief.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.OfficeConfig{
		Directory: componentDir, OrderDirectory: filepath.Join(root, "orders"), MaxStages: 8, MaxContextEntries: 8,
	}, toolRegistry, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	capabilities := registry.Capabilities()
	if len(capabilities) != 1 || !capabilities[0].Ready {
		t.Fatalf("expected ready component, got %+v", capabilities)
	}
}

func TestStageSkillMustImplementDeclaredConnector(t *testing.T) {
	root := t.TempDir()
	toolDir := filepath.Join(root, "tools")
	skillDir := filepath.Join(root, "skills")
	componentDir := filepath.Join(root, "office")
	for _, directory := range []string{toolDir, filepath.Join(skillDir, "research"), componentDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	toolManifest := `{
		"name":"research_connector","version":"1.0.0","description":"research connector",
		"capabilities":["office.knowledge.search"],"command":"/usr/bin/true",
		"input_schema":{"type":"object"}
	}`
	skillManifest := `{
		"schema_version":"ecs.skill/v1","name":"research","version":"1.0.0",
		"description":"research skill","input_schema":{"type":"object"},
		"dependencies":{"tools":[{"name":"research_connector"}]},
		"workflow":[{"id":"search","tool":"research_connector","input":"$input"}],
		"prompt_card":{"use":"research"}
	}`
	if err := os.WriteFile(filepath.Join(toolDir, "research.json"), []byte(toolManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "research", "skill.json"), []byte(skillManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistry(config.ToolsConfig{Directory: toolDir}, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	skillRegistry := skills.NewRegistry(config.SkillsConfig{
		Directory: skillDir, MaxPromptSkills: 4, MaxSteps: 4,
	}, toolRegistry, logger)
	if err := skillRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	manifest := validTestManifest()
	manifest.Stages[1].Skills = []string{"research"}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(componentDir, "brief.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.OfficeConfig{
		Directory: componentDir, OrderDirectory: filepath.Join(root, "orders"),
		MaxStages: 8, MaxContextEntries: 8,
	}, toolRegistry, logger)
	registry.SetSkillRegistry(skillRegistry)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	order, err := registry.Compile("brief", CompileRequest{Input: json.RawMessage(`{"topic":"ECS"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(order.Tasks[1].Skills) != 1 || order.Tasks[1].Skills[0] != "research" {
		t.Fatalf("stage skills were not compiled into the work order: %#v", order.Tasks[1])
	}

	manifest.Stages[1].Connectors = []string{"office.knowledge.search", "office.web.search"}
	manifest.Connectors = append(manifest.Connectors, ConnectorRequirement{
		Capability: "office.web.search",
	})
	data, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(componentDir, "brief.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := registry.Reload(); err == nil {
		t.Fatal("connector without a declared skill dependency must be rejected")
	}
}

func newTestRegistry(t *testing.T, manifest Manifest) *Registry {
	t.Helper()
	root := t.TempDir()
	components := filepath.Join(root, "office.d")
	if err := os.MkdirAll(components, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(components, manifest.Name+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(config.OfficeConfig{
		Directory: components, OrderDirectory: filepath.Join(root, "orders"),
		MaxStages: 8, MaxContextEntries: 8,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	return registry
}

func validTestManifest() Manifest {
	return Manifest{
		SchemaVersion: ManifestSchema, Name: "brief", Version: "1.0.0", Description: "Research brief",
		Team: []AgentIdentity{
			{Name: "planner", Role: "planner", Purpose: "plan"},
			{Name: "researcher", Role: "executor", Purpose: "research"},
			{Name: "reviewer", Role: "verifier", Purpose: "verify"},
		},
		InputSchema: map[string]any{
			"type": "object", "required": []any{"topic"},
			"properties": map[string]any{"topic": map[string]any{"type": "string"}},
		},
		Connectors: []ConnectorRequirement{{Capability: "office.knowledge.search", Required: true}},
		Stages: []Stage{
			{ID: "plan", Name: "Plan", Agent: "planner", Purpose: "plan", Produces: []ArtifactSpec{{Name: "plan", Required: true}}},
			{ID: "research", Name: "Research", Agent: "researcher", Purpose: "research", DependsOn: []string{"plan"},
				Connectors: []string{"office.knowledge.search"}, Consumes: []string{"plan"},
				Produces: []ArtifactSpec{{Name: "evidence", Required: true}}},
			{ID: "verify", Name: "Verify", Agent: "reviewer", Purpose: "verify", DependsOn: []string{"research"},
				Consumes: []string{"evidence"}, Produces: []ArtifactSpec{{Name: "brief", Required: true}}},
		},
	}
}
