package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"ecs/internal/agent"
	"ecs/internal/agentteams"
	"ecs/internal/config"
	"ecs/internal/discovery"
	"ecs/internal/office"
	"ecs/internal/protocol"
	"ecs/internal/session"
	"ecs/internal/skills"
	"ecs/internal/tools"
)

func TestOfficeHumanApprovalBlocksConnectorUntilAdminDecision(t *testing.T) {
	root := t.TempDir()
	toolDir := filepath.Join(root, "tools")
	skillDir := filepath.Join(root, "skills")
	componentDir := filepath.Join(root, "office")
	for _, directory := range []string{toolDir, filepath.Join(skillDir, "dispatch"), componentDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(toolDir, "connector.json"): `{
			"name":"connector_tool","version":"1.0.0","description":"write task",
			"capabilities":["office.task.write"],"command":"/usr/bin/true",
			"input_schema":{"type":"object"}
		}`,
		filepath.Join(skillDir, "dispatch", "skill.json"): `{
			"schema_version":"ecs.skill/v1","name":"dispatch_skill","version":"1.0.0",
			"description":"dispatch task","input_schema":{"type":"object"},
			"dependencies":{"tools":[{"name":"connector_tool"}]},
			"workflow":[{"id":"dispatch","tool":"connector_tool","input":"$input"}],
			"risk":{"level":"high","approval":"required"},
			"prompt_card":{"use":"dispatch approved task"}
		}`,
		filepath.Join(componentDir, "approval.json"): `{
			"schema_version":"ecs.office/v1","name":"approval_flow","version":"1.0.0",
			"description":"approval flow",
			"team":[
				{"name":"planner","role":"planner","purpose":"plan"},
				{"name":"worker","role":"executor","purpose":"dispatch"},
				{"name":"reviewer","role":"verifier","purpose":"review"}
			],
			"input_schema":{"type":"object","required":["title"],"properties":{"title":{"type":"string"}}},
			"connectors":[{"capability":"office.task.write","required":true}],
			"stages":[{
				"id":"dispatch","name":"Dispatch","agent":"worker","purpose":"create task",
				"skills":["dispatch_skill"],"connectors":["office.task.write"],
				"approval":{"mode":"human","risk":"high","required_scopes":["task:write"]},
				"on_failure":"fail"
			}]
		}`,
	}
	for path, value := range files {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var connectorCalls atomic.Int32
	var workerFails atomic.Bool
	var worker *httptest.Server
	worker = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(protocol.NodeCapabilities{
				Node: "worker", URL: worker.URL,
				Tools: []protocol.ToolCapability{{
					Name: "connector_tool", Capabilities: []string{"office.task.write"},
				}},
				Skills: []protocol.SkillCapability{{
					Name: "dispatch_skill", Tools: []string{"connector_tool"},
				}},
			})
		case "/v1/office/tasks/execute":
			connectorCalls.Add(1)
			if workerFails.Load() {
				http.Error(w, "connection outcome unavailable", http.StatusBadGateway)
				return
			}
			var request officeTaskExecutionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode Office task: %v", err)
			}
			if err := office.ValidateApprovalRecord(
				request.Approval, request.Skills, request.Tools, request.Connectors, time.Now().UTC(),
			); err != nil {
				t.Errorf("worker received invalid approval grant: %v", err)
			}
			_ = json.NewEncoder(w).Encode(protocol.ReplyResponse{
				RequestID: request.RequestID, TraceID: request.TraceID, Node: "worker",
				Message: protocol.Message{Role: "assistant", Content: "created"},
				SkillRuns: []protocol.SkillRun{{
					Name: "dispatch_skill", Status: "success",
					ToolRuns: []protocol.ToolRun{{Name: "connector_tool"}},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer worker.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		NodeName: "controller", AdminToken: "admin-secret",
		Admin: config.AdminConfig{Enabled: true},
		Agent: config.AgentConfig{ApprovalMode: "high_risk"},
		LLM:   config.LLMConfig{BaseURL: "http://127.0.0.1:1", Timeout: config.Duration(time.Second)},
		Tools: config.ToolsConfig{
			Directory: toolDir, DefaultTimeout: config.Duration(time.Second), MaxOutputBytes: 1024,
		},
		Skills: config.SkillsConfig{Directory: skillDir, MaxPromptSkills: 4, MaxSteps: 4},
		AgentTeams: config.AgentTeamsConfig{
			Enabled: true, WorkerName: "approval-worker",
			TaskDirectory: filepath.Join(root, "agentteams"), MaxTaskPromptChars: 4000,
		},
		Office: config.OfficeConfig{
			Directory: componentDir, OrderDirectory: filepath.Join(root, "orders"),
			MaxStages: 8, MaxContextEntries: 8, ApprovalTTL: config.Duration(10 * time.Minute),
		},
		Sessions: config.SessionsConfig{Directory: filepath.Join(root, "sessions"), ContextMessages: 10},
		HTTP: config.HTTPConfig{
			MaxBodyBytes: 1 << 20, MaxConcurrent: 2,
			PeerTimeout: config.Duration(time.Second), RetryBackoff: config.Duration(time.Millisecond),
		},
		Discovery: config.DiscoveryConfig{AdvertiseURL: "http://controller"},
		Groups: map[string][]config.Peer{
			"office": {{Name: "worker", URL: worker.URL}},
		},
	}
	toolRegistry := tools.NewRegistry(cfg.Tools, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	skillRegistry := skills.NewRegistry(cfg.Skills, toolRegistry, logger)
	if err := skillRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	officeRegistry := office.NewRegistry(cfg.Office, toolRegistry, logger)
	officeRegistry.SetSkillRegistry(skillRegistry)
	if err := officeRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	order, err := officeRegistry.Compile(
		"approval_flow", office.CompileRequest{Input: json.RawMessage(`{"title":"launch"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionStore, err := session.New(cfg.Sessions)
	if err != nil {
		t.Fatal(err)
	}
	discoveryManager := discovery.New(
		cfg.Discovery, cfg.Nested, cfg.NodeName, cfg.LLM.Model,
		cfg.AuthToken, cfg.Agent.ApprovalMode, toolRegistry, logger,
	)
	handler := NewWithOfficeRuntime(
		cfg, agent.NewWithSkills(cfg.Agent, cfg.LLM, toolRegistry, skillRegistry, logger),
		toolRegistry, skillRegistry, nil, officeRegistry, sessionStore,
		discoveryManager, logger, nil, nil,
	)

	dispatch := func(orderID string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(
			http.MethodPost, "/v1/office/work-orders/"+orderID+"/dispatch",
			bytes.NewBufferString(`{"group":"office"}`),
		)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	first := dispatch(order.ID)
	if first.Code != http.StatusAccepted || connectorCalls.Load() != 0 {
		t.Fatalf("connector ran before approval: status=%d calls=%d body=%s", first.Code, connectorCalls.Load(), first.Body.String())
	}
	var pending office.WorkOrder
	if err := json.Unmarshal(first.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	approval := pending.Tasks[0].ApprovalRecord
	if pending.Status != "pending_approval" || approval == nil || approval.Status != "pending" {
		t.Fatalf("pending approval was not returned: %#v", pending)
	}

	unauthorized := httptest.NewRequest(
		http.MethodPost,
		"/v1/admin/office/work-orders/"+order.ID+"/tasks/dispatch/approval",
		bytes.NewBufferString(`{"approval_id":"`+approval.ID+`","decision":"approve"}`),
	)
	unauthorizedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin approval was accepted: %d %s", unauthorizedRecorder.Code, unauthorizedRecorder.Body.String())
	}

	approve := httptest.NewRequest(
		http.MethodPost,
		"/v1/admin/office/work-orders/"+order.ID+"/tasks/dispatch/approval",
		bytes.NewBufferString(`{"approval_id":"`+approval.ID+`","decision":"approve","actor":"QA负责人","opinion":"同意执行"}`),
	)
	approve.Header.Set("Authorization", "Bearer admin-secret")
	approveRecorder := httptest.NewRecorder()
	handler.ServeHTTP(approveRecorder, approve)
	if approveRecorder.Code != http.StatusOK {
		t.Fatalf("approval failed: %d %s", approveRecorder.Code, approveRecorder.Body.String())
	}
	second := dispatch(order.ID)
	if second.Code != http.StatusOK || connectorCalls.Load() != 1 {
		t.Fatalf("approved connector did not run exactly once: status=%d calls=%d body=%s", second.Code, connectorCalls.Load(), second.Body.String())
	}
	var completed office.WorkOrder
	if err := json.Unmarshal(second.Body.Bytes(), &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" ||
		completed.Tasks[0].ApprovalRecord == nil ||
		completed.Tasks[0].ApprovalRecord.DecidedBy != "QA负责人" ||
		len(completed.Tasks[0].Attempts) != 1 ||
		completed.Tasks[0].Attempts[0].Status != office.AttemptSucceeded ||
		len(completed.Outbox) != 1 ||
		completed.Outbox[0].Status != office.OutboxConfirmed {
		t.Fatalf("approval evidence was not retained: %#v", completed)
	}

	agentTeamsOrder, err := officeRegistry.Compile(
		"approval_flow", office.CompileRequest{Input: json.RawMessage(`{"title":"agentteams"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	agentTeamsBody, _ := json.Marshal(agentteams.TaskRequest{
		ProjectID: "approval-project", TaskID: "approval-agent-task",
		Objective: "dispatch through the controlled Office stage",
		Office: &agentteams.OfficeTaskRef{
			WorkOrderID: agentTeamsOrder.ID, StageID: "dispatch",
		},
	})
	agentTeamsRequest := httptest.NewRequest(
		http.MethodPost, "/v1/agentteams/tasks", bytes.NewReader(agentTeamsBody),
	)
	agentTeamsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(agentTeamsRecorder, agentTeamsRequest)
	if agentTeamsRecorder.Code != http.StatusAccepted || connectorCalls.Load() != 1 {
		t.Fatalf(
			"AgentTeams bypassed approval: status=%d calls=%d body=%s",
			agentTeamsRecorder.Code, connectorCalls.Load(), agentTeamsRecorder.Body.String(),
		)
	}
	var agentTeamsPending agentteams.TaskResult
	if err := json.Unmarshal(agentTeamsRecorder.Body.Bytes(), &agentTeamsPending); err != nil {
		t.Fatal(err)
	}
	if agentTeamsPending.Status != "PENDING_APPROVAL" ||
		agentTeamsPending.Meta == nil ||
		agentTeamsPending.Meta.Status != "pending_approval" {
		t.Fatalf("AgentTeams pending approval state was not retained: %#v", agentTeamsPending)
	}
	if _, err := os.Stat(filepath.Join(root, "agentteams", "tasks", "approval-agent-task", "result.json")); !os.IsNotExist(err) {
		t.Fatalf("pending AgentTeams task was incorrectly finalized: %v", err)
	}

	rejectedOrder, err := officeRegistry.Compile(
		"approval_flow", office.CompileRequest{Input: json.RawMessage(`{"title":"reject"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	rejectPendingResponse := dispatch(rejectedOrder.ID)
	var rejectPending office.WorkOrder
	if err := json.Unmarshal(rejectPendingResponse.Body.Bytes(), &rejectPending); err != nil {
		t.Fatal(err)
	}
	rejectApproval := rejectPending.Tasks[0].ApprovalRecord
	reject := httptest.NewRequest(
		http.MethodPost,
		"/v1/admin/office/work-orders/"+rejectedOrder.ID+"/tasks/dispatch/approval",
		bytes.NewBufferString(`{"approval_id":"`+rejectApproval.ID+`","decision":"reject","actor":"QA负责人","opinion":"信息不完整"}`),
	)
	reject.Header.Set("Authorization", "Bearer admin-secret")
	rejectRecorder := httptest.NewRecorder()
	handler.ServeHTTP(rejectRecorder, reject)
	if rejectRecorder.Code != http.StatusOK {
		t.Fatalf("rejection failed: %d %s", rejectRecorder.Code, rejectRecorder.Body.String())
	}
	var rejected office.WorkOrder
	if err := json.Unmarshal(rejectRecorder.Body.Bytes(), &rejected); err != nil {
		t.Fatal(err)
	}
	if rejected.Status != "rejected" || rejected.Tasks[0].ApprovalRecord.Status != "rejected" {
		t.Fatalf("rejection was not persisted: %#v", rejected)
	}
	rejectedDispatch := dispatch(rejectedOrder.ID)
	if rejectedDispatch.Code != http.StatusConflict || connectorCalls.Load() != 1 {
		t.Fatalf("rejected connector was executed: status=%d calls=%d body=%s", rejectedDispatch.Code, connectorCalls.Load(), rejectedDispatch.Body.String())
	}

	recoveryOrder, err := officeRegistry.Compile(
		"approval_flow", office.CompileRequest{Input: json.RawMessage(`{"title":"recover"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	recoveryPendingResponse := dispatch(recoveryOrder.ID)
	var recoveryPending office.WorkOrder
	if err := json.Unmarshal(recoveryPendingResponse.Body.Bytes(), &recoveryPending); err != nil {
		t.Fatal(err)
	}
	recoveryApproval := recoveryPending.Tasks[0].ApprovalRecord
	recoveryApprove := httptest.NewRequest(
		http.MethodPost,
		"/v1/admin/office/work-orders/"+recoveryOrder.ID+"/tasks/dispatch/approval",
		bytes.NewBufferString(`{"approval_id":"`+recoveryApproval.ID+`","decision":"approve","actor":"QA负责人"}`),
	)
	recoveryApprove.Header.Set("Authorization", "Bearer admin-secret")
	recoveryApproveRecorder := httptest.NewRecorder()
	handler.ServeHTTP(recoveryApproveRecorder, recoveryApprove)
	if recoveryApproveRecorder.Code != http.StatusOK {
		t.Fatalf("recovery approval failed: %d %s", recoveryApproveRecorder.Code, recoveryApproveRecorder.Body.String())
	}
	workerFails.Store(true)
	unknownResponse := dispatch(recoveryOrder.ID)
	if unknownResponse.Code != http.StatusAccepted {
		t.Fatalf("unknown outcome did not enter recovery: %d %s", unknownResponse.Code, unknownResponse.Body.String())
	}
	var unknown office.WorkOrder
	if err := json.Unmarshal(unknownResponse.Body.Bytes(), &unknown); err != nil {
		t.Fatal(err)
	}
	if unknown.Status != office.StatusRecoveryRequired ||
		unknown.Tasks[0].Attempts[0].Status != office.AttemptOutcomeUnknown ||
		unknown.Outbox[0].Status != office.OutboxRecoveryRequired {
		t.Fatalf("unknown outcome recovery evidence missing: %#v", unknown)
	}
	workerFails.Store(false)
	recoveredResponse := dispatch(recoveryOrder.ID)
	if recoveredResponse.Code != http.StatusOK {
		t.Fatalf("recovery dispatch failed: %d %s", recoveredResponse.Code, recoveredResponse.Body.String())
	}
	var recovered office.WorkOrder
	if err := json.Unmarshal(recoveredResponse.Body.Bytes(), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "completed" ||
		len(recovered.Tasks[0].Attempts) != 2 ||
		recovered.Outbox[0].Status != office.OutboxSuperseded ||
		recovered.Outbox[1].Status != office.OutboxConfirmed {
		t.Fatalf("recovery did not preserve and resolve attempt history: %#v", recovered)
	}
}
