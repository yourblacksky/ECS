package agentteams

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Store struct {
	root string
	mu   sync.Mutex
}

var ErrOwnerMismatch = errors.New("AgentTeams task belongs to another owner")

func NewStore(root string) *Store {
	return &Store{root: root}
}

func (s *Store) Begin(task TaskRequest, ownerPeerNodeID string) (TaskMeta, error) {
	if err := task.Validate(); err != nil {
		return TaskMeta{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	dir := s.taskDir(task.TaskID)
	if existing, document, err := s.loadMetaDocumentUnlocked(task.TaskID); err == nil {
		if !OwnedBy(existing, ownerPeerNodeID) {
			return TaskMeta{}, ErrOwnerMismatch
		}
		existing.Status = "in_progress"
		existing.AcknowledgedAt = &now
		existing.TraceID = EffectiveTraceID(task)
		existing.RequestHash = Fingerprint(task)
		document["status"] = existing.Status
		document["acknowledged_at"] = now
		document["trace_id"] = existing.TraceID
		document["request_hash"] = existing.RequestHash
		document["owner_peer_node_id"] = normalizedOwner(ownerPeerNodeID)
		if err := writeJSONAtomic(filepath.Join(dir, "meta.json"), document); err != nil {
			return TaskMeta{}, err
		}
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return TaskMeta{}, err
	}
	meta := TaskMeta{
		TaskID:          task.TaskID,
		ProjectID:       task.ProjectID,
		OwnerPeerNodeID: normalizedOwner(ownerPeerNodeID),
		TaskTitle:       taskTitle(task.Objective),
		AssignedTo:      task.AssignedTo,
		Status:          "in_progress",
		DependsOn:       []string{},
		AssignedAt:      now,
		AcknowledgedAt:  &now,
		SpecPath:        filepath.ToSlash(filepath.Join("shared", "tasks", task.TaskID, "spec.md")),
		ResultPath:      filepath.ToSlash(filepath.Join("shared", "tasks", task.TaskID, "result.md")),
		TraceID:         EffectiveTraceID(task),
		RequestHash:     Fingerprint(task),
	}
	if err := os.MkdirAll(filepath.Join(dir, "workspace"), 0o700); err != nil {
		return TaskMeta{}, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "deliverables"), 0o700); err != nil {
		return TaskMeta{}, err
	}
	if err := writeAtomic(filepath.Join(dir, "spec.md"), []byte(taskSpec(task)), 0o600); err != nil {
		return TaskMeta{}, err
	}
	if err := writeJSONAtomic(filepath.Join(dir, "meta.json"), meta); err != nil {
		return TaskMeta{}, err
	}
	return meta, nil
}

func (s *Store) SetOfficeMapping(taskID string, mapping OfficeTaskMapping) (TaskMeta, error) {
	if !safeID.MatchString(taskID) {
		return TaskMeta{}, errors.New("invalid task id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, document, err := s.loadMetaDocumentUnlocked(taskID)
	if err != nil {
		return TaskMeta{}, err
	}
	meta.Office = &mapping
	document["office"] = mapping
	if err := writeJSONAtomic(filepath.Join(s.taskDir(taskID), "meta.json"), document); err != nil {
		return TaskMeta{}, err
	}
	return meta, nil
}

func (s *Store) SetStatus(taskID, status, message string) (TaskMeta, error) {
	if !safeID.MatchString(taskID) || strings.TrimSpace(status) == "" {
		return TaskMeta{}, errors.New("invalid task status")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, document, err := s.loadMetaDocumentUnlocked(taskID)
	if err != nil {
		return TaskMeta{}, err
	}
	meta.Status = status
	meta.Error = strings.TrimSpace(message)
	document["status"] = meta.Status
	if meta.Error == "" {
		delete(document, "error")
	} else {
		document["error"] = meta.Error
	}
	if err := writeJSONAtomic(filepath.Join(s.taskDir(taskID), "meta.json"), document); err != nil {
		return TaskMeta{}, err
	}
	return meta, nil
}

// OwnedBy treats pre-ownership records as local-only for safe migration.
func OwnedBy(meta TaskMeta, ownerPeerNodeID string) bool {
	owner := meta.OwnerPeerNodeID
	if owner == "" {
		owner = "local"
	}
	return owner == normalizedOwner(ownerPeerNodeID)
}

func normalizedOwner(ownerPeerNodeID string) string {
	ownerPeerNodeID = strings.TrimSpace(ownerPeerNodeID)
	if ownerPeerNodeID == "" {
		return "local"
	}
	return ownerPeerNodeID
}

func (s *Store) Complete(task TaskRequest, result TaskResult) (TaskMeta, error) {
	return s.finish(task, result, "submitted", "SUCCESS", "")
}

func (s *Store) Fail(task TaskRequest, result TaskResult, cause error) (TaskMeta, error) {
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	return s.finish(task, result, "submitted", "BLOCKED", message)
}

func (s *Store) finish(
	task TaskRequest,
	result TaskResult,
	metaStatus string,
	resultStatus string,
	message string,
) (TaskMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, document, err := s.loadMetaDocumentUnlocked(task.TaskID)
	if err != nil {
		return TaskMeta{}, err
	}
	now := time.Now().UTC()
	meta.Status = metaStatus
	meta.SubmittedAt = &now
	meta.Error = message
	document["status"] = metaStatus
	document["submitted_at"] = now
	if message != "" {
		document["error"] = message
	} else {
		delete(document, "error")
	}
	result.Status = resultStatus
	result.Meta = &meta
	dir := s.taskDir(task.TaskID)
	if err := writeAtomic(filepath.Join(dir, "result.md"), []byte(renderResultMarkdown(resultStatus, result)), 0o600); err != nil {
		return TaskMeta{}, err
	}
	if err := writeJSONAtomic(filepath.Join(dir, "result.json"), result); err != nil {
		return TaskMeta{}, err
	}
	if err := writeJSONAtomic(filepath.Join(dir, "meta.json"), document); err != nil {
		return TaskMeta{}, err
	}
	return meta, nil
}

func renderResultMarkdown(status string, result TaskResult) string {
	body := strings.TrimSpace(result.Message.Content)
	if body == "" {
		body = strings.TrimSpace(result.Error)
	}
	if body == "" {
		body = "No result was produced."
	}
	return fmt.Sprintf(
		"STATUS: %s\n\nSUMMARY:\n%s\n\nDELIVERABLES:\n- None\n",
		status,
		body,
	)
}

func (s *Store) LoadResult(taskID string) (TaskResult, error) {
	if !safeID.MatchString(taskID) {
		return TaskResult{}, errors.New("invalid task id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(s.taskDir(taskID), "result.json"))
	if err != nil {
		return TaskResult{}, err
	}
	var result TaskResult
	if err := json.Unmarshal(data, &result); err != nil {
		return TaskResult{}, err
	}
	meta, err := s.loadMetaUnlocked(taskID)
	if err == nil {
		result.Meta = &meta
	}
	return result, nil
}

func (s *Store) loadMetaUnlocked(taskID string) (TaskMeta, error) {
	meta, _, err := s.loadMetaDocumentUnlocked(taskID)
	return meta, err
}

func (s *Store) loadMetaDocumentUnlocked(taskID string) (TaskMeta, map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(s.taskDir(taskID), "meta.json"))
	if err != nil {
		return TaskMeta{}, nil, err
	}
	var meta TaskMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return TaskMeta{}, nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return TaskMeta{}, nil, err
	}
	return meta, document, nil
}

func (s *Store) taskDir(taskID string) string {
	return filepath.Join(s.root, "tasks", taskID)
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(path, data, 0o600)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".ecs-agentteams-*")
	if err != nil {
		return err
	}
	tempName := file.Name()
	defer os.Remove(tempName)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
