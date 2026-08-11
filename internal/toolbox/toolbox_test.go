package toolbox

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runTool(t *testing.T, name, input string) (int, string, string) {
	t.Helper()
	var output, errorOutput bytes.Buffer
	code := Run(name, strings.NewReader(input), &output, &errorOutput)
	return code, strings.TrimSpace(output.String()), strings.TrimSpace(errorOutput.String())
}

func TestCalculate(t *testing.T) {
	code, output, errorOutput := runTool(t, "calculate", `{"a":6,"op":"divide","b":4}`)
	if code != 0 || errorOutput != "" || output != `{"result":1.5}` {
		t.Fatalf("unexpected result: code=%d output=%s error=%s", code, output, errorOutput)
	}
	code, output, errorOutput = runTool(t, "calculate", `{"a":2,"op":"power","b":10}`)
	if code != 0 || errorOutput != "" || output != `{"result":1024}` {
		t.Fatalf("unexpected power result: code=%d output=%s error=%s", code, output, errorOutput)
	}
	code, output, errorOutput = runTool(t, "calculate", `{"a":1024,"op":"log_base","b":2}`)
	if code != 0 || errorOutput != "" || output != `{"result":10}` {
		t.Fatalf("unexpected logarithm result: code=%d output=%s error=%s", code, output, errorOutput)
	}
	code, _, errorOutput = runTool(t, "calculate", `{"a":1,"op":"divide","b":0}`)
	if code == 0 || !strings.Contains(errorOutput, "divide by zero") {
		t.Fatalf("division by zero should fail: code=%d error=%s", code, errorOutput)
	}
	code, _, errorOutput = runTool(t, "calculate", `{"a":1024,"op":"log_base","b":1}`)
	if code == 0 || !strings.Contains(errorOutput, "logarithm base") {
		t.Fatalf("invalid logarithm base should fail: code=%d error=%s", code, errorOutput)
	}
}

func TestTextTools(t *testing.T) {
	code, output, _ := runTool(t, "text_stats", `{"text":"你好 world\n第二行"}`)
	if code != 0 || output != `{"characters":12,"lines":2,"words":3}` {
		t.Fatalf("unexpected text stats: %s", output)
	}
	code, output, _ = runTool(t, "text_replace", `{"text":"a-b-a","old":"a","new":"x"}`)
	if code != 0 || output != `{"text":"x-b-x"}` {
		t.Fatalf("unexpected replacement: %s", output)
	}
}

func TestNativeTextCountCN(t *testing.T) {
	code, output, errorOutput := runTool(
		t,
		"text_count_cn",
		`{"text":"你好，Go １２3!\n"}`,
	)
	if code != 0 || errorOutput != "" {
		t.Fatalf("text_count_cn failed: code=%d error=%s", code, errorOutput)
	}
	var result struct {
		Total       int `json:"total_chars"`
		Chinese     int `json:"chinese_chars"`
		Letters     int `json:"letters"`
		Digits      int `json:"digits"`
		Punctuation int `json:"punctuation"`
		Spaces      int `json:"spaces"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 11 || result.Chinese != 2 || result.Letters != 2 ||
		result.Digits != 3 || result.Punctuation != 2 || result.Spaces != 2 {
		t.Fatalf("unexpected text_count_cn result: %s", output)
	}
}

func TestNativeTextDiff(t *testing.T) {
	code, output, errorOutput := runTool(
		t,
		"text_diff",
		`{"text_a":"a\nb\nc","text_b":"a\nB\nc"}`,
	)
	if code != 0 || errorOutput != "" {
		t.Fatalf("text_diff failed: code=%d error=%s", code, errorOutput)
	}
	var result struct {
		Added     int      `json:"added"`
		Removed   int      `json:"removed"`
		Unchanged int      `json:"unchanged"`
		Diff      []string `json:"diff"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 || result.Removed != 1 || result.Unchanged != 2 {
		t.Fatalf("unexpected text_diff counts: %s", output)
	}
	expected := []string{" a", "-b", "+B", " c"}
	if strings.Join(result.Diff, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("unexpected text_diff body: %#v", result.Diff)
	}
}

func TestNativeTextExtract(t *testing.T) {
	code, output, errorOutput := runTool(
		t,
		"text_extract",
		`{"text":"联系 a@example.com 或 13800138000，访问 https://example.com，日期 2026-08-03。","pattern":["email","phone","url","date"]}`,
	)
	if code != 0 || errorOutput != "" {
		t.Fatalf("text_extract failed: code=%d error=%s", code, errorOutput)
	}
	var result struct {
		Count     int                 `json:"count"`
		Matches   []string            `json:"matches"`
		ByPattern map[string][]string `json:"by_pattern"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 4 ||
		result.ByPattern["email"][0] != "a@example.com" ||
		result.ByPattern["phone"][0] != "13800138000" ||
		result.ByPattern["url"][0] != "https://example.com" ||
		result.ByPattern["date"][0] != "2026-08-03" {
		t.Fatalf("unexpected text_extract result: %s", output)
	}
}

func TestNativeWorkdays(t *testing.T) {
	code, output, errorOutput := runTool(
		t,
		"workdays",
		`{"start":"2026年8月3日","end":"2026年8月9日"}`,
	)
	if code != 0 || errorOutput != "" {
		t.Fatalf("workdays failed: code=%d error=%s", code, errorOutput)
	}
	var result struct {
		Workdays int `json:"workdays"`
		Weekend  int `json:"weekend_days"`
		Total    int `json:"total_days"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Workdays != 5 || result.Weekend != 2 || result.Total != 7 {
		t.Fatalf("unexpected workdays result: %s", output)
	}
	code, _, errorOutput = runTool(
		t,
		"workdays",
		`{"start":"2026-02-30","end":"2026-03-01"}`,
	)
	if code == 0 || !strings.Contains(errorOutput, "invalid input") {
		t.Fatalf("invalid date should fail: code=%d error=%s", code, errorOutput)
	}
}

func TestMakeID(t *testing.T) {
	code, output, errorOutput := runTool(t, "make_id", `{"prefix":"task-"}`)
	if code != 0 || errorOutput != "" {
		t.Fatalf("make_id failed: %s", errorOutput)
	}
	var result map[string]string
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result["id"], "task-") || len(result["id"]) != len("task-")+24 {
		t.Fatalf("unexpected id: %q", result["id"])
	}
}

func TestMeetingActionToolsProduceAndVerifyReceipts(t *testing.T) {
	code, scope, errorOutput := runTool(
		t, "meeting_scope_plan", `{"meeting_title":"ECS 交付会","participants":["张三","张三"," 李四 "]}`,
	)
	if code != 0 || errorOutput != "" || !strings.Contains(scope, `"meeting_type":"delivery"`) ||
		!strings.Contains(scope, `"participants":["张三","李四"]`) {
		t.Fatalf("meeting_scope_plan failed: code=%d output=%s error=%s", code, scope, errorOutput)
	}
	source := "决定：下周发布测试版\n行动：整理发布清单 | owner=张三 | due=2026-08-03"
	code, extracted, errorOutput := runTool(
		t, "meeting_fact_extract", `{"source":`+quotedJSON(source)+`}`,
	)
	if code != 0 || errorOutput != "" {
		t.Fatalf("meeting_fact_extract failed: code=%d error=%s", code, errorOutput)
	}
	var facts struct {
		Decisions   []string            `json:"decisions"`
		ActionItems []meetingActionItem `json:"action_items"`
		Unresolved  []string            `json:"unresolved"`
	}
	if err := json.Unmarshal([]byte(extracted), &facts); err != nil {
		t.Fatal(err)
	}
	if len(facts.Decisions) != 1 || facts.Decisions[0] != "下周发布测试版" ||
		len(facts.ActionItems) != 1 ||
		facts.ActionItems[0].Summary != "整理发布清单" ||
		facts.ActionItems[0].Owner != "张三" ||
		facts.Unresolved == nil || len(facts.Unresolved) != 0 {
		t.Fatalf("unexpected extracted facts: %s", extracted)
	}
	actionItems, _ := json.Marshal(facts.ActionItems)
	code, verified, errorOutput := runTool(
		t, "meeting_evidence_verify", `{"action_items":`+string(actionItems)+`}`,
	)
	if code != 0 || errorOutput != "" || !strings.Contains(verified, `"valid":true`) {
		t.Fatalf("meeting_evidence_verify failed: code=%d output=%s error=%s", code, verified, errorOutput)
	}
	var verification struct {
		Blockers []map[string]any `json:"blockers"`
	}
	if err := json.Unmarshal([]byte(verified), &verification); err != nil {
		t.Fatal(err)
	}
	if verification.Blockers == nil || len(verification.Blockers) != 0 {
		t.Fatalf("successful verification must emit an empty blockers array: %s", verified)
	}
	code, dispatched, errorOutput := runTool(
		t, "office_action_dispatch",
		`{"action_items":`+string(actionItems)+`,"idempotency_key":"meeting-1"}`,
	)
	if code != 0 || errorOutput != "" {
		t.Fatalf("office_action_dispatch failed: code=%d error=%s", code, errorOutput)
	}
	var dispatch struct {
		Receipts []actionReceipt `json:"receipts"`
	}
	if err := json.Unmarshal([]byte(dispatched), &dispatch); err != nil {
		t.Fatal(err)
	}
	receipts, _ := json.Marshal(dispatch.Receipts)
	code, receiptCheck, errorOutput := runTool(
		t, "execution_receipt_verify", `{"receipts":`+string(receipts)+`}`,
	)
	if code != 0 || errorOutput != "" || !strings.Contains(receiptCheck, `"valid":true`) {
		t.Fatalf("execution_receipt_verify failed: code=%d output=%s error=%s", code, receiptCheck, errorOutput)
	}
	var receiptVerification struct {
		Invalid []int `json:"invalid_receipts"`
	}
	if err := json.Unmarshal([]byte(receiptCheck), &receiptVerification); err != nil {
		t.Fatal(err)
	}
	if receiptVerification.Invalid == nil || len(receiptVerification.Invalid) != 0 {
		t.Fatalf("valid receipts must emit an empty invalid_receipts array: %s", receiptCheck)
	}
	code, compensation, errorOutput := runTool(
		t, "office_action_compensate",
		`{"action":"cancel_created_items","work_order":"office-1","task_id":"dispatch","idempotency_key":"office:office-1:dispatch:compensate","receipts":`+string(receipts)+`}`,
	)
	if code != 0 || errorOutput != "" ||
		!strings.Contains(compensation, `"result":"compensated"`) {
		t.Fatalf("office_action_compensate failed: code=%d output=%s error=%s", code, compensation, errorOutput)
	}
}

func quotedJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestUnknownTool(t *testing.T) {
	code, _, errorOutput := runTool(t, "missing", `{}`)
	if code == 0 || !strings.Contains(errorOutput, "unknown built-in tool") {
		t.Fatalf("unknown tool should fail: code=%d error=%s", code, errorOutput)
	}
}
