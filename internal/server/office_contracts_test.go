package server

import (
	"encoding/json"
	"strings"
	"testing"

	"ecs/internal/office"
	"ecs/internal/protocol"
)

func TestOfficeTaskPromptPreservesStructuredSkillHandoff(t *testing.T) {
	upstreamResponse, err := json.Marshal(protocol.ReplyResponse{
		Message: protocol.Message{Role: "assistant", Content: "行动项：整理清单。"},
		SkillRuns: []protocol.SkillRun{{
			Name:   "meeting.evidence.verify",
			Status: "success",
			Output: `{"valid":true,"verified_items":[{"summary":"整理清单","owner":"张三"}]}`,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	order := office.WorkOrder{ID: "order-1", Input: json.RawMessage(`{"source":"meeting"}`), Tasks: []office.Task{
		{ID: "extract", Name: "核验会议结论", Result: upstreamResponse},
	}}
	task := office.Task{ID: "dispatch", Name: "发布行动", DependsOn: []string{"extract"}}
	prompt := officeTaskPrompt(order, task)
	if !strings.Contains(prompt, `meeting.evidence.verify 原始 JSON: {"valid":true`) ||
		!strings.Contains(prompt, `"owner":"张三"`) ||
		!strings.Contains(prompt, "展示结果: 行动项：整理清单。") {
		t.Fatalf("structured handoff was flattened: %s", prompt)
	}
}
