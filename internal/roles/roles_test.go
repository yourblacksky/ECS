package roles

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ecs/internal/config"
	"ecs/internal/protocol"
)

func TestRegistryRoutesSingleExecutorTaskAndVerifies(t *testing.T) {
	registry := testRegistry(t)
	request, plan, err := registry.Route(protocol.ReplyRequest{
		Prompt: "calculate 2+2",
		Skills: []string{"calculate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Pipeline != "default" || plan.Strategy != "ecs-capability-single-task" || len(plan.Tasks) != 1 ||
		plan.Tasks[0].Agent != "executor" || plan.Verifier != "verifier" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if request.Model != "test-model" || len(request.Skills) != 1 || request.Skills[0] != "calculate" ||
		len(request.Tools) != 1 || request.Tools[0] != "calculate" {
		t.Fatalf("executor policy was not applied: %#v", request)
	}
	verification := registry.Verify(plan, protocol.ReplyResponse{
		Message: protocol.Message{Role: "assistant", Content: "4"},
		SkillRuns: []protocol.SkillRun{{
			Name: "calculate", Status: "success",
		}},
		ToolRuns: []protocol.ToolRun{{Name: "calculate", Output: `{"result":4}`}},
	})
	if verification.Status != "accepted" || len(verification.Evidence) < 2 {
		t.Fatalf("valid result rejected: %#v", verification)
	}
}

func TestRegistryRequiresCapabilitiesSkillForCapabilityQuestions(t *testing.T) {
	registry := testRegistry(t)
	request, plan, err := registry.Route(protocol.ReplyRequest{
		Prompt: "我现在有什么工具和 Skill 可以用？",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Skills) != 1 || request.Skills[0] != "capabilities" ||
		len(request.Tools) != 1 || request.Tools[0] != "capabilities" {
		t.Fatalf("capability query was not constrained to capabilities: %#v", request)
	}
	if len(plan.Tasks) != 1 || len(plan.Tasks[0].RequiredSkills) != 1 ||
		plan.Tasks[0].RequiredSkills[0] != "capabilities" {
		t.Fatalf("capabilities was not marked required: %#v", plan)
	}
	rejected := registry.Verify(plan, protocol.ReplyResponse{
		Message: protocol.Message{Role: "assistant", Content: "我有一些工具。"},
	})
	if rejected.Status != "rejected" {
		t.Fatalf("answer without required skill was accepted: %#v", rejected)
	}
	accepted := registry.Verify(plan, protocol.ReplyResponse{
		Message: protocol.Message{Role: "assistant", Content: "10 个 Tool，10 个 Skill。"},
		SkillRuns: []protocol.SkillRun{{
			Name: "capabilities", Status: "success",
		}},
		ToolRuns: []protocol.ToolRun{{
			Name: "capabilities", Output: `{"tool_count":10,"skill_count":10}`,
		}},
	})
	if accepted.Status != "accepted" {
		t.Fatalf("answer with required skill was rejected: %#v", accepted)
	}
}

func TestRegistryRequiresEveryExplicitManifestCapability(t *testing.T) {
	registry := testRegistry(t)
	tests := []struct {
		prompt         string
		requiredSkills []string
		requiredTools  []string
	}{
		{
			prompt:         `text_stats ： “一段文字”`,
			requiredSkills: []string{"text_stats"},
			requiredTools:  []string{"text_stats"},
		},
		{
			prompt:        `office_action_compensate: 撤销动作`,
			requiredTools: []string{"office_action_compensate"},
		},
	}
	for _, test := range tests {
		request, plan, err := registry.Route(protocol.ReplyRequest{Prompt: test.prompt})
		if err != nil {
			t.Fatalf("route %q: %v", test.prompt, err)
		}
		task := plan.Tasks[0]
		if strings.Join(task.RequiredSkills, ",") != strings.Join(test.requiredSkills, ",") ||
			strings.Join(task.RequiredTools, ",") != strings.Join(test.requiredTools, ",") {
			t.Fatalf("explicit capability was not required: prompt=%q plan=%#v", test.prompt, plan)
		}
		if strings.Join(request.Skills, ",") != strings.Join(test.requiredSkills, ",") ||
			strings.Join(request.Tools, ",") != strings.Join(test.requiredTools, ",") {
			t.Fatalf("explicit capability did not reduce candidates: prompt=%q request=%#v", test.prompt, request)
		}
		rejected := registry.VerifyWithRequest(plan, request, protocol.ReplyResponse{
			Message: protocol.Message{Role: "assistant", Content: `{"result":"guessed"}`},
		})
		if rejected.Status != "rejected" {
			t.Fatalf("fabricated result for %q was accepted: %#v", test.prompt, rejected)
		}
	}
}

func TestRegistryDetectsExplicitCapabilityInLatestUserMessage(t *testing.T) {
	registry := testRegistry(t)
	request, plan, err := registry.Route(protocol.ReplyRequest{Messages: []protocol.Message{
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好。"},
		{Role: "user", Content: "calculate: 2+2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(request.Skills, ",") != "calculate" ||
		strings.Join(request.Tools, ",") != "calculate" ||
		strings.Join(plan.Tasks[0].RequiredSkills, ",") != "calculate" ||
		strings.Join(plan.Tasks[0].RequiredTools, ",") != "calculate" {
		t.Fatalf("latest user capability was not routed deterministically: request=%#v plan=%#v", request, plan)
	}
}

func TestRegistryPreservesExplicitEmptyNetworkCapabilityPolicy(t *testing.T) {
	registry := testRegistry(t)
	request, plan, err := registry.Route(protocol.ReplyRequest{
		Prompt:         "只做普通问答",
		RestrictSkills: true,
		RestrictTools:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Skills) != 0 || len(request.Tools) != 0 ||
		len(plan.Tasks) != 1 ||
		len(plan.Tasks[0].AllowedSkills) != 0 ||
		len(plan.Tasks[0].AllowedTools) != 0 {
		t.Fatalf("empty relationship capability policy expanded to local capabilities: request=%#v plan=%#v", request, plan)
	}
}

func TestRegistryPreservesTrustedRequiredCapabilitiesInRestrictedRequest(t *testing.T) {
	registry := testRegistry(t)
	request, plan, err := registry.Route(protocol.ReplyRequest{
		Prompt:         "执行会议事实提取",
		Skills:         []string{"calculate"},
		Tools:          []string{"calculate"},
		RequiredSkills: []string{"calculate"},
		RestrictSkills: true,
		RestrictTools:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Tasks[0].RequiredSkills, ",") != "calculate" {
		t.Fatalf("trusted required skill was not preserved: request=%#v plan=%#v", request, plan)
	}
	if len(plan.Tasks[0].RequiredTools) != 0 {
		t.Fatalf("allowed dependency tools must not become mandatory: %#v", plan)
	}
}

func TestRegistryRejectsRequiredCapabilityOutsideRestrictedPolicy(t *testing.T) {
	registry := testRegistry(t)
	if _, _, err := registry.Route(protocol.ReplyRequest{
		Prompt:         "执行会议事实提取",
		Skills:         []string{"calculate"},
		Tools:          []string{"calculate"},
		RequiredSkills: []string{"capabilities"},
		RestrictSkills: true,
		RestrictTools:  true,
	}); err == nil {
		t.Fatal("required capability outside the restricted policy was accepted")
	}
}

func TestLLMRoleMayInheritNodeModel(t *testing.T) {
	err := validate(Manifest{
		SchemaVersion: "ecs.agent/v1",
		Name:          "inherited",
		Purpose:       "inherit the node model",
		ExecutionMode: "llm",
		InputSchema:   map[string]any{"type": "object"},
		OutputSchema:  map[string]any{"type": "object"},
		FailurePolicy: FailurePolicy{OnFailure: "fail"},
	})
	if err != nil {
		t.Fatalf("empty role model should inherit llm.model: %v", err)
	}
}

func TestRegistryRejectsExecutorCapabilityEscalation(t *testing.T) {
	registry := testRegistry(t)
	if _, _, err := registry.Route(protocol.ReplyRequest{
		Prompt: "run shell", Skills: []string{"shell"},
	}); err == nil {
		t.Fatal("disallowed skill must be rejected before model execution")
	}
	_, plan, err := registry.Route(protocol.ReplyRequest{Prompt: "calculate", Skills: []string{"calculate"}})
	if err != nil {
		t.Fatal(err)
	}
	verification := registry.Verify(plan, protocol.ReplyResponse{
		Message:  protocol.Message{Role: "assistant", Content: "done"},
		ToolRuns: []protocol.ToolRun{{Name: "calculate", Error: "failed"}},
	})
	if verification.Status != "rejected" || len(verification.Errors) == 0 {
		t.Fatalf("tool failure was accepted: %#v", verification)
	}
	mismatch := registry.Verify(plan, protocol.ReplyResponse{
		Message:  protocol.Message{Role: "assistant", Content: "5"},
		ToolRuns: []protocol.ToolRun{{Name: "calculate", Output: `{"result":4}`}},
	})
	if mismatch.Status != "rejected" {
		t.Fatalf("answer that contradicts tool evidence was accepted: %#v", mismatch)
	}
	leak := registry.Verify(plan, protocol.ReplyResponse{
		Message: protocol.Message{Role: "assistant", Content: `<tool_call>{"name":"calculate","arguments":{"a":1,"op":"add","b":2}}</tool_call>`},
	})
	if leak.Status != "rejected" {
		t.Fatalf("answer leaking a raw tool_call was accepted: %#v", leak)
	}
	unclosed := registry.Verify(plan, protocol.ReplyResponse{
		Message: protocol.Message{Role: "assistant", Content: `<tool_call>{"name":"calculate","arguments":{"a":1}}`},
	})
	if unclosed.Status != "rejected" {
		t.Fatalf("answer leaking an unclosed tool_call was accepted: %#v", unclosed)
	}
}

func TestOutputEvidenceAppearsPreservesJSONNumbers(t *testing.T) {
	for _, test := range []struct {
		content string
		output  string
	}{
		{"执行结果：结果 5119744。", `{"result":5119744}`},
		{"执行结果：结果 9007199254740993。", `{"result":9007199254740993}`},
		{"执行结果：items []。", `{"items":[]}`},
	} {
		if !outputEvidenceAppears(test.content, test.output) {
			t.Fatalf("valid evidence was not found: content=%q output=%q", test.content, test.output)
		}
	}
}

func TestVerifierRejectsCapabilityContradiction(t *testing.T) {
	registry := testRegistry(t)
	request, plan, err := registry.Route(protocol.ReplyRequest{
		Prompt: "clock 有什么用",
	})
	if err != nil {
		t.Fatal(err)
	}
	request.Messages = []protocol.Message{{
		Role:    "assistant",
		Content: "当前可调用 Tool：clock、calculate",
	}}
	verification := registry.VerifyWithRequest(plan, request, protocol.ReplyResponse{
		Message: protocol.Message{
			Role:    "assistant",
			Content: "抱歉，我当前没有 clock 这个工具。",
		},
	})
	if verification.Status != "rejected" ||
		!strings.Contains(strings.Join(verification.Errors, " "), "denies capability clock") {
		t.Fatalf("capability contradiction was accepted: %#v", verification)
	}
	accepted := registry.VerifyWithRequest(plan, request, protocol.ReplyResponse{
		Message: protocol.Message{
			Role:    "assistant",
			Content: "clock 已注册且可调用；没有时区参数时返回节点默认时区。",
		},
	})
	if accepted.Status != "accepted" {
		t.Fatalf("unrelated negative phrase was treated as a contradiction: %#v", accepted)
	}
}

func TestVerifierRequiresExactLiteralProbeOutput(t *testing.T) {
	registry := testRegistry(t)
	request, plan, err := registry.Route(protocol.ReplyRequest{
		Prompt:       "execute literal probe",
		ExpectedText: "READY_QWEN06_WORKER1",
	})
	if err != nil {
		t.Fatal(err)
	}
	mismatch := registry.VerifyWithRequest(plan, request, protocol.ReplyResponse{
		Message: protocol.Message{Role: "assistant", Content: `{"name":"READY_QWEN06_WORKER1"}`},
	})
	if mismatch.Status != "rejected" ||
		!strings.Contains(strings.Join(mismatch.Errors, " "), "literal probe mismatch") {
		t.Fatalf("literal mismatch was accepted: %#v", mismatch)
	}
	accepted := registry.VerifyWithRequest(plan, request, protocol.ReplyResponse{
		Message: protocol.Message{Role: "assistant", Content: "READY_QWEN06_WORKER1"},
	})
	if accepted.Status != "accepted" ||
		!strings.Contains(strings.Join(accepted.Evidence, " "), "literal_probe:exact_match") {
		t.Fatalf("exact literal output was rejected: %#v", accepted)
	}
}

func TestPipelineUsesCapabilityMatchedRolesWithoutFixedNames(t *testing.T) {
	roleDirectory := t.TempDir()
	pipelineDirectory := t.TempDir()
	manifests := map[string]string{
		"writer.json": `{
			"schema_version":"ecs.agent/v1","name":"writer","purpose":"write",
			"execution_mode":"llm","model":"writer-model",
			"allowed_skills":["text_replace"],"allowed_tools":["text_replace"],
			"input_schema":{"type":"object"},"output_schema":{"type":"object"},
			"failure_policy":{"max_retries":0,"on_failure":"return_error"}
		}`,
		"calculator.json": `{
			"schema_version":"ecs.agent/v1","name":"calculator","purpose":"calculate",
			"execution_mode":"llm","model":"calculator-model",
			"allowed_skills":["calculate"],"allowed_tools":["calculate"],
			"input_schema":{"type":"object"},"output_schema":{"type":"object"},
			"failure_policy":{"max_retries":0,"on_failure":"return_error"}
		}`,
	}
	writeFiles(t, roleDirectory, manifests)
	writeFiles(t, pipelineDirectory, map[string]string{
		"office.json": `{
			"schema_version":"ecs.pipeline/v1","name":"office",
			"strategy":"capability_single_task",
			"executors":["writer","calculator"],"fallback_executor":"writer"
		}`,
	})
	registry := NewRegistry(config.AgentRolesConfig{
		Directory:         roleDirectory,
		PipelineDirectory: pipelineDirectory,
		DefaultPipeline:   "office",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	request, plan, err := registry.Route(protocol.ReplyRequest{
		Prompt: "calculate 8 / 2",
		Skills: []string{"calculate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].Agent != "calculator" ||
		plan.Planner != "" || plan.Verifier != "" || request.Model != "calculator-model" {
		t.Fatalf("capability routing selected the wrong role: request=%#v plan=%#v", request, plan)
	}
	verification := registry.Verify(plan, protocol.ReplyResponse{
		Message: protocol.Message{Role: "assistant", Content: "4"},
	})
	if verification.Validation != "skipped" || verification.Status != "accepted" {
		t.Fatalf("optional verifier was not skipped: %#v", verification)
	}
	if _, _, err := registry.Route(protocol.ReplyRequest{
		Prompt:   "test",
		Pipeline: "not-installed",
	}); err == nil {
		t.Fatal("unknown request pipeline must be rejected")
	}
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	directory := t.TempDir()
	pipelineDirectory := t.TempDir()
	manifests := map[string]string{
		"planner.json": `{
			"schema_version":"ecs.agent/v1","name":"planner","purpose":"plan",
			"execution_mode":"deterministic","model":"test-model",
			"input_schema":{"type":"object"},"output_schema":{"type":"object"},
			"failure_policy":{"max_retries":0,"on_failure":"fail"}
		}`,
		"executor.json": `{
			"schema_version":"ecs.agent/v1","name":"executor","purpose":"execute",
			"execution_mode":"llm","model":"test-model",
			"allowed_skills":["calculate","capabilities","text_stats"],"allowed_tools":["calculate","capabilities","text_stats","office_action_compensate"],
			"input_schema":{"type":"object"},"output_schema":{"type":"object"},
			"failure_policy":{"max_retries":0,"on_failure":"return_error"}
		}`,
		"verifier.json": `{
			"schema_version":"ecs.agent/v1","name":"verifier","purpose":"verify",
			"execution_mode":"deterministic","model":"test-model",
			"allowed_tools":["calculate"],
			"input_schema":{"type":"object"},"output_schema":{"type":"object"},
			"failure_policy":{"max_retries":0,"on_failure":"return_error"}
		}`,
	}
	writeFiles(t, directory, manifests)
	writeFiles(t, pipelineDirectory, map[string]string{
		"default.json": `{
			"schema_version":"ecs.pipeline/v1","name":"default",
			"strategy":"capability_single_task",
			"planner":"planner","executors":["executor"],
			"fallback_executor":"executor","verifier":"verifier"
		}`,
	})
	registry := NewRegistry(
		config.AgentRolesConfig{
			Directory:         directory,
			PipelineDirectory: pipelineDirectory,
			DefaultPipeline:   "default",
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	return registry
}

func writeFiles(t *testing.T, directory string, files map[string]string) {
	t.Helper()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRouteAgentDirectPinsPrincipal(t *testing.T) {
	registry := testRegistry(t)
	request, plan, err := registry.Route(protocol.ReplyRequest{
		Prompt:        "extract meeting facts",
		Agent:         "executor",
		Skills:        []string{"text_stats"},
		Tools:         []string{"text_stats"},
		RestrictSkills: true,
		RestrictTools:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].Agent != "executor" ||
		plan.Strategy != "ecs-agent-direct" || plan.Pipeline != "default" {
		t.Fatalf("agent-direct routing produced the wrong plan: %#v", plan)
	}
	if request.Agent != "executor" || len(request.Skills) != 1 || request.Skills[0] != "text_stats" {
		t.Fatalf("agent-direct request was not constrained: %#v", request)
	}
}

func TestRouteAgentDirectFailsClosed(t *testing.T) {
	registry := testRegistry(t)
	cases := []struct {
		name    string
		request protocol.ReplyRequest
	}{
		{
			name: "unknown agent",
			request: protocol.ReplyRequest{
				Prompt: "task", Agent: "ghost", Skills: []string{"calculate"}, RestrictSkills: true,
			},
		},
		{
			name: "non-llm agent",
			request: protocol.ReplyRequest{
				Prompt: "task", Agent: "planner", Skills: []string{"calculate"}, RestrictSkills: true,
			},
		},
		{
			name: "capability outside agent allowlist",
			request: protocol.ReplyRequest{
				Prompt: "task", Agent: "executor", Skills: []string{"meeting.fact.extract"}, RestrictSkills: true,
			},
		},
		{
			name: "required capability outside agent allowlist",
			request: protocol.ReplyRequest{
				Prompt: "task", Agent: "executor", Skills: []string{"calculate"},
				RequiredSkills: []string{"meeting.fact.extract"}, RestrictSkills: true,
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := registry.Route(test.request); err == nil {
				t.Fatalf("agent-direct route %q must fail closed: %#v", test.name, test.request)
			}
		})
	}
}

func TestRouteAgentDirectInheritsAgentModel(t *testing.T) {
	roleDirectory := t.TempDir()
	pipelineDirectory := t.TempDir()
	writeFiles(t, roleDirectory, map[string]string{
		"specialist.json": `{
			"schema_version":"ecs.agent/v1","name":"specialist","purpose":"specialize",
			"execution_mode":"llm","model":"specialist-model",
			"allowed_skills":["meeting.fact.extract"],"allowed_tools":["meeting_fact_extract"],
			"input_schema":{"type":"object"},"output_schema":{"type":"object"},
			"failure_policy":{"max_retries":0,"on_failure":"return_error"}
		}`,
	})
	writeFiles(t, pipelineDirectory, map[string]string{
		"default.json": `{
			"schema_version":"ecs.pipeline/v1","name":"default",
			"strategy":"capability_single_task",
			"executors":["specialist"],"fallback_executor":"specialist"
		}`,
	})
	registry := NewRegistry(config.AgentRolesConfig{
		Directory:         roleDirectory,
		PipelineDirectory: pipelineDirectory,
		DefaultPipeline:   "default",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	request, plan, err := registry.Route(protocol.ReplyRequest{
		Prompt:        "extract facts",
		Agent:         "specialist",
		Skills:        []string{"meeting.fact.extract"},
		Tools:         []string{"meeting_fact_extract"},
		RestrictSkills: true,
		RestrictTools:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Model != "specialist-model" || plan.Tasks[0].Agent != "specialist" {
		t.Fatalf("agent-direct model inheritance failed: request=%#v plan=%#v", request, plan)
	}
}

func TestRouteWithoutAgentKeepsPipelineSelection(t *testing.T) {
	registry := testRegistry(t)
	_, plan, err := registry.Route(protocol.ReplyRequest{
		Prompt: "calculate 2+2",
		Skills: []string{"calculate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Strategy != "ecs-capability-single-task" || plan.Tasks[0].Agent != "executor" {
		t.Fatalf("plain pipeline routing changed: %#v", plan)
	}
}
