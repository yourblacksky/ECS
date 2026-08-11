package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"ecs/internal/protocol"
	"ecs/internal/roles"
	"ecs/internal/schema"
	"ecs/internal/skills"
	"ecs/internal/tools"
)

func TestBindSingleStringInput(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{"type": "string"},
		},
		"required": []any{"text"},
	}
	arguments, ok := bindManifestInput(
		`text_stats ： “啊；老师快点发来看还是短发国鹏； ”`,
		"text_stats",
		schema,
	)
	if !ok {
		t.Fatal("explicit single-string invocation was not bound")
	}
	var decoded map[string]string
	if err := json.Unmarshal(arguments, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["text"] != "啊；老师快点发来看还是短发国鹏；" {
		t.Fatalf("quoted input was not preserved: %#v", decoded)
	}
}

func TestMissingInputResponseUsesModelFacingSlots(t *testing.T) {
	manifest := tools.Manifest{InputAdapter: schema.Adapter{
		Mappings: map[string]string{"schedule.start_at": "start_time"},
	}}
	missing := modelFacingMissingFields([]string{"schedule.start_at"}, manifest)
	response := missingInputResponse(
		protocol.ReplyRequest{RequestID: "req-1", SessionID: "session-1"},
		"meeting.create",
		json.RawMessage(`{"title":"复盘"}`),
		missing,
	)
	if response.InputRequest == nil || response.InputRequest.MissingFields[0] != "start_time" {
		t.Fatalf("missing input was not structured: %#v", response)
	}
	if response.Verification == nil || response.Verification.Status != "needs_input" ||
		!strings.Contains(response.Message.Content, "`start_time`") {
		t.Fatalf("slot gate response is incomplete: %#v", response)
	}
}

func TestBindSingleStringInputRejectsAmbiguousInput(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"old": map[string]any{"type": "string"},
			"new": map[string]any{"type": "string"},
		},
		"required": []string{"old", "new"},
	}
	for _, prompt := range []string{
		"text_replace: a -> b",
		"请调用 text_replace: a -> b",
		"text_replace a -> b",
	} {
		if _, ok := bindManifestInput(prompt, "text_replace", schema); ok {
			t.Fatalf("ambiguous invocation was bound: %q", prompt)
		}
	}
}

func TestBindManifestInputSupportsNoArgumentsAndExplicitJSON(t *testing.T) {
	arguments, ok := bindManifestInput("clock", "clock", map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	})
	if !ok || string(arguments) != `{}` {
		t.Fatalf("no-argument capability was not bound: %s, %v", arguments, ok)
	}

	input := `calculate: {"a":2,"op":"add","b":3}`
	arguments, ok = bindManifestInput(input, "calculate", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a":  map[string]any{"type": "number"},
			"op": map[string]any{"type": "string"},
			"b":  map[string]any{"type": "number"},
		},
		"required": []any{"a", "op", "b"},
	})
	if !ok || string(arguments) != `{"a":2,"op":"add","b":3}` {
		t.Fatalf("explicit JSON arguments were not preserved: %s, %v", arguments, ok)
	}
}

func TestParseCapabilityDecision(t *testing.T) {
	decision, err := parseCapabilityDecision(
		"```json\n{\"execute\":true,\"arguments\":{\"prefix\":\"demo\"}}\n```",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Execute || string(decision.Arguments) != `{"prefix":"demo"}` {
		t.Fatalf("unexpected execute decision: %#v", decision)
	}
	decision, err = parseCapabilityDecision(`{"execute":false,"reply":"只介绍，不执行。"}`)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Execute || decision.Reply != "只介绍，不执行。" {
		t.Fatalf("unexpected non-execute decision: %#v", decision)
	}
	if _, err := parseCapabilityDecision(`{"execute":true}`); err == nil {
		t.Fatal("execute decision without arguments must fail")
	}
}

func TestValidateCapabilityDecisionArgumentsRejectsMissingRequiredFields(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action_items":    map[string]any{"type": "array"},
			"idempotency_key": map[string]any{"type": "string"},
		},
		"required": []any{"action_items", "idempotency_key"},
	}
	if err := validateCapabilityDecisionArguments(
		json.RawMessage(`{"action_items":[]}`), schema,
	); err == nil || !strings.Contains(err.Error(), "idempotency_key") {
		t.Fatalf("missing required field was accepted: %v", err)
	}
	if err := validateCapabilityDecisionArguments(
		json.RawMessage(`{"action_items":[],"idempotency_key":"order-1"}`), schema,
	); err != nil {
		t.Fatalf("valid capability arguments were rejected: %v", err)
	}
}

func TestPlanWithoutRequiredCallsPreservesPolicy(t *testing.T) {
	plan := roles.Plan{Tasks: []roles.Task{{
		AllowedSkills:  []string{"make_id"},
		AllowedTools:   []string{"make_id"},
		RequiredSkills: []string{"make_id"},
		RequiredTools:  []string{"make_id"},
	}}}
	cleared := planWithoutRequiredCalls(plan)
	if len(cleared.Tasks[0].RequiredSkills) != 0 || len(cleared.Tasks[0].RequiredTools) != 0 {
		t.Fatalf("requirements were not cleared: %#v", cleared)
	}
	if len(cleared.Tasks[0].AllowedSkills) != 1 || len(cleared.Tasks[0].AllowedTools) != 1 {
		t.Fatalf("capability policy changed: %#v", cleared)
	}
	if len(plan.Tasks[0].RequiredSkills) != 1 || len(plan.Tasks[0].RequiredTools) != 1 {
		t.Fatalf("original plan was mutated: %#v", plan)
	}
}

func TestConstrainedSkillSequencePreservesRequiredOrder(t *testing.T) {
	plan := roles.Plan{Tasks: []roles.Task{{RequiredSkills: []string{"first", "second"}}}}
	available := []tools.Manifest{{Name: "second"}, {Name: "first"}}
	allowed := map[string]skills.Manifest{
		"first":  {Name: "first"},
		"second": {Name: "second"},
	}
	targets, ok := constrainedSkillSequenceTargets(plan, available, allowed)
	if !ok || strings.Join(targets, ",") != "first,second" {
		t.Fatalf("required Skill order was not preserved: %#v, %v", targets, ok)
	}
	step := planWithRequiredSkill(plan, "second")
	if strings.Join(planRequiredSkills(step), ",") != "second" || len(planRequiredTools(step)) != 0 {
		t.Fatalf("sequence step plan is invalid: %#v", step)
	}
}

func TestRequiredOfficeCapabilityCannotBecomeExplanationOnly(t *testing.T) {
	plan := roles.Plan{Tasks: []roles.Task{{RequiredSkills: []string{"meeting.scope.plan"}}}}
	officeRequest := protocol.ReplyRequest{Metadata: map[string]string{
		"office_task": "office_123.scope",
	}}
	if !requiredOfficeCapability(plan, officeRequest, "meeting.scope.plan") {
		t.Fatal("Office required Skill was not marked mandatory")
	}
	if requiredOfficeCapability(plan, protocol.ReplyRequest{}, "meeting.scope.plan") {
		t.Fatal("ordinary conversation was incorrectly forced to execute")
	}
}

func TestTypedHandoffArgumentsUsesLatestSchemaCompatibleOutput(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"receipts": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "object"},
			},
		},
		"required":             []any{"receipts"},
		"additionalProperties": false,
	}
	arguments, ok := typedHandoffArguments([]string{
		`not json`,
		`{"action_items":[]}`,
		`{"receipts":[{"id":"receipt-1"}]}`,
	}, schema)
	if !ok || string(arguments) != `{"receipts":[{"id":"receipt-1"}]}` {
		t.Fatalf("compatible Skill output was not bound: %s, %v", arguments, ok)
	}
	if _, ok := typedHandoffArguments([]string{
		`[{"id":"receipt-1"}]`,
		`{"action_items":[]}`,
	}, schema); ok {
		t.Fatal("incompatible Skill output was bound")
	}
}

func TestRenderNonExecutionCapabilityReplyUsesManifest(t *testing.T) {
	reply := renderNonExecutionCapabilityReply("make_id", tools.Manifest{
		Description: "生成随机唯一标识；可添加前缀",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"prefix": map[string]any{"type": "string"},
			},
		},
	})
	for _, expected := range []string{"未执行 `make_id`", "可添加前缀", "`prefix`（string，可选）"} {
		if !strings.Contains(reply, expected) {
			t.Fatalf("manifest reply %q is missing %q", reply, expected)
		}
	}
}

func TestRenderCapabilityOutputHandlesAllJSONShapes(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected []string
	}{
		{"large number", `{"result":5119744}`, []string{"结果：5119744"}},
		{"large exact integer", `{"result":9007199254740993}`, []string{"9007199254740993"}},
		{"generic nested output", `{"items":[{"name":"alpha","score":2}],"ok":true}`, []string{"items[0].name alpha", "items[0].score 2", "ok true"}},
		{"empty and null output", `{"items":[],"next":null}`, []string{"items []", "next null"}},
		{"top-level array", `["alpha",2]`, []string{`["alpha",2]`}},
		{"plain text", `already readable`, []string{"already readable"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := renderCapabilityOutput(test.output)
			for _, expected := range test.expected {
				if !strings.Contains(result, expected) {
					t.Fatalf("rendered output %q is missing %q", result, expected)
				}
			}
		})
	}
}

func TestNormalizeExecutedCapabilityResponseOnlyRewritesEvidenceEcho(t *testing.T) {
	toolRuns := []protocol.ToolRun{{Name: "calculate", Output: `{"result":5119744}`}}
	rendered := normalizeExecutedCapabilityResponse(` { "result": 5119744 } `, toolRuns, nil)
	if strings.Contains(rendered, `{"result"`) || !strings.Contains(rendered, "结果：5119744") {
		t.Fatalf("raw evidence echo was not rendered: %q", rendered)
	}
	for _, content := range []string{"结果是 5119744。", `{"different":5119744}`} {
		if got := normalizeExecutedCapabilityResponse(content, toolRuns, nil); got != content {
			t.Fatalf("non-matching model response changed: got %q want %q", got, content)
		}
	}
}
