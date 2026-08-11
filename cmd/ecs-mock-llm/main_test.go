package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOfficeDecisionForMeetingStages(t *testing.T) {
	cases := []struct {
		name     string
		locked   string
		messages []string
		expected []string
	}{
		{
			name:   "scope",
			locked: "meeting.scope.plan",
			expected: []string{
				`"execute":true`, `"meeting_title":"AgentTeams ECS E2E"`, `"participants"`,
			},
		},
		{
			name:   "extract",
			locked: "meeting.fact.extract",
			expected: []string{
				`"execute":true`, `"source"`,
			},
		},
		{
			name:   "verify",
			locked: "meeting.evidence.verify",
			expected: []string{
				`"execute":true`, `"action_items"`, `"owner":"张三"`,
			},
		},
		{
			name:   "dispatch",
			locked: "office.action.dispatch",
			messages: []string{
				`执行动作必须使用精确幂等键 "office:order-1:order-1.dispatch"，并调用 execution.receipt.verify 校验连接器回执。`,
			},
			expected: []string{
				`"execute":true`, `"action_items"`, `"idempotency_key":"office:order-1:order-1.dispatch"`,
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			contents := []string{"你是 ECS 能力调用判定器。能力已经锁定为 " + test.locked + "。"}
			contents = append(contents, test.messages...)
			response, ok := officeDecisionFromContents(contents)
			if !ok {
				t.Fatalf("no decision produced for %s", test.name)
			}
			for _, expected := range test.expected {
				if !strings.Contains(response, expected) {
					t.Fatalf("%s decision %q is missing %q", test.name, response, expected)
				}
			}
			var decision map[string]any
			if err := json.Unmarshal([]byte(response), &decision); err != nil {
				t.Fatalf("%s decision is not JSON: %v", test.name, err)
			}
		})
	}
}

func TestOfficeDecisionPreservesPlainToolCalls(t *testing.T) {
	contents := []string{"计算 12 乘以 3"}
	if response, ok := officeDecisionFromContents(contents); ok {
		t.Fatalf("plain prompt produced an office decision: %q", response)
	}
}
