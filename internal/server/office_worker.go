package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"ecs/internal/agent"
	"ecs/internal/config"
	"ecs/internal/networkauth"
	"ecs/internal/office"
	"ecs/internal/protocol"
	"ecs/internal/trace"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

func (s *Server) executeOfficeCompensation(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes)
	defer r.Body.Close()
	var request officeCompensationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid Office compensation request: "+err.Error())
		return
	}
	manifest, ok := s.registry.Get(request.Tool)
	if !ok || !slices.Contains(manifest.Capabilities, request.Connector) ||
		!strings.Contains(request.Tool, "compensat") {
		writeError(w, http.StatusForbidden, "compensation tool does not match the declared connector")
		return
	}
	argumentValues := map[string]any{
		"action": request.Action, "work_order": request.WorkOrder,
		"task_id": request.TaskID, "idempotency_key": request.IdempotencyKey,
	}
	if len(request.Receipts) > 0 {
		argumentValues["receipts"] = request.Receipts
	}
	arguments, _ := json.Marshal(argumentValues)
	output, err := s.registry.Execute(r.Context(), request.Tool, arguments)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	var value struct {
		Receipts []struct {
			ReceiptID         string `json:"receipt_id"`
			Action            string `json:"action"`
			Target            string `json:"target"`
			Result            string `json:"result"`
			ExternalReference string `json:"external_reference"`
			IdempotencyKey    string `json:"idempotency_key"`
			CreatedAt         string `json:"created_at"`
		} `json:"receipts"`
	}
	if err := json.Unmarshal([]byte(output), &value); err != nil || len(value.Receipts) == 0 {
		writeError(w, http.StatusBadGateway, "compensation connector returned no receipt")
		return
	}
	now := time.Now().UTC()
	response := officeCompensationResponse{Status: "compensated", Tool: request.Tool}
	for _, receipt := range value.Receipts {
		createdAt, parseErr := time.Parse(time.RFC3339Nano, receipt.CreatedAt)
		if parseErr != nil || receipt.Result != "compensated" ||
			!strings.HasPrefix(receipt.IdempotencyKey, request.IdempotencyKey) {
			writeError(w, http.StatusBadGateway, "compensation connector returned an invalid receipt")
			return
		}
		response.Receipts = append(response.Receipts, office.ExecutionReceipt{
			ReceiptID: receipt.ReceiptID, Action: receipt.Action, Target: receipt.Target,
			Result: receipt.Result, ExternalReference: receipt.ExternalReference,
			IdempotencyKey: receipt.IdempotencyKey, CreatedAt: createdAt.UTC(),
			Connector: request.Connector, Tool: request.Tool, VerifiedAt: now,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) executeOfficeTask(w http.ResponseWriter, r *http.Request) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "node is busy")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes)
	defer r.Body.Close()
	var request officeTaskExecutionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid office task: "+err.Error())
		return
	}
	if strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.WorkOrder) == "" ||
		strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "request_id, work_order, task_id, and prompt are required")
		return
	}
	if request.Approval == nil && request.ApprovalGrant != nil {
		writeError(w, http.StatusForbidden, "Office execution grant has no approval record")
		return
	}
	if request.Approval != nil {
		expectedIssuer := ""
		if request.ApprovalGrant != nil {
			expectedIssuer = request.ApprovalGrant.Issuer
		}
		if relationship, ok := r.Context().Value(networkRelationshipKey{}).(networkauth.PublicRelationship); ok {
			if relationship.Role != networkauth.RoleController {
				writeError(w, http.StatusForbidden, "only an authenticated controller may grant Office execution approval")
				return
			}
			expectedIssuer = relationship.PeerNodeID
		}
		if err := verifyOfficeExecutionGrant(
			bearerToken(r), expectedIssuer, s.nodeIdentity(), s.cfg.NodeName, request,
		); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	principal := requestOwner(r.Context())
	requestKey := principal + "\x00" + request.RequestID
	fingerprintData, _ := json.Marshal(request)
	entry, owner, mismatch := s.beginRequest(requestKey, sha256.Sum256(fingerprintData))
	if mismatch {
		writeError(w, http.StatusConflict, "request_id was already used with a different office task")
		return
	}
	if !owner {
		select {
		case <-r.Context().Done():
			writeError(w, http.StatusRequestTimeout, r.Context().Err().Error())
		case <-entry.done:
			if entry.response == nil {
				writeError(w, http.StatusServiceUnavailable, "previous office task attempt failed")
				return
			}
			writeJSON(w, http.StatusOK, *entry.response)
		}
		return
	}
	completed := false
	defer func() {
		if !completed {
			s.finishRequest(requestKey, entry, nil)
		}
	}()
	if !trace.IsValidID(request.TraceID) {
		request.TraceID = trace.NewID()
	}
	recorder := s.traces.StartOwned(principal, request.TraceID, request.RequestID, s.cfg.NodeName, "/v1/office/tasks/execute", map[string]any{
		"work_order": request.WorkOrder, "component": request.Component,
		"task": request.TaskID, "agent": request.Agent,
		"skills": request.Skills, "tools": request.Tools, "connectors": request.Connectors,
	})
	if recorder.TraceID() != "" {
		request.TraceID = recorder.TraceID()
	}
	var approvedCapabilities []string
	if request.Approval != nil {
		if err := office.ValidateApprovalRecord(
			request.Approval, request.Skills, request.Tools, request.Connectors, time.Now().UTC(),
		); err != nil {
			recorder.Finish("rejected", "Office approval grant rejected", err)
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		approvedCapabilities = appendUniqueSorted(
			append([]string(nil), request.Skills...),
			request.Tools...,
		)
	}
	replyRequest := protocol.ReplyRequest{
		RequestID: request.RequestID, TraceID: request.TraceID,
		Prompt:         request.Prompt,
		Agent:          request.Agent,
		Skills:         append([]string(nil), request.Skills...),
		Tools:          append([]string(nil), request.Tools...),
		RequiredSkills: append([]string(nil), request.Skills...),
		ApprovedTools:  approvedCapabilities,
		RestrictTools:  true, RestrictSkills: true,
		Metadata: map[string]string{
			"office_work_order": request.WorkOrder,
			"office_component":  request.Component,
			"office_task":       request.TaskID,
			"office_skills":     strings.Join(request.Skills, ","),
			"office_connectors": strings.Join(request.Connectors, ","),
		},
	}
	if request.Approval != nil {
		replyRequest.Metadata["office_approval_id"] = request.Approval.ID
		replyRequest.Metadata["office_approval_policy_version"] = request.Approval.PolicyVersion
	}
	bounded, err := boundApprovedRequest(r.Context(), replyRequest, approvedCapabilities)
	if err != nil {
		recorder.Finish("rejected", "", err)
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	traceContext := trace.WithRecorder(r.Context(), recorder, recorder.RootID())
	response, err := s.agent.ReplyWithContext(traceContext, bounded, agent.PromptContext{
		AgentName: request.Agent, Role: request.AgentRole, TaskID: request.TaskID,
		Objective: request.Prompt, AcceptanceCriteria: request.AcceptanceCriteria,
		Context: request.Context,
	})
	if err != nil {
		recorder.Finish("error", "", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": err.Error(), "request_id": request.RequestID, "trace_id": request.TraceID,
			"node": s.cfg.NodeName, "message": response.Message,
			"tool_runs": response.ToolRuns, "skill_runs": response.SkillRuns,
		})
		return
	}
	if err := s.validateOfficeExecution(request, response); err != nil {
		recorder.Finish("error", "", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": err.Error(), "request_id": request.RequestID, "trace_id": request.TraceID,
			"node": s.cfg.NodeName, "message": response.Message,
			"tool_runs": response.ToolRuns, "skill_runs": response.SkillRuns,
		})
		return
	}
	response.RequestID = request.RequestID
	response.TraceID = request.TraceID
	response.Node = s.cfg.NodeName
	completed = true
	s.finishRequest(requestKey, entry, &response)
	recorder.Finish("success", response.Message.Content, nil)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) callOfficePeer(
	ctx context.Context,
	peer config.Peer,
	request officeTaskExecutionRequest,
) protocol.PeerResult {
	result := protocol.PeerResult{Peer: peer.Name}
	if request.Approval != nil && peer.Token != "" {
		subject := peer.NodeID
		if subject == "" {
			subject = peer.Name
		}
		grant, err := signOfficeExecutionGrant(peer.Token, s.nodeIdentity(), subject, request)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		request.ApprovalGrant = grant
	}
	body, err := json.Marshal(request)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	recorder, parentID := trace.FromContext(ctx)
	for attempt := 0; attempt <= s.cfg.HTTP.PeerRetries; attempt++ {
		span := recorder.StartSpan(parentID, "network_attempt", "office-task", map[string]any{
			"attempt": attempt + 1, "node": peer.Name, "task": request.TaskID,
		})
		endpoint := strings.TrimRight(peer.URL, "/") + "/v1/office/tasks/execute"
		httpRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if requestErr != nil {
			result.Error = requestErr.Error()
			recorder.FinishSpan(span, "error", result, requestErr, nil)
			return result
		}
		httpRequest.Header.Set("Content-Type", "application/json")
		if peer.Token != "" {
			httpRequest.Header.Set("Authorization", "Bearer "+peer.Token)
		}
		response, requestErr := s.client.Do(httpRequest)
		retry := false
		if requestErr != nil {
			result.Error = requestErr.Error()
			retry = true
		} else {
			responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, s.cfg.HTTP.MaxBodyBytes))
			_ = response.Body.Close()
			if readErr != nil {
				result.Error = readErr.Error()
				retry = true
			} else if response.StatusCode < 200 || response.StatusCode >= 300 {
				result.Error = fmt.Sprintf("%s: %s", response.Status, strings.TrimSpace(string(responseBody)))
				retry = response.StatusCode >= 500
			} else {
				var reply protocol.ReplyResponse
				if decodeErr := json.Unmarshal(responseBody, &reply); decodeErr != nil {
					result.Error = decodeErr.Error()
				} else {
					result.Response = &reply
					result.Error = ""
				}
			}
		}
		var spanErr error
		status := "success"
		if result.Error != "" {
			spanErr = errors.New(result.Error)
			status = "error"
		}
		recorder.FinishSpan(span, status, result, spanErr, map[string]any{"will_retry": retry})
		if !retry || attempt == s.cfg.HTTP.PeerRetries {
			return result
		}
		timer := time.NewTimer(s.cfg.HTTP.RetryBackoff.Duration() * time.Duration(attempt+1))
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Error = ctx.Err().Error()
			return result
		case <-timer.C:
		}
	}
	return result
}

func (s *Server) callOfficeCompensation(
	ctx context.Context,
	peer config.Peer,
	order office.WorkOrder,
	task office.Task,
	toolMap map[string][]string,
) office.CompensationRecord {
	now := time.Now().UTC()
	record := office.CompensationRecord{
		Status: "compensation_failed", RequestedAt: now,
	}
	if task.Compensation == nil {
		record.Error = "Office stage has no compensation contract"
		return record
	}
	record.Action = task.Compensation.Action
	record.Connector = task.Compensation.Connector
	for _, name := range toolMap[record.Connector] {
		if strings.Contains(strings.ToLower(name), "compensat") {
			record.Tool = name
			break
		}
	}
	if record.Tool == "" {
		record.Error = "no compensation tool provides connector " + record.Connector
		return record
	}
	request := officeCompensationRequest{
		WorkOrder: order.ID, TaskID: task.ID,
		Action: record.Action, Connector: record.Connector, Tool: record.Tool,
		IdempotencyKey: officeTaskIdempotencyKey(order.ID, task.ID) + ":compensate",
		Receipts:       append([]office.ExecutionReceipt(nil), task.Receipts...),
	}
	body, _ := json.Marshal(request)
	endpoint := strings.TrimRight(peer.URL, "/") + "/v1/office/tasks/compensate"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		record.Error = err.Error()
		return record
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if peer.Token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+peer.Token)
	}
	response, err := s.client.Do(httpRequest)
	if err != nil {
		record.Error = err.Error()
		return record
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, s.cfg.HTTP.MaxBodyBytes))
	if err != nil {
		record.Error = err.Error()
		return record
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		record.Error = fmt.Sprintf("%s: %s", response.Status, strings.TrimSpace(string(data)))
		return record
	}
	var value officeCompensationResponse
	if err := json.Unmarshal(data, &value); err != nil || value.Status != "compensated" ||
		len(value.Receipts) == 0 {
		record.Error = "invalid compensation response"
		return record
	}
	completedAt := time.Now().UTC()
	record.Status = "compensated"
	record.CompletedAt = &completedAt
	record.Receipts = value.Receipts
	return record
}

func (s *Server) executeLocalOfficeCompensation(
	ctx context.Context,
	order office.WorkOrder,
	task office.Task,
) office.CompensationRecord {
	now := time.Now().UTC()
	record := office.CompensationRecord{Status: "compensation_failed", RequestedAt: now}
	if task.Compensation == nil {
		record.Error = "Office stage has no compensation contract"
		return record
	}
	record.Action = task.Compensation.Action
	record.Connector = task.Compensation.Connector
	for _, manifest := range s.registry.Available() {
		if strings.Contains(strings.ToLower(manifest.Name), "compensat") &&
			slices.Contains(manifest.Capabilities, record.Connector) {
			record.Tool = manifest.Name
			break
		}
	}
	if record.Tool == "" {
		record.Error = "no compensation tool provides connector " + record.Connector
		return record
	}
	idempotencyKey := officeTaskIdempotencyKey(order.ID, task.ID) + ":compensate"
	argumentValues := map[string]any{
		"action": record.Action, "work_order": order.ID, "task_id": task.ID,
		"idempotency_key": idempotencyKey,
	}
	if len(task.Receipts) > 0 {
		argumentValues["receipts"] = task.Receipts
	}
	arguments, _ := json.Marshal(argumentValues)
	output, err := s.registry.Execute(ctx, record.Tool, arguments)
	if err != nil {
		record.Error = err.Error()
		return record
	}
	var value struct {
		Receipts []struct {
			ReceiptID         string `json:"receipt_id"`
			Action            string `json:"action"`
			Target            string `json:"target"`
			Result            string `json:"result"`
			ExternalReference string `json:"external_reference"`
			IdempotencyKey    string `json:"idempotency_key"`
			CreatedAt         string `json:"created_at"`
		} `json:"receipts"`
	}
	if err := json.Unmarshal([]byte(output), &value); err != nil || len(value.Receipts) == 0 {
		record.Error = "compensation connector returned no receipt"
		return record
	}
	completedAt := time.Now().UTC()
	for _, receipt := range value.Receipts {
		createdAt, parseErr := time.Parse(time.RFC3339Nano, receipt.CreatedAt)
		if parseErr != nil || receipt.Result != "compensated" ||
			!strings.HasPrefix(receipt.IdempotencyKey, idempotencyKey) {
			record.Error = "compensation connector returned an invalid receipt"
			return record
		}
		record.Receipts = append(record.Receipts, office.ExecutionReceipt{
			ReceiptID: receipt.ReceiptID, Action: receipt.Action, Target: receipt.Target,
			Result: receipt.Result, ExternalReference: receipt.ExternalReference,
			IdempotencyKey: receipt.IdempotencyKey, CreatedAt: createdAt.UTC(),
			Connector: record.Connector, Tool: record.Tool, VerifiedAt: completedAt,
		})
	}
	record.Status = "compensated"
	record.CompletedAt = &completedAt
	return record
}
