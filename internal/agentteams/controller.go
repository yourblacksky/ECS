package agentteams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ecs/internal/config"
)

type Reporter struct {
	cfg    config.AgentTeamsConfig
	client *http.Client
	logger *slog.Logger
	token  string
}

func NewReporter(cfg config.AgentTeamsConfig, logger *slog.Logger) *Reporter {
	return &Reporter{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
		logger: logger,
		token:  os.Getenv("AGENTTEAMS_AUTH_TOKEN"),
	}
}

func (r *Reporter) Run(ctx context.Context) {
	if !r.cfg.Enabled || r.cfg.ControllerURL == "" {
		return
	}
	if err := r.post(ctx, "ready"); err != nil {
		r.logger.Warn("AgentTeams ready report failed", "worker", r.cfg.WorkerName, "error", err)
	} else {
		r.logger.Info("AgentTeams worker ready", "worker", r.cfg.WorkerName)
	}
	ticker := time.NewTicker(r.cfg.HeartbeatInterval.Duration())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.post(ctx, "heartbeat"); err != nil {
				r.logger.Warn("AgentTeams heartbeat failed", "worker", r.cfg.WorkerName, "error", err)
			}
		}
	}
}

func (r *Reporter) post(ctx context.Context, action string) error {
	body, _ := json.Marshal(map[string]string{"lastActiveAt": time.Now().UTC().Format(time.RFC3339)})
	// AgentTeams uses the ready endpoint for both the initial readiness report
	// and periodic heartbeats (the agt CLI follows the same contract). Keep the
	// local action name for logs while mapping it to the controller route.
	controllerAction := action
	if action == "heartbeat" {
		controllerAction = "ready"
	}
	endpoint := strings.TrimRight(r.cfg.ControllerURL, "/") +
		"/api/v1/workers/" + url.PathEscape(r.cfg.WorkerName) + "/" + controllerAction
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		request.Header.Set("Authorization", "Bearer "+r.token)
	}
	response, err := r.client.Do(request)
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
