// ecs-agentteams-testbed supplies deterministic Controller, Matrix, and LLM
// boundaries for the AgentTeams -> ECS -> Office end-to-end regression. It is
// test-only and must not be used as an AgentTeams or model implementation.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// e2eSource must match the work order input used by the E2E script so the
// deterministic model output lines up with the real meeting.fact.extract
// execution inside ECS.
const e2eSource = "决定：本周完成可信会议督办演示\n" +
	"行动：整理五分钟演示清单 | owner=张三 | due=2026-08-03 | evidence=会议记录第2页\n" +
	"行动：核对回执证据 | owner=李四 | due=2026-08-04 | evidence=会议记录第3页"

var e2eActionItems = []map[string]string{
	{"summary": "整理五分钟演示清单", "owner": "张三", "deadline": "2026-08-03", "evidence": "会议记录第2页"},
	{"summary": "核对回执证据", "owner": "李四", "deadline": "2026-08-04", "evidence": "会议记录第3页"},
}

type state struct {
	mu             sync.Mutex
	readyReports   int
	assignmentSent int
	report         json.RawMessage
	stateDir       string
	taskIDs        []string
	resends        map[string]int
	lastAssigned   map[string]time.Time
	now            func() time.Time
	workerUserID   string
	leaderUserID   string
}

const (
	maxTaskResends    = 30
	taskRetryInterval = 500 * time.Millisecond
)

func main() {
	var listen, stateDir, taskIDs, workerUserID, leaderUserID string
	flag.StringVar(&listen, "listen", "127.0.0.1:18320", "testbed listen address")
	flag.StringVar(&stateDir, "state-dir", "", "directory containing the shared task and captured state")
	flag.StringVar(&taskIDs, "task-id", "agentteams-office-e2e", "comma-separated assigned task ids in dispatch order")
	flag.StringVar(&workerUserID, "worker-user", "@ecs-worker-1:matrix.test", "Matrix worker user id")
	flag.StringVar(&leaderUserID, "leader-user", "@agentteams-manager:matrix.test", "Matrix leader user id")
	flag.Parse()
	if stateDir == "" {
		log.Fatal("-state-dir is required")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		log.Fatal(err)
	}
	var ids []string
	for _, id := range strings.Split(taskIDs, ",") {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	if len(ids) == 0 {
		log.Fatal("-task-id must contain at least one task id")
	}
	harness := &state{
		stateDir: stateDir, taskIDs: ids, resends: make(map[string]int),
		lastAssigned: make(map[string]time.Time), now: time.Now,
		workerUserID: workerUserID, leaderUserID: leaderUserID,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", harness.health)
	mux.HandleFunc("GET /state", harness.getState)
	mux.HandleFunc("POST /api/v1/workers/{name}/ready", harness.ready)
	mux.HandleFunc("GET /_matrix/client/v3/sync", harness.syncMatrix)
	mux.HandleFunc("POST /_matrix/client/v3/join/{room}", harness.joinMatrix)
	mux.HandleFunc("PUT /_matrix/client/v3/rooms/{room}/send/m.room.message/{transaction}", harness.reportMatrix)
	mux.HandleFunc("POST /v1/chat/completions", harness.chat)
	log.Printf("AgentTeams E2E testbed listening on %s", listen)
	log.Fatal(http.ListenAndServe(listen, mux))
}

func (s *state) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "kind": "agentteams-e2e-testbed"})
}

func (s *state) ready(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.readyReports++
	s.persistLocked()
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// nextAssignable returns the first unfinished task in dependency order. Each
// task owns its delivery state, so a lost early assignment can be retried
// without waiting for the initial cursor to reach the last task. Pending human
// approval is retried indefinitely but paced; ordinary delivery retries remain
// bounded.
func (s *state) nextAssignable() int {
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	for index, taskID := range s.taskIDs {
		if _, err := os.Stat(filepath.Join(s.stateDir, "shared", "tasks", taskID, "result.json")); err == nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.stateDir, "shared", "tasks", taskID, "meta.json")); err != nil {
			return -1
		}
		if index > 0 {
			previous := s.taskIDs[index-1]
			if _, err := os.Stat(filepath.Join(s.stateDir, "shared", "tasks", previous, "result.json")); err != nil {
				return -1
			}
		}
		last := s.lastAssigned[taskID]
		if last.IsZero() {
			return index
		}
		if now.Sub(last) < taskRetryInterval {
			return -1
		}
		// A task paused at the human approval gate has no result.json by
		// design; it is expected to wait for the gate and then resume through
		// the same assignment. Re-assigning it consumes no budget so an admin
		// that approves late still triggers the resume.
		if status, err := taskStatus(s.stateDir, taskID); err == nil && status == "pending_approval" {
			return index
		}
		if s.resends[taskID] >= maxTaskResends {
			return -1
		}
		s.resends[taskID]++
		return index
	}
	return -1
}

func (s *state) markAssigned(index int) int {
	if s.lastAssigned == nil {
		s.lastAssigned = make(map[string]time.Time)
	}
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	s.lastAssigned[s.taskIDs[index]] = now
	s.assignmentSent++
	return s.assignmentSent
}

// taskStatus reads the persisted AgentTeams task status from its meta.json.
func taskStatus(stateDir string, taskID string) (string, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, "shared", "tasks", taskID, "meta.json"))
	if err != nil {
		return "", err
	}
	var meta struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", err
	}
	return meta.Status, nil
}

func (s *state) syncMatrix(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	assignIndex := s.nextAssignable()
	assignmentSequence := s.assignmentSent
	if assignIndex >= 0 {
		assignmentSequence = s.markAssigned(assignIndex)
	}
	s.persistLocked()
	s.mu.Unlock()
	if assignIndex < 0 {
		time.Sleep(50 * time.Millisecond)
		writeJSON(w, http.StatusOK, map[string]any{
			"next_batch": fmt.Sprintf("idle-%d", time.Now().UnixNano()),
			"rooms":      map[string]any{"join": map[string]any{}},
		})
		return
	}
	taskID := s.taskIDs[assignIndex]
	event := map[string]any{
		"type": "m.room.message", "event_id": fmt.Sprintf("$ecs-agentteams-e2e-%d", assignmentSequence),
		"sender": s.leaderUserID,
		"content": map[string]any{
			"msgtype":    "m.text",
			"body":       fmt.Sprintf("%s TASK_ASSIGNED: %s - execute the assigned Office stage", s.workerUserID, taskID),
			"m.mentions": map[string]any{"user_ids": []string{s.workerUserID}},
		},
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"next_batch": fmt.Sprintf("assignment-%d", assignmentSequence),
		"rooms": map[string]any{"join": map[string]any{
			"!ecs-e2e:matrix.test": map[string]any{"timeline": map[string]any{"events": []any{event}}},
		}},
	})
}

func (s *state) joinMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"room_id": r.PathValue("room")})
}

func (s *state) reportMatrix(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var payload json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.report = append(json.RawMessage(nil), payload...)
	s.persistLocked()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"event_id": "$ecs-agentteams-e2e-result"})
}

func (s *state) chat(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var request struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	messages := make([]string, 0, len(request.Messages))
	for _, message := range request.Messages {
		messages = append(messages, message.Content)
	}
	content := deterministicChatResponse(messages)
	writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{
		"message": map[string]string{"role": "assistant", "content": content},
	}}})
}

// deterministicChatResponse implements the Office required-capability
// protocol: for every Office stage the router pins the capability in the
// system message ("能力已经锁定为 ...") and the model only fills arguments as
// {"execute":true,"arguments":{...}}. Tool results then produce a completion
// line instead of another model call.
func deterministicChatResponse(messages []string) string {
	last := ""
	if len(messages) > 0 {
		last = messages[len(messages)-1]
	}
	if strings.Contains(last, "BLOCKED") {
		return "BLOCKED"
	}
	for _, message := range messages {
		if strings.HasPrefix(message, "工具 ") && strings.Contains(message, " 的结果") {
			return "AgentTeams assignment accepted; Office stage is complete."
		}
	}
	for _, message := range messages {
		if !strings.Contains(message, "能力已经锁定为") {
			continue
		}
		switch {
		case strings.Contains(message, "meeting.scope.plan"):
			return `{"execute":true,"arguments":{"meeting_title":"AgentTeams ECS E2E","participants":["张三","李四","王经理"]}}`
		case strings.Contains(message, "meeting.fact.extract"):
			source, _ := json.Marshal(e2eSource)
			return `{"execute":true,"arguments":{"source":` + string(source) + `}}`
		case strings.Contains(message, "meeting.evidence.verify"):
			items, _ := json.Marshal(e2eActionItems)
			return `{"execute":true,"arguments":{"action_items":` + string(items) + `}}`
		case strings.Contains(message, "office.action.dispatch"):
			items, _ := json.Marshal(e2eActionItems)
			key := extractIdempotencyKey(messages)
			return `{"execute":true,"arguments":{"action_items":` + string(items) +
				`,"idempotency_key":` + strconv.Quote(key) + `}}`
		}
	}
	return "AgentTeams assignment accepted; Office stage is complete."
}

var idempotencyPattern = regexp.MustCompile(`幂等键\s*(office:[\w:.-]+)`)

func extractIdempotencyKey(messages []string) string {
	for _, message := range messages {
		if match := idempotencyPattern.FindStringSubmatch(message); len(match) == 2 {
			return match[1]
		}
	}
	return "office:missing"
}

func (s *state) getState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.snapshotLocked())
}

func (s *state) persistLocked() {
	data, err := json.MarshalIndent(s.snapshotLocked(), "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')
	temporary := filepath.Join(s.stateDir, ".controller-state.tmp")
	if os.WriteFile(temporary, data, 0o600) == nil {
		_ = os.Rename(temporary, filepath.Join(s.stateDir, "controller-state.json"))
	}
}

func (s *state) snapshotLocked() map[string]any {
	return map[string]any{
		"ready_reports":     s.readyReports,
		"assignments_sent":  s.assignmentSent,
		"task_ids":          s.taskIDs,
		"completion_report": s.report,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
