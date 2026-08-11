package server

import (
	"context"
	"ecs/internal/config"
	"ecs/internal/office"
	"ecs/internal/protocol"
	"ecs/internal/trace"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

func (s *Server) dispatchOfficeWorkOrder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes)
	defer r.Body.Close()
	var request officeDispatchRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid office dispatch request: "+err.Error())
		return
	}
	request.Group = strings.TrimSpace(request.Group)
	request.Node = strings.TrimSpace(request.Node)
	if request.Group == "" {
		writeError(w, http.StatusBadRequest, "group is required")
		return
	}
	order, err := s.office.LoadOrder(r.PathValue("order"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "office work order not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !office.OwnedBy(order, requestOwner(r.Context())) {
		writeError(w, http.StatusNotFound, "office work order not found")
		return
	}
	if order.Status != "planned" && order.Status != "running" &&
		order.Status != "pending_approval" && order.Status != office.StatusRecoveryRequired {
		writeError(w, http.StatusConflict, "work order cannot be dispatched from status "+order.Status)
		return
	}
	peers := s.groupPeersFor(request.Group, protocol.ReplyRequest{})
	if request.Node != "" {
		peers = slices.DeleteFunc(peers, func(peer config.Peer) bool {
			return peer.Name != request.Node && peer.NodeID != request.Node
		})
	}
	type officeCandidate struct {
		peer         config.Peer
		tools        map[string][]string
		capabilities protocol.NodeCapabilities
	}
	candidates := make([]officeCandidate, 0, len(peers))
	requiredSkills := officeWorkOrderSkills(order)
	for _, peer := range peers {
		capabilities, capabilityErr := s.officePeerCapabilities(r.Context(), peer)
		if capabilityErr != nil {
			s.logger.Warn("office peer capabilities unavailable", "peer", peer.Name, "error", capabilityErr)
			continue
		}
		toolMap, missing := connectorTools(capabilities, order.RequiredConnectors)
		if len(missing) == 0 && officeNodeHasSkills(capabilities, requiredSkills) {
			candidates = append(candidates, officeCandidate{
				peer: peer, tools: toolMap, capabilities: capabilities,
			})
		}
	}
	if len(candidates) == 0 {
		writeError(w, http.StatusConflict, "no peer in group provides all required office connectors")
		return
	}
	candidatePeers := make([]config.Peer, 0, len(candidates))
	for _, candidate := range candidates {
		candidatePeers = append(candidatePeers, candidate.peer)
	}
	selected := s.peerScheduler.selectAndReserve(candidatePeers, dispatchSingle, 1)
	if len(selected) == 0 {
		writeError(w, http.StatusServiceUnavailable, "no office worker available")
		return
	}
	peer := selected[0]
	var toolMap map[string][]string
	var peerCapabilities protocol.NodeCapabilities
	for _, candidate := range candidates {
		if peerKey(candidate.peer) == peerKey(peer) {
			toolMap = candidate.tools
			peerCapabilities = candidate.capabilities
			break
		}
	}
	started := time.Now()
	success := false
	defer func() { s.peerScheduler.complete(peer, time.Since(started), success, true) }()

	executionTraceID := trace.NewID()
	recorder := s.traces.StartOwned(
		requestOwner(r.Context()),
		executionTraceID, order.ID, s.cfg.NodeName,
		"/v1/office/work-orders/{order}/dispatch",
		map[string]any{"work_order": order.ID, "component": order.Component, "group": request.Group, "peer": peer.Name},
	)
	if recorder.TraceID() != "" {
		executionTraceID = recorder.TraceID()
	}
	order, err = s.office.UpdateOrder(order.ID, func(current *office.WorkOrder) error {
		if current.Status != "planned" && current.Status != "running" &&
			current.Status != "pending_approval" && current.Status != office.StatusRecoveryRequired {
			return errors.New("work order cannot continue from status " + current.Status)
		}
		current.Status = "running"
		current.ExecutionTraceID = executionTraceID
		current.Ready = true
		current.MissingConnectors = nil
		current.Audit = append(current.Audit, office.AuditEvent{
			At: time.Now().UTC(), Action: "work_order.dispatched", Actor: s.cfg.NodeName,
			Details: map[string]any{
				"group": request.Group, "peer": peer.Name,
				"connectors": current.RequiredConnectors,
			},
		})
		return nil
	})
	if err != nil {
		recorder.Finish("error", "office dispatch rejected", err)
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	for taskIndex := range order.Tasks {
		task := order.Tasks[taskIndex]
		if task.Status == "completed" {
			continue
		}
		taskTools := toolsForConnectors(toolMap, task.Connectors)
		taskTools = appendUniqueSorted(taskTools, officeSkillTools(peerCapabilities, task.Skills)...)
		var approvalRecord *office.ApprovalRecord
		if office.RequiresApproval(task.Approval) {
			var approved bool
			var record office.ApprovalRecord
			order, record, approved, err = s.office.EnsureTaskApproval(
				order.ID, task.ID, s.cfg.NodeName,
				task.Skills, taskTools, task.Connectors, time.Now().UTC(),
			)
			if err != nil {
				status := http.StatusConflict
				if errors.Is(err, os.ErrNotExist) {
					status = http.StatusNotFound
				}
				recorder.Finish("rejected", "Office approval gate rejected dispatch", err)
				writeError(w, status, err.Error())
				return
			}
			if !approved {
				recorder.Event(recorder.RootID(), "approval", task.Name, "pending_approval", map[string]any{
					"task_id": task.ID, "approval_id": record.ID,
					"policy_version": record.PolicyVersion, "expires_at": record.ExpiresAt,
				})
				recorder.Finish("pending_approval", "Office task "+task.Name+" awaits approval", nil)
				success = true
				writeJSON(w, http.StatusAccepted, order)
				return
			}
			approvalRecord = &record
			task = order.Tasks[taskIndex]
		}
		taskTraceID := trace.NewID()
		taskStarted := time.Now().UTC()
		idempotencyKey := officeTaskIdempotencyKey(order.ID, task.ID)
		var attempt office.TaskAttempt
		order, attempt, _, err = s.office.ClaimTask(order.ID, task.ID, office.TaskClaim{
			Node: peer.Name, TraceID: taskTraceID, IdempotencyKey: idempotencyKey,
			Skills: task.Skills, Tools: taskTools, Connectors: task.Connectors,
			Now: taskStarted,
		})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, office.ErrTaskAlreadyClaimed) {
				status = http.StatusConflict
			}
			recorder.Finish("error", "cannot claim Office task", err)
			writeError(w, status, err.Error())
			return
		}
		task = order.Tasks[taskIndex]
		taskRequest := officeTaskExecutionRequest{
			RequestID: attempt.ID, TraceID: taskTraceID,
			WorkOrder: order.ID, Component: order.Component,
			TaskID: task.ID, TaskName: task.Name, Agent: task.Agent, AgentRole: task.AgentRole,
			Prompt: officeTaskPrompt(order, task), Skills: append([]string(nil), task.Skills...),
			Tools: taskTools, Connectors: append([]string(nil), task.Connectors...),
			Approval:           approvalRecord,
			IdempotencyKey:     idempotencyKey,
			AcceptanceCriteria: append([]string(nil), task.AcceptanceCriteria...),
			Context:            order.Context,
		}
		taskSpan := recorder.StartSpan(recorder.RootID(), "office_task", task.Name, map[string]any{
			"task_id": task.ID, "attempt_id": attempt.ID,
			"attempt": attempt.Number, "agent": task.Agent, "node": peer.Name,
			"skills": task.Skills, "tools": taskTools, "connectors": task.Connectors,
			"approval": approvalRecord,
		})
		order, err = s.office.MarkAttemptDispatched(
			order.ID, task.ID, attempt.ID, time.Now().UTC(),
		)
		if err != nil {
			recorder.FinishSpan(taskSpan, "error", nil, err, nil)
			recorder.Finish("error", "cannot persist Office outbox dispatch", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		traceContext := trace.WithRecorder(r.Context(), recorder, taskSpan)
		stopLease := s.maintainOfficeAttemptLease(
			traceContext, order.ID, task.ID, attempt.ID,
		)
		result := s.callOfficePeer(traceContext, peer, taskRequest)
		stopLease()
		completedAt := time.Now().UTC()
		if result.Error != "" || result.Response == nil {
			message := result.Error
			if message == "" {
				message = "office worker returned an empty response"
			}
			taskErr := errors.New(message)
			order, err = s.office.MarkAttemptRecoveryRequired(
				order.ID, task.ID, attempt.ID, taskErr.Error(), completedAt,
			)
			if err != nil {
				recorder.FinishSpan(taskSpan, "error", result, err, nil)
				recorder.Finish("error", "cannot persist unknown Office outcome", err)
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			recorder.FinishSpan(taskSpan, "error", result, taskErr, nil)
			recorder.Finish(
				office.StatusRecoveryRequired,
				"Office task outcome is unknown and requires recovery: "+task.Name,
				taskErr,
			)
			writeJSON(w, http.StatusAccepted, order)
			return
		}
		receipts, receiptVerification, receiptErr := s.validateOfficeReceipts(
			taskRequest, task, *result.Response, completedAt,
		)
		if receiptErr != nil {
			taskErr := fmt.Errorf("receipt verification failed: %w", receiptErr)
			status := officeFailureStatus(task.OnFailure)
			var compensationRecord *office.CompensationRecord
			if task.OnFailure == "compensate" {
				value := s.callOfficeCompensation(traceContext, peer, order, task, toolMap)
				compensationRecord = &value
				status = value.Status
			}
			order, err = s.office.CompleteTaskAttempt(
				order.ID, task.ID, attempt.ID, office.TaskAttemptResult{
					Status: status, Error: taskErr.Error(),
					CompensationRecord: compensationRecord,
					Actor:              peer.Name, CompletedAt: completedAt,
				},
			)
			if err != nil {
				recorder.FinishSpan(taskSpan, "error", result.Response, err, nil)
				recorder.Finish("error", "cannot persist rejected Office receipt", err)
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			recorder.FinishSpan(taskSpan, "error", result.Response, taskErr, nil)
			recorder.Finish(status, "Office receipt rejected: "+task.Name, taskErr)
			writeJSON(w, http.StatusOK, order)
			return
		}
		encodedResult, _ := json.Marshal(result.Response)
		order, err = s.office.CompleteTaskAttempt(
			order.ID, task.ID, attempt.ID, office.TaskAttemptResult{
				Status: "completed", Result: encodedResult,
				Receipts: receipts, ReceiptVerification: receiptVerification,
				Actor: peer.Name, CompletedAt: completedAt,
			},
		)
		if err != nil {
			recorder.FinishSpan(taskSpan, "error", result, err, nil)
			recorder.Finish("error", "cannot persist task result", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		recorder.FinishSpan(taskSpan, "success", result.Response, nil, nil)
	}
	order, err = s.office.UpdateOrder(order.ID, func(current *office.WorkOrder) error {
		current.Status = "completed"
		current.Audit = append(current.Audit, office.AuditEvent{
			At: time.Now().UTC(), Action: "work_order.completed", Actor: peer.Name,
			Details: map[string]any{"tasks": len(current.Tasks)},
		})
		return nil
	})
	if err != nil {
		recorder.Finish("error", "cannot complete work order", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	success = true
	recorder.Finish("completed", "office work order "+order.ID+" completed by "+peer.Name, nil)
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) officePeerCapabilities(ctx context.Context, peer config.Peer) (protocol.NodeCapabilities, error) {
	for _, capabilities := range s.discovery.Snapshot() {
		if (peer.NodeID != "" && capabilities.NodeID == peer.NodeID) ||
			strings.TrimRight(capabilities.URL, "/") == strings.TrimRight(peer.URL, "/") {
			return capabilities, nil
		}
	}
	endpoint := strings.TrimRight(peer.URL, "/") + "/v1/capabilities"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return protocol.NodeCapabilities{}, err
	}
	if peer.Token != "" {
		request.Header.Set("Authorization", "Bearer "+peer.Token)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return protocol.NodeCapabilities{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		diagnostic, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return protocol.NodeCapabilities{}, fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(diagnostic)))
	}
	var capabilities protocol.NodeCapabilities
	if err := json.NewDecoder(io.LimitReader(response.Body, s.cfg.HTTP.MaxBodyBytes)).Decode(&capabilities); err != nil {
		return protocol.NodeCapabilities{}, err
	}
	return capabilities, nil
}
