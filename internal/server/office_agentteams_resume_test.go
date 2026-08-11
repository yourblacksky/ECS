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
	"testing"
	"time"

	"ecs/internal/agent"
	"ecs/internal/agentteams"
	"ecs/internal/config"
	"ecs/internal/discovery"
	"ecs/internal/office"
	"ecs/internal/roles"
	"ecs/internal/session"
	"ecs/internal/skills"
	"ecs/internal/tools"
)

// TestAgentTeamsTaskResumesAfterApproval verifies the human-approval gate
// closes the AgentTeams -> Office loop: a task that first lands in
// PENDING_APPROVAL must resume in place (not replay the stale pending state)
// once an admin approves the bound stage.
func TestAgentTeamsTaskResumesAfterApproval(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"execute\":true,\"arguments\":{}}"}}]}`)
	}))
	defer llm.Close()

	root := t.TempDir()
	toolDir := filepath.Join(root, "tools")
	skillDir := filepath.Join(root, "skills")
	officeDir := filepath.Join(root, "office")
	agentDir := filepath.Join(root, "agents")
	pipelineDir := filepath.Join(root, "pipelines")
	agentTeamsDir := filepath.Join(root, "agentteams")
	promptPath := filepath.Join(root, "prompt.txt")
	for _, directory := range []string{toolDir, filepath.Join(skillDir, "dispatch"), officeDir, agentDir, pipelineDir, agentTeamsDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		promptPath: "必须使用指定能力。",
		filepath.Join(toolDir, "connector.json"): `{
			"name":"connector_tool","description":"connector","capabilities":["office.task.write"],
			"command":"/usr/bin/true",
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
		filepath.Join(officeDir, "approval.json"): `{
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
		filepath.Join(agentDir, "worker.json"): `{
			"schema_version":"ecs.agent/v1","name":"worker","purpose":"dispatch approved tasks",
			"execution_mode":"llm",
			"allowed_skills":["dispatch_skill"],"allowed_tools":["connector_tool"],
			"input_schema":{"type":"object"},"output_schema":{"type":"object"},
			"failure_policy":{"max_retries":0,"on_failure":"return_error"}
		}`,
		filepath.Join(pipelineDir, "default.json"): `{
			"schema_version":"ecs.pipeline/v1","name":"default",
			"strategy":"capability_single_task",
			"executors":["worker"],"fallback_executor":"worker"
		}`,
	}
	for path, value := range files {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		NodeName: "office-worker", AdminToken: "admin-secret",
		Admin: config.AdminConfig{Enabled: true},
		Agent: config.AgentConfig{
			SystemPromptFile: promptPath, MaxToolRounds: 2, MaxPromptTools: 4,
			MaxSystemPromptChars: 4000, MaxToolCatalogChars: 1000, ApprovalMode: "off",
		},
		LLM: config.LLMConfig{
			BaseURL: llm.URL, Model: "test", Timeout: config.Duration(2 * time.Second),
		},
		Tools: config.ToolsConfig{
			Directory: toolDir, DefaultTimeout: config.Duration(time.Second), MaxOutputBytes: 1024,
		},
		Skills: config.SkillsConfig{
			Directory: skillDir, MaxPromptSkills: 4, MaxSteps: 4,
		},
		Office: config.OfficeConfig{
			Directory: officeDir, OrderDirectory: filepath.Join(root, "orders"),
			MaxStages: 8, MaxContextEntries: 8,
		},
		Sessions: config.SessionsConfig{
			Directory: filepath.Join(root, "sessions"), ContextMessages: 10,
		},
		HTTP: config.HTTPConfig{
			MaxBodyBytes: 1 << 20, MaxConcurrent: 2, PeerTimeout: config.Duration(time.Second),
		},
		Discovery: config.DiscoveryConfig{AdvertiseURL: "http://office-worker"},
		AgentTeams: config.AgentTeamsConfig{
			Enabled: true, WorkerName: "ecs-worker-1", TaskDirectory: agentTeamsDir,
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
	roleRegistry := roles.NewRegistry(config.AgentRolesConfig{
		Directory: agentDir, PipelineDirectory: pipelineDir, DefaultPipeline: "default",
	}, logger)
	if err := roleRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	officeRegistry := office.NewRegistry(cfg.Office, toolRegistry, logger)
	officeRegistry.SetSkillRegistry(skillRegistry)
	if err := officeRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	sessionStore, err := session.New(cfg.Sessions)
	if err != nil {
		t.Fatal(err)
	}
	runner := agent.NewWithRuntimeAndTransport(cfg.Agent, cfg.LLM, toolRegistry, skillRegistry, roleRegistry, logger, nil)
	discoveryManager := discovery.New(
		cfg.Discovery, cfg.Nested, cfg.NodeName, cfg.LLM.Model,
		cfg.AuthToken, cfg.Agent.ApprovalMode, toolRegistry, logger,
	)
	handler := NewWithOfficeRuntime(
		cfg, runner, toolRegistry, skillRegistry, roleRegistry, officeRegistry,
		sessionStore, discoveryManager, logger, nil, nil,
	)

	createBody, _ := json.Marshal(map[string]any{
		"requested_by": "agentteams-e2e",
		"input":        map[string]string{"title": "approve and resume"},
	})
	createRequest := httptest.NewRequest(
		http.MethodPost, "/v1/office/components/approval_flow/work-orders", bytes.NewReader(createBody),
	)
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, createRequest)
	if createRecorder.Code != http.StatusOK && createRecorder.Code != http.StatusCreated {
		t.Fatalf("work order creation failed: %d %s", createRecorder.Code, createRecorder.Body.String())
	}
	var order office.WorkOrder
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &order); err != nil {
		t.Fatal(err)
	}

	task := agentteams.TaskRequest{
		ProjectID: "approval-project", TaskID: "approval-resume-task",
		Objective: "dispatch through the controlled Office stage",
		Office: &agentteams.OfficeTaskRef{
			WorkOrderID: order.ID, StageID: "dispatch",
		},
	}
	body, _ := json.Marshal(task)
	submit := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/agentteams/tasks", bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	first := submit()
	if first.Code != http.StatusAccepted {
		t.Fatalf("first submission did not gate on approval: %d %s", first.Code, first.Body.String())
	}
	var pending agentteams.TaskResult
	if err := json.Unmarshal(first.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Status != "PENDING_APPROVAL" || pending.Meta == nil ||
		pending.Meta.Status != "pending_approval" {
		t.Fatalf("unexpected pending state: %#v", pending)
	}
	// Persist the pending response to reproduce a stale AgentTeams result from
	// an external/shared controller. The second submission must now enter the
	// LoadResult -> agentTeamsApprovalReadyToResume branch; without that guard it
	// would replay this PENDING_APPROVAL result forever.
	pendingData, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	pendingResultPath := filepath.Join(agentTeamsDir, "tasks", task.TaskID, "result.json")
	if err := os.WriteFile(pendingResultPath, pendingData, 0o600); err != nil {
		t.Fatal(err)
	}

	current, err := officeRegistry.LoadOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	approval := current.Tasks[0].ApprovalRecord
	if approval == nil {
		t.Fatal("dispatch stage has no approval record")
	}
	approveBody, _ := json.Marshal(map[string]any{
		"approval_id": approval.ID, "decision": "approve", "actor": "QA负责人", "opinion": "同意执行",
	})
	approveRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/admin/office/work-orders/"+order.ID+"/tasks/dispatch/approval",
		bytes.NewReader(approveBody),
	)
	approveRequest.Header.Set("Authorization", "Bearer admin-secret")
	approveRecorder := httptest.NewRecorder()
	handler.ServeHTTP(approveRecorder, approveRequest)
	if approveRecorder.Code != http.StatusOK {
		t.Fatalf("approval failed: %d %s", approveRecorder.Code, approveRecorder.Body.String())
	}

	second := submit()
	if second.Code != http.StatusOK {
		t.Fatalf("approved task did not resume: %d %s", second.Code, second.Body.String())
	}
	var resumed agentteams.TaskResult
	if err := json.Unmarshal(second.Body.Bytes(), &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Status != "SUCCESS" || len(resumed.SkillRuns) != 1 ||
		resumed.SkillRuns[0].Name != "dispatch_skill" {
		t.Logf("resumed skill runs: %#v", resumed.SkillRuns)
		t.Logf("resumed error: %q message: %q", resumed.Error, resumed.Message.Content)
		t.Fatalf("approved task did not execute its stage skill: %#v", resumed)
	}
	finalOrder, err := officeRegistry.LoadOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalOrder.Status != "completed" || finalOrder.Tasks[0].Status != "completed" ||
		finalOrder.Tasks[0].ApprovalRecord == nil ||
		finalOrder.Tasks[0].ApprovalRecord.Status != "approved" {
		t.Fatalf("approved stage was not completed durably: %#v", finalOrder)
	}
}
