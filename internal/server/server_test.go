package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ecs/internal/agent"
	"ecs/internal/agentteams"
	"ecs/internal/config"
	"ecs/internal/discovery"
	"ecs/internal/office"
	"ecs/internal/protocol"
	"ecs/internal/roles"
	"ecs/internal/session"
	"ecs/internal/skills"
	"ecs/internal/tools"
)

func TestRequestIdempotency(t *testing.T) {
	server := &Server{requests: make(map[string]*requestEntry)}
	fingerprint := sha256.Sum256([]byte("same request"))
	entry, owner, mismatch := server.beginRequest("request-1", fingerprint)
	if !owner || mismatch {
		t.Fatal("first caller must own request")
	}
	response := protocol.ReplyResponse{RequestID: "request-1"}
	server.finishRequest("request-1", entry, &response)

	cached, owner, mismatch := server.beginRequest("request-1", fingerprint)
	if owner || mismatch {
		t.Fatal("duplicate caller must receive cache")
	}
	<-cached.done
	if cached.response == nil || cached.response.RequestID != "request-1" {
		t.Fatal("cached response missing")
	}
	other := sha256.Sum256([]byte("different request"))
	if _, _, mismatch := server.beginRequest("request-1", other); !mismatch {
		t.Fatal("same request id with different payload must be rejected")
	}
}

func TestRecursiveReplyDelegatesAndHonorsDepth(t *testing.T) {
	var childCalls atomic.Int32
	var child *httptest.Server
	child = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		childCalls.Add(1)
		var request protocol.RecursiveReplyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode child request: %v", err)
		}
		if request.Depth != 1 || !pathContains(request.Path, "http://parent") {
			t.Errorf("child recursion metadata missing: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(protocol.RecursiveReplyResponse{
			RequestID: request.Reply.RequestID,
			Node:      "child",
			URL:       child.URL,
			Depth:     request.Depth,
			Summary:   &protocol.Message{Role: "assistant", Content: "child answer"},
		})
	}))
	defer child.Close()

	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"model answer"}}]}`)
	}))
	defer llm.Close()

	temp := t.TempDir()
	prompt := filepath.Join(temp, "prompt.txt")
	if err := os.WriteFile(prompt, []byte("answer briefly"), 0o600); err != nil {
		t.Fatal(err)
	}
	toolDir := filepath.Join(temp, "tools")
	if err := os.Mkdir(toolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistry(config.ToolsConfig{
		Directory:      toolDir,
		DefaultTimeout: config.Duration(time.Second),
		MaxOutputBytes: 1024,
	}, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	sessionStore, err := session.New(config.SessionsConfig{Directory: filepath.Join(temp, "sessions")})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		NodeName: "parent",
		Agent: config.AgentConfig{
			SystemPromptFile: prompt,
			MaxToolRounds:    1,
			MaxPromptTools:   4,
			ApprovalMode:     "off",
		},
		LLM: config.LLMConfig{
			BaseURL: llm.URL,
			Model:   "test",
			Timeout: config.Duration(2 * time.Second),
		},
		HTTP: config.HTTPConfig{
			MaxBodyBytes:  1 << 20,
			MaxConcurrent: 4,
			PeerTimeout:   config.Duration(2 * time.Second),
			RetryBackoff:  config.Duration(time.Millisecond),
		},
		Sessions: config.SessionsConfig{ContextMessages: 20},
		Trace: config.TraceConfig{
			Enabled: true, Directory: filepath.Join(temp, "traces"), MaxValueChars: 1000,
		},
		Discovery: config.DiscoveryConfig{AdvertiseURL: "http://parent"},
		Nested: config.NestedConfig{
			Enabled:         true,
			DefaultMaxDepth: 8,
			Children:        []config.Peer{{Name: "child", URL: child.URL}},
		},
	}
	runner := agent.New(cfg.Agent, cfg.LLM, registry, logger)
	discoveryManager := discovery.New(
		cfg.Discovery,
		cfg.Nested,
		cfg.NodeName,
		cfg.LLM.Model,
		cfg.AuthToken,
		cfg.Agent.ApprovalMode,
		registry,
		logger,
	)
	handler := New(cfg, runner, registry, sessionStore, discoveryManager, logger)

	call := func(id string, maxDepth int) (int, protocol.RecursiveReplyResponse) {
		request := protocol.RecursiveReplyRequest{
			Reply:    protocol.ReplyRequest{RequestID: id, Prompt: "do work"},
			MaxDepth: &maxDepth,
		}
		body, _ := json.Marshal(request)
		httpRequest := httptest.NewRequest(http.MethodPost, "/v1/recursive/reply", bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httpRequest)
		var response protocol.RecursiveReplyResponse
		_ = json.Unmarshal(recorder.Body.Bytes(), &response)
		return recorder.Code, response
	}

	status, response := call("recursive-1", 0)
	if status != http.StatusOK || len(response.Children) != 1 || response.Summary == nil {
		t.Fatalf("unexpected recursive response (%d): %#v", status, response)
	}
	if childCalls.Load() != 1 {
		t.Fatalf("expected one child call, got %d", childCalls.Load())
	}
	traceData, err := os.ReadFile(filepath.Join(temp, "traces", response.TraceID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"kind": "nested_ecs"`, `"kind": "network_attempt"`, `"kind": "verifier"`} {
		if !strings.Contains(string(traceData), expected) {
			t.Fatalf("recursive trace missing %s: %s", expected, traceData)
		}
	}
	status, response = call("recursive-2", 1)
	if status != http.StatusOK || len(response.Children) != 0 {
		t.Fatalf("depth limit was not honored (%d): %#v", status, response)
	}
	if childCalls.Load() != 1 {
		t.Fatal("child must not be called at max depth 1")
	}
	loopRequest := protocol.RecursiveReplyRequest{
		Reply: protocol.ReplyRequest{RequestID: "recursive-loop", Prompt: "do work"},
		Path:  []string{"http://parent"},
	}
	loopBody, _ := json.Marshal(loopRequest)
	httpRequest := httptest.NewRequest(http.MethodPost, "/v1/recursive/reply", bytes.NewReader(loopBody))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusLoopDetected {
		t.Fatalf("expected loop detection, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestContextWindowKeepsNewestMessages(t *testing.T) {
	messages := []protocol.Message{
		{Content: "one"},
		{Content: "two"},
		{Content: "three"},
	}
	window := contextWindow(messages, 2, 0)
	if len(window) != 2 || window[0].Content != "two" {
		t.Fatalf("unexpected window: %#v", window)
	}
}

func TestContextWindowTrimsByChars(t *testing.T) {
	messages := []protocol.Message{
		{Content: "旧消息很长很长"},
		{Content: "较新的消息"},
		{Content: "最新"},
	}
	// 预算只够最近两条（6+2=8 字符），最旧的应被裁掉
	window := contextWindow(messages, 20, 9)
	if len(window) != 2 || window[0].Content != "较新的消息" || window[1].Content != "最新" {
		t.Fatalf("unexpected char-trimmed window: %#v", window)
	}
	// 即使预算装不下任何一条，也必须保留最新一条
	window = contextWindow(messages, 20, 1)
	if len(window) != 1 || window[0].Content != "最新" {
		t.Fatalf("must keep the newest message: %#v", window)
	}
}

func TestReplyPersistsQueryableTrace(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"trace answer"}}]}`)
	}))
	defer llm.Close()
	temp := t.TempDir()
	prompt := filepath.Join(temp, "prompt.txt")
	if err := os.WriteFile(prompt, []byte("answer"), 0o600); err != nil {
		t.Fatal(err)
	}
	toolsDir := filepath.Join(temp, "tools")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistry(config.ToolsConfig{
		Directory: toolsDir, DefaultTimeout: config.Duration(time.Second), MaxOutputBytes: 1024,
	}, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		NodeName: "trace-node",
		Agent: config.AgentConfig{
			SystemPromptFile: prompt, MaxToolRounds: 1, MaxPromptTools: 2, ApprovalMode: "off",
		},
		LLM: config.LLMConfig{
			BaseURL: llm.URL, Model: "test-model", Timeout: config.Duration(2 * time.Second),
		},
		Trace: config.TraceConfig{
			Enabled: true, Directory: filepath.Join(temp, "traces"), MaxValueChars: 1000,
		},
		HTTP: config.HTTPConfig{
			MaxBodyBytes: 1 << 20, MaxConcurrent: 2, PeerTimeout: config.Duration(time.Second),
		},
		Sessions:  config.SessionsConfig{Directory: filepath.Join(temp, "sessions"), ContextMessages: 20},
		Discovery: config.DiscoveryConfig{AdvertiseURL: "http://trace-node"},
	}
	sessionStore, err := session.New(cfg.Sessions)
	if err != nil {
		t.Fatal(err)
	}
	runner := agent.New(cfg.Agent, cfg.LLM, registry, logger)
	discoveryManager := discovery.New(
		cfg.Discovery, cfg.Nested, cfg.NodeName, cfg.LLM.Model, "", "off", registry, logger,
	)
	handler := New(cfg, runner, registry, sessionStore, discoveryManager, logger)
	body := bytes.NewBufferString(`{"request_id":"trace-request-1","prompt":"hello"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/reply", body)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var reply protocol.ReplyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || reply.TraceID == "" {
		t.Fatalf("unexpected reply: %d %s", recorder.Code, recorder.Body.String())
	}
	if reply.Verification == nil || reply.Verification.Validation == "" {
		t.Fatalf("reply did not expose verification outcome: %#v", reply)
	}
	traceRequest := httptest.NewRequest(http.MethodGet, "/v1/traces/"+reply.TraceID, nil)
	traceRecorder := httptest.NewRecorder()
	handler.ServeHTTP(traceRecorder, traceRequest)
	if traceRecorder.Code != http.StatusOK {
		t.Fatalf("trace not queryable: %d %s", traceRecorder.Code, traceRecorder.Body.String())
	}
	var document map[string]any
	if err := json.Unmarshal(traceRecorder.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	root := document["root"].(map[string]any)
	children := root["children"].([]any)
	planner := children[0].(map[string]any)
	plannerChildren := planner["children"].([]any)
	if planner["kind"] != "planner" || len(plannerChildren) != 2 ||
		plannerChildren[0].(map[string]any)["kind"] != "model" ||
		plannerChildren[1].(map[string]any)["kind"] != "verifier" {
		t.Fatalf("planner/model trace missing: %#v", planner)
	}
}

func TestAgentTeamsTaskUsesSmallModelContextAndPersistsResult(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		for _, expected := range []string{"ecs-verifier", "task-demo-1", "返回准确结果", "信息不足时只问一个问题"} {
			if !strings.Contains(text, expected) {
				t.Errorf("LLM request missing %q: %s", expected, text)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"结果为 16。"}}]}`)
	}))
	defer llm.Close()

	temp := t.TempDir()
	promptPath := filepath.Join(temp, "system.txt")
	guidancePath := filepath.Join(temp, "guidance.txt")
	runtimePath := filepath.Join(temp, "runtime.yaml")
	toolDir := filepath.Join(temp, "tools")
	if err := os.Mkdir(toolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		promptPath:   "准确完成任务。",
		guidancePath: "信息不足时只问一个问题。",
		runtimePath: `
member:
  runtimeName: ecs-verifier
  role: verifier
desired:
  instructions: 只验证当前任务
`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		NodeName: "ecs-test",
		LLM: config.LLMConfig{
			BaseURL: llm.URL,
			Model:   "test",
			Timeout: config.Duration(2 * time.Second),
		},
		Agent: config.AgentConfig{
			SystemPromptFile:          promptPath,
			ConversationGuidanceFile:  guidancePath,
			ConversationGuidance:      true,
			MaxSystemPromptChars:      4000,
			MaxAgentContextChars:      1200,
			MaxConversationGuideChars: 400,
			MaxToolCatalogChars:       800,
			MaxToolRounds:             1,
			MaxPromptTools:            4,
			ApprovalMode:              "off",
		},
		AgentTeams: config.AgentTeamsConfig{
			Enabled:            true,
			WorkerName:         "ecs-worker",
			RuntimeConfigFile:  runtimePath,
			TaskDirectory:      filepath.Join(temp, "shared"),
			MaxTaskPromptChars: 1000,
		},
		Tools: config.ToolsConfig{
			Directory:      toolDir,
			DefaultTimeout: config.Duration(time.Second),
			MaxOutputBytes: 1024,
		},
		Sessions: config.SessionsConfig{
			Directory:       filepath.Join(temp, "sessions"),
			ContextMessages: 20,
		},
		HTTP: config.HTTPConfig{
			MaxBodyBytes:  1 << 20,
			MaxConcurrent: 2,
			PeerTimeout:   config.Duration(time.Second),
		},
		Discovery: config.DiscoveryConfig{AdvertiseURL: "http://ecs-test"},
	}
	registry := tools.NewRegistry(cfg.Tools, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	sessionStore, err := session.New(cfg.Sessions)
	if err != nil {
		t.Fatal(err)
	}
	runner := agent.New(cfg.Agent, cfg.LLM, registry, logger)
	discoveryManager := discovery.New(
		cfg.Discovery,
		cfg.Nested,
		cfg.NodeName,
		cfg.LLM.Model,
		cfg.AuthToken,
		cfg.Agent.ApprovalMode,
		registry,
		logger,
	)
	handler := New(cfg, runner, registry, sessionStore, discoveryManager, logger)
	taskBody := []byte(`{
		"project_id":"project-demo",
		"task_id":"task-demo-1",
		"objective":"计算 9 加 7",
		"acceptance_criteria":["返回准确结果"]
	}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/agentteams/tasks", bytes.NewReader(taskBody))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status":"SUCCESS"`) ||
		!strings.Contains(recorder.Body.String(), "结果为 16") {
		t.Fatalf("unexpected task result: %s", recorder.Body.String())
	}
	resultPath := filepath.Join(temp, "shared", "tasks", "task-demo-1", "result.md")
	data, err := os.ReadFile(resultPath)
	if err != nil || !strings.Contains(string(data), "结果为 16") {
		t.Fatalf("result was not persisted: %v %s", err, data)
	}
}

func TestAgentTeamsTaskAdvancesMappedOfficeStage(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"范围已确认。"}}]}`)
	}))
	defer llm.Close()

	temp := t.TempDir()
	promptPath := filepath.Join(temp, "system.txt")
	toolDir := filepath.Join(temp, "tools")
	componentDir := filepath.Join(temp, "office.d")
	if err := os.WriteFile(promptPath, []byte("准确完成任务。"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(toolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(componentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := office.Manifest{
		SchemaVersion: office.ManifestSchema, Name: "meeting", Version: "1.0.0", Description: "meeting flow",
		Team: []office.AgentIdentity{
			{Name: "planner", Role: "planner", Purpose: "plan"},
			{Name: "clerk", Role: "executor", Purpose: "extract"},
			{Name: "reviewer", Role: "verifier", Purpose: "verify"},
		},
		InputSchema: map[string]any{
			"type": "object", "required": []any{"title"},
			"properties": map[string]any{"title": map[string]any{"type": "string"}},
		},
		Stages: []office.Stage{
			{ID: "scope", Name: "Scope", Agent: "planner", Purpose: "define scope"},
			{ID: "extract", Name: "Extract", Agent: "clerk", Purpose: "extract facts", DependsOn: []string{"scope"}},
			{ID: "verify", Name: "Verify", Agent: "reviewer", Purpose: "verify facts", DependsOn: []string{"extract"}},
		},
	}
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(componentDir, "meeting.json"), manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		NodeName: "ecs-office-worker",
		LLM: config.LLMConfig{
			BaseURL: llm.URL, Model: "test", Timeout: config.Duration(2 * time.Second),
		},
		Agent: config.AgentConfig{
			SystemPromptFile: promptPath, MaxSystemPromptChars: 4000,
			MaxAgentContextChars: 1200, MaxToolCatalogChars: 800,
			MaxToolRounds: 1, MaxPromptTools: 4, ApprovalMode: "off",
		},
		AgentTeams: config.AgentTeamsConfig{
			Enabled: true, WorkerName: "planner",
			TaskDirectory: filepath.Join(temp, "shared"), MaxTaskPromptChars: 1000,
		},
		Office: config.OfficeConfig{
			Directory: componentDir, OrderDirectory: filepath.Join(temp, "orders"),
			MaxStages: 8, MaxContextEntries: 8,
		},
		Tools: config.ToolsConfig{
			Directory: toolDir, DefaultTimeout: config.Duration(time.Second), MaxOutputBytes: 1024,
		},
		Sessions: config.SessionsConfig{
			Directory: filepath.Join(temp, "sessions"), ContextMessages: 20,
		},
		HTTP: config.HTTPConfig{
			MaxBodyBytes: 1 << 20, MaxConcurrent: 2, PeerTimeout: config.Duration(time.Second),
		},
		Discovery: config.DiscoveryConfig{AdvertiseURL: "http://ecs-test"},
	}
	toolRegistry := tools.NewRegistry(cfg.Tools, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	officeRegistry := office.NewRegistry(cfg.Office, toolRegistry, logger)
	if err := officeRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	order, err := officeRegistry.Compile("meeting", office.CompileRequest{
		Input: json.RawMessage(`{"title":"周会"}`), TraceID: "agentteams-trace",
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionStore, err := session.New(cfg.Sessions)
	if err != nil {
		t.Fatal(err)
	}
	runner := agent.New(cfg.Agent, cfg.LLM, toolRegistry, logger)
	discoveryManager := discovery.New(
		cfg.Discovery, cfg.Nested, cfg.NodeName, cfg.LLM.Model,
		cfg.AuthToken, cfg.Agent.ApprovalMode, toolRegistry, logger,
	)
	handler := NewWithOfficeRuntime(
		cfg, runner, toolRegistry, nil, nil, officeRegistry, sessionStore,
		discoveryManager, logger, nil, nil,
	)
	body, _ := json.Marshal(agentteams.TaskRequest{
		ProjectID: "meeting-project", TaskID: "at-scope", Objective: "确认会议输出范围",
		TraceID: "agentteams-trace",
		Office:  &agentteams.OfficeTaskRef{WorkOrderID: order.ID, StageID: "scope"},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/agentteams/tasks", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	var result agentteams.TaskResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Office == nil || result.Office.Status != "completed" ||
		result.Office.OfficeTaskID != order.ID+".scope" {
		t.Fatalf("AgentTeams result is missing Office mapping: %#v", result)
	}
	loaded, err := officeRegistry.LoadOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "running" || loaded.AgentTeamsProjectID != "meeting-project" ||
		loaded.Tasks[0].AgentTeamsTaskID != "at-scope" ||
		loaded.Tasks[0].TraceID != result.ExecutionTraceID ||
		len(loaded.Tasks[0].Attempts) != 1 ||
		loaded.Tasks[0].Attempts[0].Status != office.AttemptSucceeded ||
		len(loaded.Outbox) != 1 ||
		loaded.Outbox[0].Status != office.OutboxConfirmed {
		t.Fatalf("Office stage did not persist AgentTeams state: %#v", loaded)
	}
}

func TestOfficeExecutionRequiresSkillAndConnectorRuns(t *testing.T) {
	root := t.TempDir()
	toolManifest := `{
		"name":"office_connector","description":"connector",
		"capabilities":["office.task.write"],"command":"/usr/bin/true",
		"input_schema":{"type":"object"}
	}`
	if err := os.WriteFile(filepath.Join(root, "connector.json"), []byte(toolManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistry(config.ToolsConfig{Directory: root}, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	server := &Server{registry: registry}
	request := officeTaskExecutionRequest{
		Skills: []string{"office.action.dispatch"}, Connectors: []string{"office.task.write"},
	}
	response := protocol.ReplyResponse{SkillRuns: []protocol.SkillRun{{
		Name: "office.action.dispatch", Status: "success",
		ToolRuns: []protocol.ToolRun{{Name: "office_connector"}},
	}}}
	if err := server.validateOfficeExecution(request, response); err != nil {
		t.Fatalf("valid Stage → Skill → Connector chain was rejected: %v", err)
	}
	response.SkillRuns[0].ToolRuns = nil
	if err := server.validateOfficeExecution(request, response); err == nil ||
		!strings.Contains(err.Error(), "was not executed") {
		t.Fatalf("missing connector execution was not rejected: %v", err)
	}
	response.SkillRuns = nil
	if err := server.validateOfficeExecution(request, response); err == nil ||
		!strings.Contains(err.Error(), "successful SkillRun") {
		t.Fatalf("missing SkillRun was not rejected: %v", err)
	}
}

func TestOfficeTaskExecutesStageSkillThroughConnectorTool(t *testing.T) {
	var modelCalls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if modelCalls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"<tool_call>{\"name\":\"dispatch_skill\",\"arguments\":{\"value\":\"create task\"}}</tool_call>"}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"任务已创建。"}}]}`)
	}))
	defer llm.Close()

	root := t.TempDir()
	toolDir := filepath.Join(root, "tools")
	skillDir := filepath.Join(root, "skills")
	officeDir := filepath.Join(root, "office")
	promptPath := filepath.Join(root, "prompt.txt")
	for _, directory := range []string{toolDir, filepath.Join(skillDir, "dispatch"), officeDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		promptPath: "必须使用指定能力。",
		filepath.Join(toolDir, "connector.json"): `{
			"name":"connector_tool","description":"connector","capabilities":["office.task.write"],
			"command":"/bin/sh","args":["-c","cat"],
			"input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false},
			"output_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}
		}`,
		filepath.Join(skillDir, "dispatch", "skill.json"): `{
			"schema_version":"ecs.skill/v1","name":"dispatch_skill","version":"1.0.0","description":"dispatch",
			"input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false},
			"output_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]},
			"dependencies":{"tools":[{"name":"connector_tool"}]},
			"workflow":[{"id":"dispatch","tool":"connector_tool","input":"$input","on_success":"return"}],
			"prompt_card":{"use":"dispatch an office task"}
		}`,
	}
	for path, value := range files {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		NodeName: "office-worker",
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
	sessionStore, err := session.New(cfg.Sessions)
	if err != nil {
		t.Fatal(err)
	}
	runner := agent.NewWithSkills(cfg.Agent, cfg.LLM, toolRegistry, skillRegistry, logger)
	discoveryManager := discovery.New(
		cfg.Discovery, cfg.Nested, cfg.NodeName, cfg.LLM.Model,
		cfg.AuthToken, cfg.Agent.ApprovalMode, toolRegistry, logger,
	)
	handler := NewWithOfficeRuntime(
		cfg, runner, toolRegistry, skillRegistry, nil, officeRegistry,
		sessionStore, discoveryManager, logger, nil, nil,
	)
	body, _ := json.Marshal(officeTaskExecutionRequest{
		RequestID: "office-stage-1", TraceID: "trace-office-stage-1",
		WorkOrder: "office_1", Component: "meeting", TaskID: "office_1.dispatch",
		TaskName: "Dispatch", Agent: "worker", AgentRole: "executor",
		Prompt: "创建任务", Skills: []string{"dispatch_skill"},
		Tools: []string{"connector_tool"}, Connectors: []string{"office.task.write"},
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/office/tasks/execute", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.ReplyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.SkillRuns) != 1 || response.SkillRuns[0].Name != "dispatch_skill" ||
		len(response.SkillRuns[0].ToolRuns) != 1 ||
		response.SkillRuns[0].ToolRuns[0].Name != "connector_tool" {
		t.Fatalf("Stage → Skill → Connector chain was not recorded: %#v", response)
	}
}

func TestUniquePeersPrefersPersistentNodeIdentity(t *testing.T) {
	peers := uniquePeers([]config.Peer{
		{NodeID: "ecs_0123456789abcdef0123456789abcdef", Name: "configured", URL: "http://old.local"},
		{NodeID: "ecs_0123456789abcdef0123456789abcdef", Name: "discovered", URL: "http://new.local"},
		{Name: "legacy-a", URL: "http://legacy.local/"},
		{Name: "legacy-b", URL: "http://legacy.local"},
	})
	if len(peers) != 2 {
		t.Fatalf("expected one peer per stable identity, got %#v", peers)
	}
	if peers[0].Name != "configured" || peers[1].Name != "legacy-a" {
		t.Fatalf("source precedence was not preserved: %#v", peers)
	}
}

func TestUpsertAdminNodeUsesPersistentIdentity(t *testing.T) {
	nodeID := "ecs_0123456789abcdef0123456789abcdef"
	nodes := []adminNode{{ID: "local", Name: "local"}}
	knownByNodeID := make(map[string]int)

	nodes, replacedID := upsertAdminNode(nodes, knownByNodeID, nodeID, adminNode{
		ID: "discovered:worker", Name: "worker", Source: "discovered",
	})
	if replacedID != "" || len(nodes) != 2 {
		t.Fatalf("unexpected discovery insert: replaced=%q nodes=%#v", replacedID, nodes)
	}

	nodes, replacedID = upsertAdminNode(nodes, knownByNodeID, nodeID, adminNode{
		ID: "network:" + nodeID, Name: "worker", Source: "network",
	})
	if replacedID != "discovered:worker" || len(nodes) != 2 {
		t.Fatalf("persistent identity was not replaced in place: replaced=%q nodes=%#v", replacedID, nodes)
	}
	if nodes[1].Source != "network" {
		t.Fatalf("authenticated node did not win: %#v", nodes[1])
	}
}

func TestOfficeTaskRoutesToStageAgentPrincipal(t *testing.T) {
	var modelCalls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if modelCalls.Add(1) == 1 {
			// Office required-capability protocol: the router pins the stage
			// agent and the model only fills arguments, never a free tool_call.
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"execute\":true,\"arguments\":{\"value\":\"create task\"}}"}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"任务已创建。"}}]}`)
	}))
	defer llm.Close()

	root := t.TempDir()
	toolDir := filepath.Join(root, "tools")
	skillDir := filepath.Join(root, "skills")
	officeDir := filepath.Join(root, "office")
	agentDir := filepath.Join(root, "agents")
	pipelineDir := filepath.Join(root, "pipelines")
	promptPath := filepath.Join(root, "prompt.txt")
	for _, directory := range []string{toolDir, filepath.Join(skillDir, "dispatch"), officeDir, agentDir, pipelineDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		promptPath: "必须使用指定能力。",
		filepath.Join(toolDir, "connector.json"): `{
			"name":"connector_tool","description":"connector","capabilities":["office.task.write"],
			"command":"/bin/sh","args":["-c","cat"],
			"input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false},
			"output_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}
		}`,
		filepath.Join(skillDir, "dispatch", "skill.json"): `{
			"schema_version":"ecs.skill/v1","name":"dispatch_skill","version":"1.0.0","description":"dispatch",
			"input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false},
			"output_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]},
			"dependencies":{"tools":[{"name":"connector_tool"}]},
			"workflow":[{"id":"dispatch","tool":"connector_tool","input":"$input","on_success":"return"}],
			"prompt_card":{"use":"dispatch an office task"}
		}`,
		filepath.Join(agentDir, "worker.json"): `{
			"schema_version":"ecs.agent/v1","name":"worker","purpose":"dispatch approved tasks",
			"execution_mode":"llm",
			"allowed_skills":["dispatch_skill"],"allowed_tools":["connector_tool"],
			"input_schema":{"type":"object"},"output_schema":{"type":"object"},
			"failure_policy":{"max_retries":0,"on_failure":"return_error"}
		}`,
		filepath.Join(agentDir, "reviewer.json"): `{
			"schema_version":"ecs.agent/v1","name":"reviewer","purpose":"review only",
			"execution_mode":"llm",
			"allowed_skills":["meeting.evidence.verify"],"allowed_tools":["meeting_evidence_verify"],
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
		NodeName: "office-worker",
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
	execute := func(agentName string, skills []string, tools []string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(officeTaskExecutionRequest{
			RequestID: "office-routed-" + agentName, TraceID: "trace-routed-" + agentName,
			WorkOrder: "office_1", Component: "meeting", TaskID: "office_1.dispatch",
			TaskName: "Dispatch", Agent: agentName, AgentRole: "executor",
			Prompt: "创建任务", Skills: skills,
			Tools: tools, Connectors: []string{"office.task.write"},
		})
		request := httptest.NewRequest(http.MethodPost, "/v1/office/tasks/execute", bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	allowed := execute("worker", []string{"dispatch_skill"}, []string{"connector_tool"})
	if allowed.Code != http.StatusOK {
		t.Fatalf("stage agent with matching allowlist was rejected: %d %s", allowed.Code, allowed.Body.String())
	}
	var response protocol.ReplyResponse
	if err := json.Unmarshal(allowed.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.SkillRuns) != 1 || response.SkillRuns[0].Name != "dispatch_skill" {
		t.Fatalf("stage agent principal did not execute its skill: %#v", response)
	}
	denied := execute("reviewer", []string{"dispatch_skill"}, []string{"connector_tool"})
	if denied.Code == http.StatusOK {
		t.Fatalf("stage agent outside its allowlist was allowed: %d %s", denied.Code, denied.Body.String())
	}
	if !strings.Contains(denied.Body.String(), "not allowed for agent") {
		t.Fatalf("denial did not cite the agent policy: %s", denied.Body.String())
	}
	unknown := execute("ghost", []string{"dispatch_skill"}, []string{"connector_tool"})
	if unknown.Code == http.StatusOK {
		t.Fatalf("unregistered stage agent was allowed: %d %s", unknown.Code, unknown.Body.String())
	}
}
