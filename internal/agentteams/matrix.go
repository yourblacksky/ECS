package agentteams

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ecs/internal/config"
)

var assignmentPattern = regexp.MustCompile(`(?:^|\s)TASK_ASSIGNED:\s*([A-Za-z0-9][A-Za-z0-9._-]{0,127})(?:\s|$)`)

type MatrixWorker struct {
	cfg          config.Config
	runtime      RuntimeConfig
	matrixURL    string
	token        string
	selfUserID   string
	localTaskURL string
	client       *http.Client
	localClient  *http.Client
	logger       *slog.Logger
}

type matrixSyncResponse struct {
	NextBatch string `json:"next_batch"`
	Rooms     struct {
		Join map[string]struct {
			Timeline struct {
				Events []matrixEvent `json:"events"`
			} `json:"timeline"`
		} `json:"join"`
		Invite map[string]json.RawMessage `json:"invite"`
	} `json:"rooms"`
}

type matrixEvent struct {
	Type    string `json:"type"`
	EventID string `json:"event_id"`
	Sender  string `json:"sender"`
	Content struct {
		MsgType  string `json:"msgtype"`
		Body     string `json:"body"`
		Mentions struct {
			UserIDs []string `json:"user_ids"`
		} `json:"m.mentions"`
	} `json:"content"`
}

func NewMatrixWorker(cfg config.Config, logger *slog.Logger) (*MatrixWorker, error) {
	if !cfg.AgentTeams.Enabled || cfg.AgentTeams.MatrixURL == "" {
		return nil, nil
	}
	_, runtimeConfig, err := LoadRuntimeProfile(cfg.AgentTeams.RuntimeConfigFile)
	if err != nil {
		return nil, err
	}
	tokenEnv := cfg.AgentTeams.MatrixTokenEnv
	if runtimeConfig.Credentials.MatrixTokenEnv != "" {
		tokenEnv = runtimeConfig.Credentials.MatrixTokenEnv
	}
	token := runtimeConfig.Matrix.AccessToken
	if token == "" && tokenEnv != "" {
		token = os.Getenv(tokenEnv)
	}
	if token == "" {
		return nil, fmt.Errorf("Matrix access token is missing; set %s", tokenEnv)
	}
	if runtimeConfig.Member.MatrixUserID == "" {
		return nil, errors.New("runtime config member.matrixUserId is required for Matrix")
	}
	localURL, err := localTaskEndpoint(cfg.Listen)
	if err != nil {
		return nil, err
	}
	timeout := cfg.AgentTeams.MatrixSyncTimeout.Duration() + 15*time.Second
	return &MatrixWorker{
		cfg:          cfg,
		runtime:      runtimeConfig,
		matrixURL:    strings.TrimRight(cfg.AgentTeams.MatrixURL, "/"),
		token:        token,
		selfUserID:   runtimeConfig.Member.MatrixUserID,
		localTaskURL: localURL,
		client:       &http.Client{Timeout: timeout},
		localClient:  &http.Client{Timeout: cfg.HTTP.WriteTimeout.Duration()},
		logger:       logger,
	}, nil
}

func (w *MatrixWorker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	since := w.loadSince()
	backoff := time.Second
	for {
		next, err := w.syncOnce(ctx, since)
		if err == nil {
			if next != "" {
				since = next
				if writeErr := writeAtomic(w.cfg.AgentTeams.MatrixSinceFile, []byte(next+"\n"), 0o600); writeErr != nil {
					w.logger.Warn("Matrix sync position could not be saved", "error", writeErr)
				}
			}
			backoff = time.Second
			continue
		}
		if ctx.Err() != nil {
			return
		}
		w.logger.Warn("Matrix sync failed", "error", err, "retry_in", backoff.String())
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (w *MatrixWorker) syncOnce(ctx context.Context, since string) (string, error) {
	response, err := w.sync(ctx, since)
	if err != nil {
		return "", err
	}
	for roomID := range response.Rooms.Invite {
		if err := w.joinRoom(ctx, roomID); err != nil {
			return "", fmt.Errorf("join Matrix room %s: %w", roomID, err)
		}
	}
	for roomID, room := range response.Rooms.Join {
		for _, event := range room.Timeline.Events {
			if err := w.handleEvent(ctx, roomID, event); err != nil {
				return "", err
			}
		}
	}
	return response.NextBatch, nil
}

func (w *MatrixWorker) sync(ctx context.Context, since string) (matrixSyncResponse, error) {
	query := url.Values{}
	query.Set("timeout", strconv.FormatInt(w.cfg.AgentTeams.MatrixSyncTimeout.Duration().Milliseconds(), 10))
	if since != "" {
		query.Set("since", since)
	}
	endpoint := w.matrixURL + "/_matrix/client/v3/sync?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return matrixSyncResponse{}, err
	}
	request.Header.Set("Authorization", "Bearer "+w.token)
	response, err := w.client.Do(request)
	if err != nil {
		return matrixSyncResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return matrixSyncResponse{}, fmt.Errorf("Matrix sync %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	var payload matrixSyncResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&payload); err != nil {
		return matrixSyncResponse{}, err
	}
	return payload, nil
}

func (w *MatrixWorker) handleEvent(ctx context.Context, roomID string, event matrixEvent) error {
	if event.Type != "m.room.message" || event.Content.MsgType != "m.text" ||
		event.Sender == "" || event.Sender == w.selfUserID {
		return nil
	}
	taskID, ok := ParseAssignment(event.Content.Body)
	if !ok || !w.isMentioned(event) {
		return nil
	}
	task, err := w.loadAssignedTask(taskID, event.Sender, roomID)
	if err != nil {
		return w.sendTaskReport(ctx, roomID, event.Sender, taskID, "BLOCKED", err.Error(), event.EventID)
	}
	result, statusCode, err := w.executeLocalTask(ctx, task)
	if err != nil {
		return err
	}
	status := result.Status
	if status == "" {
		status = "SUCCESS"
	}
	if statusCode >= 500 && status != "BLOCKED" {
		return fmt.Errorf("local AgentTeams task returned HTTP %d", statusCode)
	}
	return w.sendTaskReport(
		ctx,
		roomID,
		event.Sender,
		taskID,
		status,
		result.Message.Content,
		event.EventID,
	)
}

func ParseAssignment(body string) (string, bool) {
	match := assignmentPattern.FindStringSubmatch(body)
	if len(match) != 2 || !safeID.MatchString(match[1]) {
		return "", false
	}
	return match[1], true
}

func (w *MatrixWorker) isMentioned(event matrixEvent) bool {
	for _, userID := range event.Content.Mentions.UserIDs {
		if userID == w.selfUserID {
			return true
		}
	}
	return strings.Contains(event.Content.Body, w.selfUserID) ||
		(w.runtime.Member.RuntimeName != "" &&
			strings.Contains(event.Content.Body, w.runtime.Member.RuntimeName))
}

func (w *MatrixWorker) loadAssignedTask(taskID string, sender string, roomID string) (TaskRequest, error) {
	dir := filepath.Join(w.cfg.AgentTeams.TaskDirectory, "tasks", taskID)
	metaData, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return TaskRequest{}, fmt.Errorf("read task metadata: %w", err)
	}
	var meta TaskMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return TaskRequest{}, fmt.Errorf("decode task metadata: %w", err)
	}
	if meta.TaskID != "" && meta.TaskID != taskID {
		return TaskRequest{}, errors.New("task metadata id does not match assignment")
	}
	if meta.AssignedTo != "" && !w.isAssignedToSelf(meta.AssignedTo) {
		return TaskRequest{}, fmt.Errorf("task is assigned to %s", meta.AssignedTo)
	}
	spec, err := os.ReadFile(filepath.Join(dir, "spec.md"))
	if err != nil {
		return TaskRequest{}, fmt.Errorf("read task spec: %w", err)
	}
	fullSpec := strings.TrimSpace(string(spec))
	objective := compactTaskSpec(fullSpec)
	if len([]rune(objective)) > 12000 {
		objective = string([]rune(objective)[:12000])
	}
	task := TaskRequest{
		ProjectID:          meta.ProjectID,
		TaskID:             taskID,
		AssignedBy:         sender,
		AssignedTo:         meta.AssignedTo,
		Objective:          objective,
		AcceptanceCriteria: extractAcceptanceCriteria(fullSpec),
		Context: map[string]string{
			"source":          "matrix",
			"assignment_by":   sender,
			"assignment_room": roomID,
		},
		TraceID: taskID,
	}
	if meta.Office != nil {
		task.Office = &OfficeTaskRef{
			WorkOrderID: meta.Office.WorkOrderID,
			StageID:     meta.Office.StageID,
		}
	}
	return task, nil
}

func compactTaskSpec(spec string) string {
	lines := strings.Split(spec, "\n")
	result := make([]string, 0, len(lines))
	skipSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			if strings.Contains(heading, "completion report") ||
				strings.Contains(heading, "post-action") ||
				strings.Contains(heading, "完成回报") {
				skipSection = true
				continue
			}
			skipSection = false
		}
		if skipSection || strings.Contains(trimmed, "TASK_COMPLETED:") ||
			strings.Contains(trimmed, "taskflow submit_task") {
			continue
		}
		result = append(result, line)
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

func (w *MatrixWorker) isAssignedToSelf(assigned string) bool {
	return assigned == w.selfUserID ||
		assigned == w.runtime.Member.Name ||
		assigned == w.runtime.Member.RuntimeName
}

func extractAcceptanceCriteria(spec string) []string {
	lines := strings.Split(spec, "\n")
	var result []string
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			inSection = strings.Contains(heading, "acceptance") || strings.Contains(heading, "验收")
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "- ") {
			result = append(result, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			if len(result) == 5 {
				break
			}
		}
	}
	return result
}

func (w *MatrixWorker) executeLocalTask(ctx context.Context, task TaskRequest) (TaskResult, int, error) {
	body, err := json.Marshal(task)
	if err != nil {
		return TaskResult{}, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.localTaskURL, bytes.NewReader(body))
	if err != nil {
		return TaskResult{}, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	if w.cfg.AuthToken != "" {
		request.Header.Set("Authorization", "Bearer "+w.cfg.AuthToken)
	}
	response, err := w.localClient.Do(request)
	if err != nil {
		return TaskResult{}, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, w.cfg.HTTP.MaxBodyBytes))
	if err != nil {
		return TaskResult{}, response.StatusCode, err
	}
	var result TaskResult
	if err := json.Unmarshal(data, &result); err != nil {
		return TaskResult{}, response.StatusCode, fmt.Errorf("decode local task result: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if result.Status == "BLOCKED" {
			return result, response.StatusCode, nil
		}
		return result, response.StatusCode, fmt.Errorf("local task HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	return result, response.StatusCode, nil
}

func (w *MatrixWorker) sendTaskReport(
	ctx context.Context,
	roomID string,
	leaderUserID string,
	taskID string,
	status string,
	summary string,
	eventID string,
) error {
	marker := "TASK_COMPLETED"
	if status == "PENDING_APPROVAL" {
		marker = "TASK_PENDING_APPROVAL"
	} else if status == "BLOCKED" || status == "REVISION_NEEDED" {
		marker = "TASK_BLOCKED"
	}
	summary = strings.Join(strings.Fields(summary), " ")
	if len([]rune(summary)) > 300 {
		summary = string([]rune(summary)[:299]) + "…"
	}
	body := fmt.Sprintf("%s %s: %s", leaderUserID, marker, taskID)
	if marker == "TASK_COMPLETED" {
		body += fmt.Sprintf(" - Result: shared/tasks/%s/result.md", taskID)
	}
	if summary != "" {
		body += "\n" + summary
	}
	payload := map[string]any{
		"msgtype": "m.text",
		"body":    body,
		"m.mentions": map[string]any{
			"user_ids": []string{leaderUserID},
		},
	}
	data, _ := json.Marshal(payload)
	transactionID := "ecs-" + taskID + "-" + compactEventID(eventID)
	endpoint := w.matrixURL + "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) +
		"/send/m.room.message/" + url.PathEscape(transactionID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+w.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := w.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Matrix report %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	w.logger.Info("AgentTeams Matrix task reported", "task_id", taskID, "status", status, "room_id", roomID)
	return nil
}

func (w *MatrixWorker) joinRoom(ctx context.Context, roomID string) error {
	endpoint := w.matrixURL + "/_matrix/client/v3/join/" + url.PathEscape(roomID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+w.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := w.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

func (w *MatrixWorker) loadSince() string {
	data, err := os.ReadFile(w.cfg.AgentTeams.MatrixSinceFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func localTaskEndpoint(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("derive local AgentTeams endpoint: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/v1/agentteams/tasks", nil
}

func compactEventID(value string) string {
	value = strings.TrimPrefix(value, "$")
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' {
			builder.WriteRune(char)
		}
		if builder.Len() >= 48 {
			break
		}
	}
	if builder.Len() == 0 {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return builder.String()
}
