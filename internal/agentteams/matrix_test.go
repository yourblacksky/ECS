package agentteams

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
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/protocol"
)

func TestParseAssignment(t *testing.T) {
	taskID, ok := ParseAssignment(
		"@ecs-worker:matrix.local TASK_ASSIGNED: project-01-task-02 - Please start. Spec: shared/tasks/project-01-task-02/spec.md",
	)
	if !ok || taskID != "project-01-task-02" {
		t.Fatalf("unexpected assignment parse: %q %v", taskID, ok)
	}
	if _, ok := ParseAssignment("TASK_COMPLETED: task-1"); ok {
		t.Fatal("completion must not parse as assignment")
	}
}

func TestMatrixAssignmentExecutesAndReportsCompletion(t *testing.T) {
	temp := t.TempDir()
	shared := filepath.Join(temp, "shared")
	taskDir := filepath.Join(shared, "tasks", "task-1")
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := TaskMeta{
		TaskID:     "task-1",
		ProjectID:  "project-1",
		AssignedTo: "@ecs-worker:matrix.local",
		Status:     "assigned",
		Office: &OfficeTaskMapping{
			WorkOrderID: "office_123", StageID: "scope",
		},
	}
	if err := writeJSONAtomic(filepath.Join(taskDir, "meta.json"), meta); err != nil {
		t.Fatal(err)
	}
	spec := "# Task\n\n完成计算。\n\n## Acceptance Criteria\n- 返回准确结果\n\n## Completion Report\nTASK_COMPLETED: task-1\n"
	if err := os.WriteFile(filepath.Join(taskDir, "spec.md"), []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}

	var localTask TaskRequest
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&localTask); err != nil {
			t.Errorf("decode local task: %v", err)
		}
		_ = json.NewEncoder(w).Encode(TaskResult{
			TaskID:  "task-1",
			Status:  "SUCCESS",
			Message: protocol.Message{Role: "assistant", Content: "答案是 16。"},
		})
	}))
	defer local.Close()

	var reportBody string
	matrix := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/sync"):
			_, _ = io.WriteString(w, `{
				"next_batch":"next-1",
				"rooms":{"join":{"!task:matrix.local":{"timeline":{"events":[{
					"type":"m.room.message",
					"event_id":"$event-1",
					"sender":"@leader:matrix.local",
					"content":{
						"msgtype":"m.text",
						"body":"@ecs-worker:matrix.local TASK_ASSIGNED: task-1 - Please start. Spec: shared/tasks/task-1/spec.md",
						"m.mentions":{"user_ids":["@ecs-worker:matrix.local"]}
					}
				}]}}}}
			}`)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.room.message/"):
			data, _ := io.ReadAll(r.Body)
			reportBody = string(data)
			_, _ = io.WriteString(w, `{"event_id":"$report-1"}`)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer matrix.Close()

	runtimePath := filepath.Join(temp, "runtime.yaml")
	runtimeData := `
member:
  name: ecs-worker
  runtimeName: ecs-worker
  role: worker
  runtime: ecs
  matrixUserId: "@ecs-worker:matrix.local"
matrix:
  accessToken: matrix-token
`
	if err := os.WriteFile(runtimePath, []byte(runtimeData), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Listen: "127.0.0.1:8090",
		AgentTeams: config.AgentTeamsConfig{
			Enabled:            true,
			MatrixURL:          matrix.URL,
			RuntimeConfigFile:  runtimePath,
			TaskDirectory:      shared,
			MatrixSyncTimeout:  config.Duration(time.Second),
			MatrixSinceFile:    filepath.Join(temp, "since"),
			MaxTaskPromptChars: 1000,
		},
		HTTP: config.HTTPConfig{
			MaxBodyBytes: 1 << 20,
			WriteTimeout: config.Duration(5 * time.Second),
		},
	}
	worker, err := NewMatrixWorker(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	worker.localTaskURL = local.URL
	next, err := worker.syncOnce(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "next-1" {
		t.Fatalf("unexpected next batch: %q", next)
	}
	if localTask.TaskID != "task-1" || localTask.ProjectID != "project-1" ||
		len(localTask.AcceptanceCriteria) != 1 || localTask.Office == nil ||
		localTask.Office.WorkOrderID != "office_123" || localTask.Office.StageID != "scope" {
		t.Fatalf("task was not restored correctly: %#v", localTask)
	}
	if strings.Contains(localTask.Objective, "TASK_COMPLETED") {
		t.Fatalf("runtime completion instructions leaked into small-model task: %s", localTask.Objective)
	}
	for _, expected := range []string{
		"@leader:matrix.local",
		"TASK_COMPLETED: task-1",
		"shared/tasks/task-1/result.md",
		"答案是 16",
	} {
		if !strings.Contains(reportBody, expected) {
			t.Fatalf("Matrix report missing %q: %s", expected, reportBody)
		}
	}
}

func TestMatrixIgnoresAssignmentWithoutWorkerMention(t *testing.T) {
	worker := &MatrixWorker{
		selfUserID: "@ecs-worker:matrix.local",
		runtime:    RuntimeConfig{},
	}
	var event matrixEvent
	event.Type = "m.room.message"
	event.Sender = "@leader:matrix.local"
	event.Content.MsgType = "m.text"
	event.Content.Body = "TASK_ASSIGNED: task-1"
	if worker.isMentioned(event) {
		t.Fatal("unmentioned assignment must be ignored")
	}
}

func TestStorePreservesLeaderOwnedTaskSpec(t *testing.T) {
	root := filepath.Join(t.TempDir(), "shared")
	dir := filepath.Join(root, "tasks", "task-existing")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := TaskMeta{
		TaskID:    "task-existing",
		ProjectID: "project-1",
		Status:    "assigned",
		DependsOn: []string{"task-before"},
	}
	if err := writeJSONAtomic(filepath.Join(dir, "meta.json"), meta); err != nil {
		t.Fatal(err)
	}
	var originalDocument map[string]any
	metaData, _ := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err := json.Unmarshal(metaData, &originalDocument); err != nil {
		t.Fatal(err)
	}
	originalDocument["room_id"] = "!leader-owned:matrix.local"
	originalDocument["custom_extension"] = "keep-me"
	if err := writeJSONAtomic(filepath.Join(dir, "meta.json"), originalDocument); err != nil {
		t.Fatal(err)
	}
	const originalSpec = "leader-owned task spec"
	if err := os.WriteFile(filepath.Join(dir, "spec.md"), []byte(originalSpec), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(root)
	updated, err := store.Begin(TaskRequest{TaskID: "task-existing", ProjectID: "project-1", Objective: originalSpec}, "local")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "in_progress" || len(updated.DependsOn) != 1 {
		t.Fatalf("existing metadata was not preserved: %#v", updated)
	}
	data, err := os.ReadFile(filepath.Join(dir, "spec.md"))
	if err != nil || string(data) != originalSpec {
		t.Fatalf("leader-owned spec changed: %v %q", err, data)
	}
	updatedData, _ := os.ReadFile(filepath.Join(dir, "meta.json"))
	var updatedDocument map[string]any
	if err := json.Unmarshal(updatedData, &updatedDocument); err != nil {
		t.Fatal(err)
	}
	if updatedDocument["room_id"] != "!leader-owned:matrix.local" ||
		updatedDocument["custom_extension"] != "keep-me" {
		t.Fatalf("leader-owned metadata fields were lost: %#v", updatedDocument)
	}
	if _, err := store.Complete(
		TaskRequest{TaskID: "task-existing", ProjectID: "project-1", Objective: originalSpec},
		TaskResult{TaskID: "task-existing", Message: protocol.Message{Role: "assistant", Content: "done"}},
	); err != nil {
		t.Fatal(err)
	}
	completedData, _ := os.ReadFile(filepath.Join(dir, "meta.json"))
	var completedDocument map[string]any
	if err := json.Unmarshal(completedData, &completedDocument); err != nil {
		t.Fatal(err)
	}
	if completedDocument["status"] != "submitted" ||
		completedDocument["room_id"] != "!leader-owned:matrix.local" ||
		completedDocument["custom_extension"] != "keep-me" {
		t.Fatalf("leader-owned metadata fields were lost on completion: %#v", completedDocument)
	}
}
