package agentteams

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ecs/internal/config"
)

func TestMapTaskUsesProfileAndBoundsUserPrompt(t *testing.T) {
	task := TaskRequest{
		ProjectID:          "project-1",
		TaskID:             "task-1",
		Objective:          strings.Repeat("分析输入并给出结论。", 100),
		AcceptanceCriteria: []string{"输出结论"},
		Context:            map[string]string{"source": "matrix"},
		Skills:             []string{"text_stats", "clock"},
		Tools:              []string{"text_stats", "clock"},
	}
	request, prompt := MapTask(task, AgentProfile{
		Name:          "worker-a",
		Role:          "analyst",
		Model:         "qwen-small",
		AllowedSkills: []string{"clock"},
		AllowedTools:  []string{"text_stats"},
	}, 600)
	if len([]rune(request.Prompt)) > 600 {
		t.Fatalf("user task exceeds budget: %d", len([]rune(request.Prompt)))
	}
	if request.Model != "qwen-small" || len(request.Tools) != 1 || request.Tools[0] != "text_stats" ||
		len(request.Skills) != 1 || request.Skills[0] != "clock" {
		t.Fatalf("profile or tool allowlist was not mapped: %#v", request)
	}
	if prompt.AgentName != "worker-a" || prompt.TaskID != "task-1" || prompt.ProjectID != "project-1" {
		t.Fatalf("prompt context was not mapped: %#v", prompt)
	}
	if request.Metadata["agentteams.trace_id"] != "task-1" {
		t.Fatalf("missing default trace id: %#v", request.Metadata)
	}
}

func TestStorePersistsAgentTeamsTaskContract(t *testing.T) {
	root := filepath.Join(t.TempDir(), "shared")
	store := NewStore(root)
	task := TaskRequest{
		ProjectID: "project-1",
		TaskID:    "task-1",
		Objective: "计算结果",
		TraceID:   "trace-1",
	}
	meta, err := store.Begin(task, "local")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Status != "in_progress" {
		t.Fatalf("unexpected begin status: %#v", meta)
	}
	result := TaskResult{TaskID: task.TaskID, Status: "SUCCESS"}
	finalMeta, err := store.Complete(task, result)
	if err != nil {
		t.Fatal(err)
	}
	if finalMeta.Status != "submitted" {
		t.Fatalf("unexpected final status: %#v", finalMeta)
	}
	loaded, err := store.LoadResult(task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "SUCCESS" || loaded.Meta == nil || loaded.Meta.Status != "submitted" {
		t.Fatalf("unexpected stored result: %#v", loaded)
	}
	for _, name := range []string{"meta.json", "spec.md", "result.md", "result.json"} {
		if _, err := os.Stat(filepath.Join(root, "tasks", task.TaskID, name)); err != nil {
			t.Fatalf("%s was not stored: %v", name, err)
		}
	}
	resultMarkdown, err := os.ReadFile(filepath.Join(root, "tasks", task.TaskID, "result.md"))
	if err != nil || !strings.Contains(string(resultMarkdown), "STATUS: SUCCESS") {
		t.Fatalf("result contract was not written: %v %s", err, resultMarkdown)
	}
}

func TestStorePersistsOfficeStageMapping(t *testing.T) {
	store := NewStore(t.TempDir())
	task := TaskRequest{TaskID: "task-office", Objective: "完成会议事实提取"}
	if _, err := store.Begin(task, "local"); err != nil {
		t.Fatal(err)
	}
	mapping := OfficeTaskMapping{
		WorkOrderID: "office_123", Component: "meeting_followup",
		StageID: "extract", OfficeTaskID: "office_123.extract", Status: "running",
	}
	if _, err := store.SetOfficeMapping(task.TaskID, mapping); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Complete(task, TaskResult{TaskID: task.TaskID, Office: &mapping}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadResult(task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Meta == nil || loaded.Meta.Office == nil ||
		loaded.Meta.Office.OfficeTaskID != "office_123.extract" {
		t.Fatalf("Office mapping was not persisted: %#v", loaded)
	}
}

func TestStoreRejectsTaskIDOwnedByAnotherPeer(t *testing.T) {
	store := NewStore(t.TempDir())
	task := TaskRequest{TaskID: "shared-task", Objective: "first request"}
	if _, err := store.Begin(task, "peer-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Begin(task, "peer-b"); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("expected owner mismatch, got %v", err)
	}
}

func TestLegacyAgentTeamsTaskIsLocalOnly(t *testing.T) {
	if !OwnedBy(TaskMeta{}, "local") {
		t.Fatal("legacy task should remain available to the local owner")
	}
	if OwnedBy(TaskMeta{}, "peer-a") {
		t.Fatal("legacy task must not be exposed to a remote peer")
	}
}

func TestLoadRuntimeProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.yaml")
	data := []byte(`
member:
  runtimeName: ecs-checker
  role: verifier
  runtime: ecs
desired:
  model:
    model: qwen3:0.6b
  identity: 只检查结果
  soul: 保持简短
  instructions: 不执行未分配任务
  skills: [calculate]
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	profile, _, err := LoadRuntimeProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "ecs-checker" || profile.Role != "verifier" ||
		profile.Model != "qwen3:0.6b" || len(profile.AllowedSkills) != 1 ||
		!strings.Contains(profile.Identity, "保持简短") {
		t.Fatalf("unexpected profile: %#v", profile)
	}
}

func TestReporterPostsReadyAndHeartbeat(t *testing.T) {
	var actions []string
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actions = append(actions, filepath.Base(r.URL.Path))
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil || payload["lastActiveAt"] == "" {
			t.Errorf("invalid report body: %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer controller.Close()
	reporter := NewReporter(config.AgentTeamsConfig{
		Enabled:       true,
		ControllerURL: controller.URL,
		WorkerName:    "ecs-worker",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := reporter.post(context.Background(), "ready"); err != nil {
		t.Fatal(err)
	}
	if err := reporter.post(context.Background(), "heartbeat"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "ready,ready" {
		t.Fatalf("unexpected actions: %#v", actions)
	}
}

func TestTaskRejectsUnsafePathID(t *testing.T) {
	task := TaskRequest{TaskID: "../escape", Objective: "bad"}
	if err := task.Validate(); err == nil {
		t.Fatal("unsafe task id must be rejected")
	}
}
