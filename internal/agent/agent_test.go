package agent

import (
	"context"
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

	"ecs/internal/authz"
	"ecs/internal/config"
	"ecs/internal/protocol"
	"ecs/internal/roles"
	"ecs/internal/skills"
	"ecs/internal/tools"
	"ecs/internal/trace"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestClientUsesPlatformHTTPTransport(t *testing.T) {
	var calls atomic.Int32
	client := NewClientWithTransport(
		config.LLMConfig{
			BaseURL: "https://platform.invalid/v1",
			Model:   "test-model",
			Timeout: config.Duration(time.Second),
		},
		roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			if request.URL.Host != "platform.invalid" {
				t.Fatalf("unexpected request target: %s", request.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`,
				)),
			}, nil
		}),
	)
	message, err := client.Complete(context.Background(), "", []chatMessage{{
		Role: "user", Content: "ping",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || message.Content != "ok" {
		t.Fatalf("platform transport was not used: calls=%d message=%#v", calls.Load(), message)
	}
}

func TestExecutorPromptContextPreservesOfficeStageRole(t *testing.T) {
	context := executorPromptContext(PromptContext{
		AgentName: "decision_reviewer",
		Role:      "verifier",
	}, roles.Manifest{Name: "executor", Purpose: "execute bounded work"})
	if context.AgentName != "decision_reviewer" || context.Role != "verifier" {
		t.Fatalf("Office stage identity was overwritten by pipeline executor: %#v", context)
	}
	if !strings.Contains(context.Instructions, "Assigned profile role: verifier.") {
		t.Fatalf("assigned role was not reflected in instructions: %#v", context)
	}
}

func TestLiteralProbeUsesMinimalPromptAndZeroTemperature(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload chatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Temperature != 0 || len(payload.Tools) != 0 || len(payload.Messages) != 2 ||
			payload.Messages[1].Content != "READY_QWEN06_WORKER1" ||
			strings.Contains(payload.Messages[0].Content, "READY_QWEN06_WORKER1") {
			t.Fatalf("literal probe was not minimal and deterministic: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"READY_QWEN06_WORKER1"}}]}`)
	}))
	defer llm.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistry(config.ToolsConfig{Directory: t.TempDir()}, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	runner := New(config.AgentConfig{}, config.LLMConfig{
		BaseURL: llm.URL, Model: "test", Timeout: config.Duration(time.Second),
	}, registry, logger)
	response, err := runner.Reply(context.Background(), authz.Trusted(protocol.ReplyRequest{
		Prompt:       "execute literal probe",
		ExpectedText: "READY_QWEN06_WORKER1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "READY_QWEN06_WORKER1" || response.Verification == nil ||
		response.Verification.Status != "accepted" {
		t.Fatalf("literal probe failed: %#v", response)
	}
}

func TestClientAliasesNativeToolNamesAndRestoresToolCalls(t *testing.T) {
	const originalName = "execution.receipt.verify"
	const aliasName = "execution_receipt_verify"
	client := NewClientWithTransport(
		config.LLMConfig{
			BaseURL:     "https://platform.invalid/v1",
			Model:       "test-model",
			NativeTools: true,
			Timeout:     config.Duration(time.Second),
		},
		roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var payload chatRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Tools) != 1 || payload.Tools[0].Function.Name != aliasName {
				t.Fatalf("unsafe native tool name in request: %#v", payload.Tools)
			}
			if got := payload.Messages[0].ToolCalls[0].Function.Name; got != aliasName {
				t.Fatalf("prior tool call was not aliased: %q", got)
			}
			if got := payload.Messages[1].Name; got != aliasName {
				t.Fatalf("prior tool result name was not aliased: %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"execution_receipt_verify","arguments":"{}"}}]}}]}`,
				)),
			}, nil
		}),
	)

	answer, err := client.Complete(context.Background(), "", []chatMessage{
		{
			Role: "assistant",
			ToolCalls: []toolCall{{
				ID:       "prior-call",
				Type:     "function",
				Function: toolFunction{Name: originalName, Arguments: "{}"},
			}},
		},
		{Role: "tool", Name: originalName, ToolCallID: "prior-call", Content: "ok"},
	}, []tools.Manifest{{
		Name:        originalName,
		Description: "Verify execution receipts.",
		InputSchema: map[string]any{"type": "object"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.ToolCalls) != 1 || answer.ToolCalls[0].Function.Name != originalName {
		t.Fatalf("tool call name was not restored: %#v", answer.ToolCalls)
	}
}

func TestNativeToolNameAliasesAvoidCollisions(t *testing.T) {
	aliases, originals := nativeToolNameAliases([]tools.Manifest{
		{Name: "execution.receipt.verify"},
		{Name: "execution_receipt_verify"},
	})
	if aliases["execution_receipt_verify"] != "execution_receipt_verify" {
		t.Fatalf("safe original name should remain unchanged: %#v", aliases)
	}
	dottedAlias := aliases["execution.receipt.verify"]
	if dottedAlias == "execution_receipt_verify" || !isSafeNativeToolName(dottedAlias) {
		t.Fatalf("colliding dotted name did not receive a safe unique alias: %#v", aliases)
	}
	if originals[dottedAlias] != "execution.receipt.verify" {
		t.Fatalf("alias cannot be mapped back to its original: %#v", originals)
	}
}

func TestParseTextToolCall(t *testing.T) {
	for _, input := range []string{
		`  <tool_call>{"name":"clock","arguments":{}}</tool_call>  `,
		`  <tool_call>{"name":"clock","arguments":{}}  `,
	} {
		call, ok := parseTextToolCall(input)
		if !ok {
			t.Fatalf("expected tool call for %q", input)
		}
		if call.Function.Name != "clock" || call.Function.Arguments != "{}" {
			t.Fatalf("unexpected call: %#v", call)
		}
	}
}

func TestParseTextToolCallRejectsInvalid(t *testing.T) {
	tests := []string{
		`hello`,
		`<tool_call>not-json</tool_call>`,
		`<tool_call>{"name":"clock","arguments":{}} trailing prose`,
		`<tool_call>{"name":"","arguments":{}}</tool_call>`,
	}
	for _, input := range tests {
		if _, ok := parseTextToolCall(input); ok {
			t.Fatalf("expected rejection for %q", input)
		}
	}
}

func TestResolvedToolCallsCountsTextProtocol(t *testing.T) {
	calls, textProtocol := resolvedToolCalls(chatMessage{
		Content: `<tool_call>{"name":"clock","arguments":{}}</tool_call>`,
	})
	if len(calls) != 1 || !textProtocol || calls[0].Function.Name != "clock" {
		t.Fatalf("text protocol call was not resolved: %#v, text=%v", calls, textProtocol)
	}
	native := toolCall{Function: toolFunction{Name: "calculate", Arguments: `{}`}}
	calls, textProtocol = resolvedToolCalls(chatMessage{
		Content:   `<tool_call>{"name":"clock","arguments":{}}</tool_call>`,
		ToolCalls: []toolCall{native},
	})
	if len(calls) != 1 || textProtocol || calls[0].Function.Name != "calculate" {
		t.Fatalf("native tool call did not take precedence: %#v, text=%v", calls, textProtocol)
	}
}

func TestMissingRequiredCalls(t *testing.T) {
	required := []string{"capabilities", "clock"}
	runs := []protocol.SkillRun{{Name: "capabilities", Status: "error"}}
	missing := missingRequiredSkillCalls(required, runs)
	if len(missing) != 1 || missing[0] != "clock" {
		t.Fatalf("unexpected missing required calls: %#v", missing)
	}
	toolRuns := []protocol.ToolRun{{Name: "capabilities", Output: `{}`}}
	missingTools := missingRequiredToolCalls(required, toolRuns)
	if len(missingTools) != 1 || missingTools[0] != "clock" {
		t.Fatalf("unexpected missing required tool calls: %#v", missingTools)
	}
	plan := roles.Plan{Tasks: []roles.Task{{RequiredSkills: required, RequiredTools: required}}}
	if got := planRequiredSkills(plan); len(got) != 2 {
		t.Fatalf("unexpected required skills: %#v", got)
	}
	if got := planRequiredTools(plan); len(got) != 2 {
		t.Fatalf("unexpected required tools: %#v", got)
	}
	if got := formatRequiredCalls([]string{"clock"}, []string{"text_stats"}); got != "Skill clock; Tool text_stats" {
		t.Fatalf("unexpected required call description: %q", got)
	}
}

func TestNormalizeCapabilitiesResponseListsEverySkill(t *testing.T) {
	plan := roles.Plan{Tasks: []roles.Task{{RequiredSkills: []string{"capabilities"}}}}
	response := normalizeRequiredSkillResponse(plan, protocol.ReplyResponse{
		Message: protocol.Message{Role: "assistant", Content: "有两个能力。"},
		SkillRuns: []protocol.SkillRun{{
			Name:   "capabilities",
			Status: "success",
			Output: `{"tool_count":2,"skill_count":2,"tools":[{"name":"calculate"},{"name":"clock"}],"skills":[{"name":"calculate"},{"name":"clock"}]}`,
		}},
	}, []string{"calculate"}, []string{"clock"})
	for _, expected := range []string{
		"已注册 Tool（2）",
		"当前 Pipeline 可调用 Tool（1）",
		"已注册 Skill（2）",
		"当前 Pipeline 可调用 Skill（1）",
		"calculate",
		"clock",
	} {
		if !strings.Contains(response.Message.Content, expected) {
			t.Fatalf("normalized capability response is incomplete: %q", response.Message.Content)
		}
	}
}

func TestClientAPIKeyCanBeConfiguredInMemory(t *testing.T) {
	client := NewClient(config.LLMConfig{BaseURL: "https://example.invalid", Model: "test"})
	if client.Ready() {
		t.Fatal("client without an API key must not report ready")
	}
	client.SetAPIKey("runtime-secret")
	if !client.Ready() {
		t.Fatal("client must report ready after receiving an in-memory API key")
	}
}

func TestManifestQuestionsAreAnsweredWithoutLLMGuessing(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools")
	skillsDir := filepath.Join(root, "skills")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "office.action.dispatch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "office_action_compensate.json"), []byte(`{
		"name":"office_action_compensate","version":"1.0.0",
		"description":"撤销已执行的办公动作","command":"/usr/bin/true","risk":"high",
		"input_schema":{"type":"object"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(skillsDir, "office.action.dispatch", "skill.json"),
		[]byte(`{
			"schema_version":"ecs.skill/v1","name":"office.action.dispatch","version":"1.0.0",
			"description":"创建会后行动并生成回执","when":{},
			"input_schema":{"type":"object"},
			"dependencies":{"tools":[{"name":"office_action_compensate"}]},
			"workflow":[{"id":"dispatch","tool":"office_action_compensate","input":"$input"}],
			"prompt_card":{"use":"行动项已批准时使用"}
		}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistry(config.ToolsConfig{
		Directory: toolsDir, DefaultTimeout: config.Duration(time.Second),
	}, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	skillRegistry := skills.NewRegistry(config.SkillsConfig{
		Directory: skillsDir, MaxPromptSkills: 4, MaxSteps: 4,
	}, toolRegistry, logger)
	if err := skillRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	runner := &Agent{registry: toolRegistry, skills: skillRegistry}
	toolResponse, ok := runner.manifestExplanation(
		protocol.ReplyRequest{Prompt: "office_action_compensate 有什么用"},
		[]string{"office_action_compensate"},
		nil,
	)
	if !ok ||
		!strings.Contains(toolResponse.Message.Content, "撤销已执行的办公动作") ||
		!strings.Contains(toolResponse.Message.Content, "当前 Pipeline 可调用") {
		t.Fatalf("tool manifest was not answered deterministically: %#v", toolResponse)
	}
	skillResponse, ok := runner.manifestExplanation(
		protocol.ReplyRequest{Prompt: "office.action.dispatch 这个 Skill 有什么用"},
		nil,
		[]string{"office.action.dispatch"},
	)
	if !ok ||
		!strings.Contains(skillResponse.Message.Content, "创建会后行动并生成回执") ||
		!strings.Contains(skillResponse.Message.Content, "行动项已批准时使用") {
		t.Fatalf("skill manifest was not answered deterministically: %#v", skillResponse)
	}
	listResponse, ok := runner.manifestExplanation(
		protocol.ReplyRequest{Prompt: "请列出本节点当前已注册和可调用的 Tool 与 Skill。"},
		[]string{"office_action_compensate"},
		nil,
	)
	if !ok ||
		!strings.Contains(listResponse.Message.Content, "Tool（已注册 1，当前可调用 1）") ||
		!strings.Contains(listResponse.Message.Content, "Skill（已注册 1，当前可调用 0）") ||
		!strings.Contains(listResponse.Message.Content, "当前 Pipeline 不可调用") {
		t.Fatalf("manifest list was not answered deterministically: %#v", listResponse)
	}
	_, ok = runner.manifestExplanation(
		protocol.ReplyRequest{
			Prompt:         "必须调用 office.action.dispatch；可用连接器 office.task.write。",
			Skills:         []string{"office.action.dispatch"},
			RestrictSkills: true,
			RestrictTools:  true,
		},
		nil,
		[]string{"office.action.dispatch"},
	)
	if ok {
		t.Fatal("constrained Office execution was intercepted as a Manifest question")
	}
}

func TestSmallModelPromptIsLayeredAndBounded(t *testing.T) {
	cfg := config.AgentConfig{
		MaxSystemPromptChars:      900,
		MaxAgentContextChars:      260,
		MaxConversationGuideChars: 120,
		MaxToolCatalogChars:       220,
	}
	contextValue := PromptContext{
		AgentName:    "edge-worker",
		Role:         "只负责校验结果",
		TaskID:       "task-1",
		Objective:    strings.Repeat("检查输入。", 100),
		Instructions: "发现错误时明确说明。",
		AcceptanceCriteria: []string{
			"结果包含结论",
			"不要执行额外任务",
		},
	}
	prompt := buildSystemPrompt(
		cfg,
		"基础规则：必要时调用工具。",
		"信息不足时只问一个问题。",
		"已授权 Skill 参考资料：不要猜测缺失值。",
		contextValue,
		[]tools.Manifest{{
			Name:        "clock",
			Description: "获取当前时间",
			InputSchema: map[string]any{"type": "object"},
		}},
	)
	if len([]rune(prompt)) > cfg.MaxSystemPromptChars {
		t.Fatalf("prompt exceeds budget: %d", len([]rune(prompt)))
	}
	for _, expected := range []string{
		"基础规则",
		"edge-worker",
		"task-1",
		"验收",
		"信息不足",
		"不要猜测",
		"clock",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt does not contain %q:\n%s", expected, prompt)
		}
	}
}

func TestSmallModelBasePromptKeepsProtocolGuardrailsWithinBudget(t *testing.T) {
	path := filepath.Join("..", "..", "prompts", "system.small.zh.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(data)
	if len([]rune(prompt)) > 2000 {
		t.Fatalf("small-model base prompt exceeds its default 2000-character budget: %d", len([]rune(prompt)))
	}
	for _, expected := range []string{
		"原样输出",
		"不判断文字真假",
		`<tool_call>{"name":"工具名","arguments":{…}}</tool_call>`,
		"绝对禁止裸 JSON",
		"capabilities 只管 Tool/Skill，不管模型或节点运行状态",
		"无法从当前信息验证",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("small-model base prompt is missing guardrail %q", expected)
		}
	}
	if strings.Index(prompt, "1. 原样输出") > strings.Index(prompt, "3. 工具调用") {
		t.Fatal("literal output must take priority over tool routing")
	}
}

func TestApprovalPolicy(t *testing.T) {
	runner := &Agent{cfg: config.AgentConfig{ApprovalMode: "high_risk"}}
	if !runner.requiresApproval(tools.Manifest{Risk: "high"}) {
		t.Fatal("high risk tool must require approval")
	}
	if runner.requiresApproval(tools.Manifest{Risk: "low"}) {
		t.Fatal("low risk tool should not require approval")
	}
	if !runner.requiresApproval(tools.Manifest{Risk: "low", RequiresApproval: true}) {
		t.Fatal("manifest must be able to require approval")
	}
	runner.cfg.ApprovalMode = "off"
	if runner.requiresApproval(tools.Manifest{Risk: "high", RequiresApproval: true}) {
		t.Fatal("off mode must disable approvals")
	}
}

func TestTextToolLoopEndToEnd(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode LLM request: %v", err)
		}
		if _, ok := request["reasoning_effort"]; ok {
			t.Errorf("disabled reasoning effort must be omitted: %#v", request["reasoning_effort"])
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"<tool_call>{\"name\":\"clock\",\"arguments\":{}}</tool_call>"}}]}`)
			return
		}
		messages, _ := request["messages"].([]any)
		encoded, _ := json.Marshal(messages)
		if !strings.Contains(string(encoded), "2026-07-24T10:00:00") {
			t.Errorf("second request does not contain tool output: %s", encoded)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"现在是 10:00。"}}]}`)
	}))
	defer llm.Close()

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	toolsDir := filepath.Join(dir, "tools.d")
	if err := os.Mkdir(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte("使用工具。"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"clock",
		"description":"读取时间",
		"command":"printf",
		"args":["2026-07-24T10:00:00"],
		"input_schema":{"type":"object","properties":{},"additionalProperties":false}
	}`
	if err := os.WriteFile(filepath.Join(toolsDir, "clock.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistry(config.ToolsConfig{
		Directory:      toolsDir,
		DefaultTimeout: config.Duration(2 * time.Second),
		MaxOutputBytes: 1024,
	}, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	runner := New(config.AgentConfig{
		SystemPromptFile: promptPath,
		MaxToolRounds:    2,
		MaxPromptTools:   4,
	}, config.LLMConfig{
		BaseURL:         llm.URL,
		Model:           "test",
		Timeout:         config.Duration(2 * time.Second),
		ReasoningEffort: "none",
	}, registry, logger)

	response, err := runner.Reply(context.Background(), authz.Trusted(protocol.ReplyRequest{
		Prompt: "几点？",
		Tools:  []string{"clock"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "现在是 10:00。" || len(response.ToolRuns) != 1 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestInvalidEnumGetsOneRequiredCorrection(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode LLM request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch calls.Add(1) {
		case 1:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"<tool_call>{\"name\":\"calculate\",\"arguments\":{\"a\":1024,\"op\":\"log2\",\"b\":2}}</tool_call>"}}]}`)
		case 2:
			encoded, _ := json.Marshal(request["messages"])
			if !strings.Contains(string(encoded), "只有一次修正机会") ||
				!strings.Contains(string(encoded), "schema") {
				t.Errorf("correction instruction missing from second request: %s", encoded)
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"<tool_call>{\"name\":\"calculate\",\"arguments\":{\"a\":1024,\"op\":\"log_base\",\"b\":2}}</tool_call>"}}]}`)
		default:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"1024 是 2 的 10 次方。"}}]}`)
		}
	}))
	defer llm.Close()

	temp := t.TempDir()
	promptPath := filepath.Join(temp, "prompt.txt")
	toolsDir := filepath.Join(temp, "tools")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte("严格使用 schema。"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"calculate","description":"calculate","command":"/usr/bin/printf","args":["{\"result\":10}"],
		"input_schema":{
			"type":"object",
			"properties":{
				"a":{"type":"number"},
				"op":{"type":"string","enum":["power","log_base"]},
				"b":{"type":"number"}
			},
			"required":["a","op","b"],
			"additionalProperties":false
		},
		"output_schema":{"type":"object","properties":{"result":{"type":"number"}},"required":["result"]}
	}`
	if err := os.WriteFile(filepath.Join(toolsDir, "calculate.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistry(config.ToolsConfig{
		Directory: toolsDir, DefaultTimeout: config.Duration(time.Second), MaxOutputBytes: 1024,
	}, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	runner := New(config.AgentConfig{
		SystemPromptFile: promptPath, MaxToolRounds: 2, MaxPromptTools: 2,
	}, config.LLMConfig{
		BaseURL: llm.URL, Model: "test", Timeout: config.Duration(2 * time.Second),
	}, registry, logger)

	response, err := runner.Reply(context.Background(), authz.Trusted(protocol.ReplyRequest{
		Prompt: "1024 是 2 的几次方？", Tools: []string{"calculate"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "1024 是 2 的 10 次方。" || len(response.ToolRuns) != 2 {
		t.Fatalf("unexpected corrected response: %#v", response)
	}
	if response.ToolRuns[0].Error == "" ||
		!strings.Contains(string(response.ToolRuns[0].Arguments), `"log2"`) {
		t.Fatalf("invalid first run was not preserved: %#v", response.ToolRuns[0])
	}
	if response.ToolRuns[1].Error != "" ||
		!strings.Contains(string(response.ToolRuns[1].Arguments), `"log_base"`) {
		t.Fatalf("corrected run did not succeed: %#v", response.ToolRuns[1])
	}
}

func TestInvalidEnumCannotBeBypassedWithDirectAnswer(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"<tool_call>{\"name\":\"calculate\",\"arguments\":{\"op\":\"log2\"}}</tool_call>"}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"答案碰巧是 10。"}}]}`)
	}))
	defer llm.Close()

	temp := t.TempDir()
	promptPath := filepath.Join(temp, "prompt.txt")
	toolsDir := filepath.Join(temp, "tools")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte("使用工具。"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"calculate","description":"calculate","command":"/usr/bin/true",
		"input_schema":{"type":"object","properties":{"op":{"type":"string","enum":["log_base"]}},"required":["op"],"additionalProperties":false}
	}`
	if err := os.WriteFile(filepath.Join(toolsDir, "calculate.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistry(config.ToolsConfig{
		Directory: toolsDir, DefaultTimeout: config.Duration(time.Second), MaxOutputBytes: 1024,
	}, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	runner := New(config.AgentConfig{
		SystemPromptFile: promptPath, MaxToolRounds: 2, MaxPromptTools: 2,
	}, config.LLMConfig{
		BaseURL: llm.URL, Model: "test", Timeout: config.Duration(2 * time.Second),
	}, registry, logger)

	response, err := runner.Reply(context.Background(), authz.Trusted(protocol.ReplyRequest{
		Prompt: "测试", Tools: []string{"calculate"},
	}))
	if err == nil || !strings.Contains(err.Error(), "did not retry") {
		t.Fatalf("direct answer should be rejected, response=%#v error=%v", response, err)
	}
	if len(response.ToolRuns) != 1 || response.ToolRuns[0].Error == "" {
		t.Fatalf("failed run should be returned: %#v", response)
	}
}

func TestMissingRequiredToolArgumentPausesForInput(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"<tool_call>{\"name\":\"meeting_create\",\"arguments\":{\"title\":\"季度复盘\"}}</tool_call>"}}]}`)
	}))
	defer llm.Close()

	temp := t.TempDir()
	promptPath := filepath.Join(temp, "prompt.txt")
	toolsDir := filepath.Join(temp, "tools")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte("使用工具。"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"meeting_create","description":"create meeting","command":"/usr/bin/true",
		"input_schema":{
			"type":"object",
			"properties":{"title":{"type":"string"},"start_time":{"type":"string"}},
			"required":["title","start_time"],"additionalProperties":false
		}
	}`
	if err := os.WriteFile(filepath.Join(toolsDir, "meeting_create.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := tools.NewRegistry(config.ToolsConfig{
		Directory: toolsDir, DefaultTimeout: config.Duration(time.Second), MaxOutputBytes: 1024,
	}, logger)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	runner := New(config.AgentConfig{
		SystemPromptFile: promptPath, MaxToolRounds: 2, MaxPromptTools: 2,
	}, config.LLMConfig{
		BaseURL: llm.URL, Model: "test", Timeout: config.Duration(2 * time.Second),
	}, registry, logger)

	response, err := runner.Reply(context.Background(), authz.Trusted(protocol.ReplyRequest{
		Prompt: "创建季度复盘会议", Tools: []string{"meeting_create"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.InputRequest == nil || len(response.InputRequest.MissingFields) != 1 ||
		response.InputRequest.MissingFields[0] != "start_time" || len(response.ToolRuns) != 0 {
		t.Fatalf("execution did not pause for the missing slot: %#v", response)
	}
	if response.Verification == nil || response.Verification.Status != "needs_input" {
		t.Fatalf("slot gate verification is missing: %#v", response.Verification)
	}
}

func TestSkillLoopRunsDeterministicWorkflow(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"<tool_call>{\"name\":\"echo\",\"arguments\":{\"value\":\"hello\"}}</tool_call>"}}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`)
	}))
	defer llm.Close()

	temp := t.TempDir()
	promptPath := filepath.Join(temp, "prompt.txt")
	toolsDir := filepath.Join(temp, "tools")
	skillsDir := filepath.Join(temp, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "echo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		promptPath: "使用能力。",
		filepath.Join(toolsDir, "echo.json"): `{
			"name":"echo","description":"echo","command":"/bin/sh","args":["-c","cat"],
			"input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}
		}`,
		filepath.Join(skillsDir, "echo", "skill.json"): `{
			"schema_version":"ecs.skill/v1","name":"echo","version":"1.0.0","description":"echo skill",
			"when":{"keywords":["echo"]},
			"input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false},
			"output_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]},
			"dependencies":{"tools":[{"name":"echo"}]},
			"workflow":[{"id":"echo","tool":"echo","input":"$input","on_failure":"fail"}],
			"risk":{"level":"low"},"prompt_card":{"use":"echo a supplied value"}
		}`,
	}
	for path, data := range files {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistry(config.ToolsConfig{
		Directory: toolsDir, DefaultTimeout: config.Duration(time.Second), MaxOutputBytes: 1024,
	}, logger)
	if err := toolRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	skillRegistry := skills.NewRegistry(config.SkillsConfig{
		Directory: skillsDir, MaxPromptSkills: 3, MaxSteps: 3,
	}, toolRegistry, logger)
	if err := skillRegistry.Reload(); err != nil {
		t.Fatal(err)
	}
	runner := NewWithSkills(config.AgentConfig{
		SystemPromptFile: promptPath, MaxToolRounds: 2, MaxPromptTools: 3,
	}, config.LLMConfig{
		BaseURL: llm.URL, Model: "test", Timeout: config.Duration(2 * time.Second),
	}, toolRegistry, skillRegistry, logger)
	response, err := runner.Reply(context.Background(), authz.Trusted(protocol.ReplyRequest{
		Prompt: "echo hello", Skills: []string{"echo"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "hello" || len(response.SkillRuns) != 1 ||
		response.SkillRuns[0].Status != "success" || len(response.ToolRuns) != 1 {
		t.Fatalf("unexpected skill response: %#v", response)
	}
}

func TestGroupSynthesisUsesCompactPeerResults(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "节点甲回答") {
			t.Errorf("peer reply missing from synthesis request: %s", body)
		}
		if strings.Contains(string(body), "large internal tool output") {
			t.Errorf("tool run details should not enter synthesis context: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"汇总结果"}}]}`)
	}))
	defer llm.Close()
	runner := New(config.AgentConfig{}, config.LLMConfig{
		BaseURL: llm.URL,
		Model:   "test",
		Timeout: config.Duration(2 * time.Second),
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	summary, err := runner.Summarize(context.Background(), authz.Trusted(protocol.ReplyRequest{Prompt: "原任务"}), []protocol.PeerResult{
		{
			Peer: "node-a",
			Response: &protocol.ReplyResponse{
				Message:  protocol.Message{Role: "assistant", Content: "节点甲回答"},
				ToolRuns: []protocol.ToolRun{{Output: "large internal tool output"}},
			},
		},
		{Peer: "node-b", Error: "offline"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Content != "汇总结果" {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}

func TestGroupSynthesisRetriesRawToolCallThenFallsBackDeterministically(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"<tool_call>{\"name\":\"calculate\",\"arguments\":{}}</tool_call>"}}]}`)
	}))
	defer llm.Close()
	runner := New(config.AgentConfig{}, config.LLMConfig{
		BaseURL: llm.URL,
		Model:   "test",
		Timeout: config.Duration(2 * time.Second),
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	summary, err := runner.Summarize(
		context.Background(),
		authz.Trusted(protocol.ReplyRequest{Prompt: "计算"}),
		[]protocol.PeerResult{{
			Peer: "node-a",
			Response: &protocol.ReplyResponse{
				Message: protocol.Message{Role: "assistant", Content: "节点结果是 8。"},
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected one correction retry, got %d calls", calls.Load())
	}
	if summary.Content != "节点结果是 8。" || strings.Contains(summary.Content, "<tool_call") {
		t.Fatalf("unsafe synthesis escaped deterministic fallback: %#v", summary)
	}
}

func TestGroupSynthesisTraceMarksInvalidLLMOutputDegraded(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"<tool_call>{}</tool_call>"}}]}`)
	}))
	defer llm.Close()
	runner := New(config.AgentConfig{}, config.LLMConfig{
		BaseURL: llm.URL,
		Model:   "test",
		Timeout: config.Duration(2 * time.Second),
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	store, err := trace.NewStore(config.TraceConfig{
		Enabled: true, Directory: t.TempDir(), MaxValueChars: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := store.Start("synthesis-trace", "request-1", "node-a", "/test", nil)
	ctx := trace.WithRecorder(context.Background(), recorder, recorder.RootID())
	if _, err := runner.Summarize(
		ctx,
		authz.Trusted(protocol.ReplyRequest{Prompt: "test"}),
		[]protocol.PeerResult{{Peer: "node-b", Error: "<tool_call>{}</tool_call>"}},
	); err != nil {
		t.Fatal(err)
	}
	recorder.Finish("success", "fallback delivered", nil)
	stored, err := store.Load("synthesis-trace")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Root.Children) != 1 || stored.Root.Children[0].Status != "degraded" {
		t.Fatalf("invalid synthesis was not traced as degraded: %#v", stored.Root.Children)
	}
	if strings.Contains(stored.Root.Children[0].Output.(string), "<tool_call") {
		t.Fatalf("raw tool protocol leaked through deterministic diagnostic: %#v", stored.Root.Children[0].Output)
	}
}

func TestGroupSynthesisAcceptsCorrectedRetry(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		content := `<tool_call>{"name":"clock","arguments":{}}</tool_call>`
		if call == 2 {
			content = "纠正后的自然语言结果。"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]string{"role": "assistant", "content": content},
			}},
		})
	}))
	defer llm.Close()
	runner := New(config.AgentConfig{}, config.LLMConfig{
		BaseURL: llm.URL,
		Model:   "test",
		Timeout: config.Duration(2 * time.Second),
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	summary, err := runner.Summarize(
		context.Background(),
		authz.Trusted(protocol.ReplyRequest{Prompt: "几点"}),
		[]protocol.PeerResult{{
			Peer: "node-a",
			Response: &protocol.ReplyResponse{
				Message: protocol.Message{Role: "assistant", Content: "10:00"},
			},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Content != "纠正后的自然语言结果。" || calls.Load() != 2 {
		t.Fatalf("retry was not accepted: calls=%d summary=%#v", calls.Load(), summary)
	}
}
