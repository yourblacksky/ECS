package server

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"ecs/internal/agent"
	"ecs/internal/authz"
	"ecs/internal/protocol"
	"ecs/internal/trace"
)

func (s *Server) runDirectTool(
	ctx context.Context,
	bounded authz.Bounded,
) (protocol.ReplyResponse, error) {
	request := bounded.Request()
	var response protocol.ReplyResponse
	if request.DirectTool == nil {
		return response, errors.New("direct_tool is required")
	}
	if s.registry == nil {
		return response, errors.New("tool registry is unavailable")
	}
	manifest, ok := s.registry.Get(request.DirectTool.Name)
	if !ok {
		return response, errors.New("direct tool is not installed or allowed")
	}
	requiresApproval := s.cfg.Agent.ApprovalMode == "all" ||
		(s.cfg.Agent.ApprovalMode == "high_risk" &&
			(manifest.RequiresApproval || manifest.Risk == "high"))
	if requiresApproval && !slices.Contains(request.ApprovedTools, manifest.Name) {
		risk := manifest.Risk
		if risk == "" {
			risk = "low"
		}
		return response, &agent.ApprovalError{
			Tool:        manifest.Name,
			Description: manifest.Description,
			Risk:        risk,
			Arguments:   append(json.RawMessage(nil), request.DirectTool.Arguments...),
		}
	}
	recorder, parentID := trace.FromContext(ctx)
	span := recorder.StartSpan(parentID, "tool", manifest.Name, map[string]any{
		"arguments": request.DirectTool.Arguments,
		"direct":    true,
	})
	started := time.Now()
	output, err := s.registry.Execute(ctx, manifest.Name, request.DirectTool.Arguments)
	run := protocol.ToolRun{
		Name:      manifest.Name,
		Arguments: append(json.RawMessage(nil), request.DirectTool.Arguments...),
		Output:    output,
		Duration:  time.Since(started).Milliseconds(),
	}
	status := "success"
	if err != nil {
		status = "error"
		run.Error = err.Error()
	}
	recorder.FinishSpan(span, status, output, err, map[string]any{
		"duration_ms": run.Duration,
	})
	response.ToolRuns = []protocol.ToolRun{run}
	response.Message = protocol.Message{
		Role:    "assistant",
		Content: strings.TrimSpace(output),
	}
	if response.Message.Content == "" && err == nil {
		response.Message.Content = "null"
	}
	if err == nil {
		response.Verification = &protocol.Verification{
			Verifier: "ecs-kernel", Status: "accepted", Validation: "accepted",
			Evidence: []string{"tool:" + manifest.Name + "=success"},
		}
	} else {
		response.Verification = &protocol.Verification{
			Verifier: "ecs-kernel", Status: "rejected", Validation: "rejected",
			Errors: []string{err.Error()},
		}
	}
	return response, err
}
