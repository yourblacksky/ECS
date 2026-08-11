// ecs-mock-llm is a deterministic OpenAI-compatible test double.
// It is intentionally not a language model and must not be used in production.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type chatRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// meetingSource must stay byte-identical with the fixtures used by the
// AgentTeams testbed and the meeting demo so the deterministic model output
// lines up with the real meeting.fact.extract execution inside ECS.
const meetingSource = "决定：本周完成可信会议督办演示\n" +
	"行动：整理五分钟演示清单 | owner=张三 | due=2026-08-03 | evidence=会议记录第2页\n" +
	"行动：核对回执证据 | owner=李四 | due=2026-08-04 | evidence=会议记录第3页"

var meetingActionItems = []map[string]string{
	{"summary": "整理五分钟演示清单", "owner": "张三", "deadline": "2026-08-03", "evidence": "会议记录第2页"},
	{"summary": "核对回执证据", "owner": "李四", "deadline": "2026-08-04", "evidence": "会议记录第3页"},
}

var idempotencyPattern = regexp.MustCompile(`幂等键\s*"?(office:[\w:.-]+)"?`)

func main() {
	var listen string
	flag.StringVar(&listen, "listen", "127.0.0.1:11434", "test server listen address")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "kind": "mock-llm"})
	})
	mux.HandleFunc("POST /v1/chat/completions", chat)
	log.Printf("test-only mock LLM listening on %s", listen)
	log.Fatal(http.ListenAndServe(listen, mux))
}

func chat(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var request chatRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": err.Error()},
		})
		return
	}
	last := ""
	if len(request.Messages) > 0 {
		last = request.Messages[len(request.Messages)-1].Content
	}
	if strings.HasPrefix(last, "工具 ") {
		writeAssistant(w, "mock 已收到工具结果："+last)
		return
	}
	if content, ok := officeCapabilityDecision(request.Messages); ok {
		writeAssistant(w, content)
		return
	}

	var call string
	switch {
	case strings.Contains(last, "计算"):
		call = `{"name":"calculate","arguments":{"a":12,"op":"multiply","b":3}}`
	case strings.Contains(last, "时间"):
		call = `{"name":"clock","arguments":{}}`
	case strings.Contains(last, "统计"):
		call = `{"name":"text_stats","arguments":{"text":"你好 world\n第二行"}}`
	case strings.Contains(last, "替换"):
		call = `{"name":"text_replace","arguments":{"text":"a-b-a","old":"a","new":"x"}}`
	case strings.Contains(strings.ToLower(last), "id"):
		call = `{"name":"make_id","arguments":{"prefix":"task-"}}`
	default:
		writeAssistant(w, "mock 直接回答")
		return
	}
	writeAssistant(w, "<tool_call>"+call+"</tool_call>")
}

// officeCapabilityDecision implements the Office required-capability protocol:
// the router pins the locked capability in the system message ("能力已经锁定为
// ...") and the model only fills arguments as {"execute":true,"arguments":{...}}.
func officeCapabilityDecision(messages []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}) (string, bool) {
	var contents []string
	for _, message := range messages {
		contents = append(contents, message.Content)
	}
	return officeDecisionFromContents(contents)
}

func officeDecisionFromContents(contents []string) (string, bool) {
	for _, content := range contents {
		if !strings.Contains(content, "能力已经锁定为") {
			continue
		}
		switch {
		case strings.Contains(content, "meeting.scope.plan"):
			return `{"execute":true,"arguments":{"meeting_title":"AgentTeams ECS E2E","participants":["张三","李四","王经理"]}}`, true
		case strings.Contains(content, "meeting.fact.extract"):
			source, _ := json.Marshal(meetingSource)
			return `{"execute":true,"arguments":{"source":` + string(source) + `}}`, true
		case strings.Contains(content, "meeting.evidence.verify"):
			items, _ := json.Marshal(meetingActionItems)
			return `{"execute":true,"arguments":{"action_items":` + string(items) + `}}`, true
		case strings.Contains(content, "office.action.dispatch"):
			items, _ := json.Marshal(meetingActionItems)
			key := "office:missing"
			for _, candidate := range contents {
				if match := idempotencyPattern.FindStringSubmatch(candidate); len(match) == 2 {
					key = match[1]
					break
				}
			}
			return `{"execute":true,"arguments":{"action_items":` + string(items) +
				`,"idempotency_key":` + strconv.Quote(key) + `}}`, true
		}
	}
	return "", false
}

func writeAssistant(w http.ResponseWriter, content string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]string{
					"role":    "assistant",
					"content": content,
				},
			},
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
