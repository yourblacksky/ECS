package server

import (
	"ecs/internal/agentteams"
	"ecs/internal/office"
	"ecs/internal/trace"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"
)

func (s *Server) agentTeamsTask(w http.ResponseWriter, r *http.Request) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "node is busy")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes)
	defer r.Body.Close()
	var task agentteams.TaskRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&task); err != nil {
		writeError(w, http.StatusBadRequest, "invalid AgentTeams task: "+err.Error())
		return
	}
	if err := task.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ownerPeerNodeID := requestOwner(r.Context())
	if existing, err := s.agentTeamsStore.LoadResult(task.TaskID); err == nil {
		if existing.Meta == nil || !agentteams.OwnedBy(*existing.Meta, ownerPeerNodeID) {
			writeError(w, http.StatusNotFound, "AgentTeams task not found")
			return
		}
		if existing.Meta != nil && existing.Meta.RequestHash != "" &&
			existing.Meta.RequestHash != agentteams.Fingerprint(task) {
			writeError(w, http.StatusNotFound, "AgentTeams task not found")
			return
		}
		if !s.agentTeamsApprovalReadyToResume(task, *existing.Meta, ownerPeerNodeID) {
			writeJSON(w, http.StatusOK, existing)
			return
		}
	}
	request, promptContext := agentteams.MapTask(
		task,
		s.agentTeamsProfile,
		s.cfg.AgentTeams.MaxTaskPromptChars,
	)
	var officeOrder *office.WorkOrder
	var officeStage *office.Task
	stopOfficeLease := func() {}
	defer func() { stopOfficeLease() }()
	if task.Office != nil {
		if s.office == nil {
			writeError(w, http.StatusConflict, "Office runtime is not available")
			return
		}
		resolvedOrder, resolved, resolveErr := s.office.ResolveTask(
			task.Office.WorkOrderID, task.Office.StageID, ownerPeerNodeID,
		)
		if resolveErr != nil {
			status := http.StatusConflict
			if errors.Is(resolveErr, os.ErrNotExist) {
				status = http.StatusNotFound
			}
			writeError(w, status, resolveErr.Error())
			return
		}
		officeOrder = &resolvedOrder
		officeStage = &resolved
		request.Agent = resolved.Agent
		request.Skills = append([]string(nil), resolved.Skills...)
		request.Tools = s.localOfficeStageTools(resolved)
		request.RequiredSkills = append([]string(nil), resolved.Skills...)
		request.ApprovedTools = nil
		request.RestrictSkills = true
		request.RestrictTools = true
		if officeTaskRequiresReceipts(resolved) {
			idempotencyKey := officeTaskIdempotencyKey(resolvedOrder.ID, resolved.ID)
			instruction := "\n必须使用精确幂等键 " + idempotencyKey +
				"，并调用 execution.receipt.verify 校验连接器回执。"
			request.Prompt += instruction
			promptContext.Objective += instruction
			if request.Metadata == nil {
				request.Metadata = make(map[string]string)
			}
			request.Metadata["office_idempotency_key"] = idempotencyKey
		}
	}
	bounded, err := boundApprovedRequest(r.Context(), request, nil)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	request = bounded.Request()
	meta, err := s.agentTeamsStore.Begin(task, ownerPeerNodeID)
	if err != nil {
		if errors.Is(err, agentteams.ErrOwnerMismatch) {
			writeError(w, http.StatusNotFound, "AgentTeams task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	request.TraceID = trace.NewID()
	recorder := s.traces.StartOwned(requestOwner(r.Context()), request.TraceID, request.RequestID, s.cfg.NodeName, "/v1/agentteams/tasks", map[string]any{
		"task":  task,
		"agent": promptContext.AgentName,
		"depth": 0,
	})
	if recorder.TraceID() != "" {
		request.TraceID = recorder.TraceID()
	}
	var officeMapping *agentteams.OfficeTaskMapping
	officeStageSpan := ""
	if task.Office != nil {
		var mapped office.AgentTeamsStageMapping
		mapped, err = s.office.BindAgentTeamsStage(
			task.Office.WorkOrderID, task.Office.StageID, task.TaskID,
			task.ProjectID, ownerPeerNodeID,
		)
		if err == nil {
			value := agentTeamsOfficeMapping(mapped)
			officeMapping = &value
			meta, err = s.agentTeamsStore.SetOfficeMapping(task.TaskID, value)
		}
		if err == nil && office.RequiresApproval(officeStage.Approval) {
			var approval office.ApprovalRecord
			var approved bool
			_, approval, approved, err = s.office.EnsureTaskApproval(
				task.Office.WorkOrderID, task.Office.StageID, ownerPeerNodeID,
				request.Skills, request.Tools, officeStage.Connectors, time.Now().UTC(),
			)
			if err == nil && !approved {
				_, current, resolveErr := s.office.ResolveTask(
					task.Office.WorkOrderID, task.Office.StageID, ownerPeerNodeID,
				)
				if resolveErr == nil {
					value := agentTeamsOfficeMapping(office.AgentTeamsStageMapping{
						WorkOrderID: task.Office.WorkOrderID, Component: officeOrder.Component,
						StageID: current.StageID, OfficeTaskID: current.ID, Status: current.Status,
					})
					officeMapping = &value
					meta, _ = s.agentTeamsStore.SetOfficeMapping(task.TaskID, value)
				}
				meta, _ = s.agentTeamsStore.SetStatus(
					task.TaskID, "pending_approval",
					"waiting for human approval "+approval.ID,
				)
				recorder.Event(recorder.RootID(), "office_approval", approval.ID, "pending", map[string]any{
					"work_order_id": task.Office.WorkOrderID,
					"stage_id":      task.Office.StageID,
					"expires_at":    approval.ExpiresAt,
				})
				recorder.Finish("pending_approval", "AgentTeams task is waiting for human approval", nil)
				writeJSON(w, http.StatusAccepted, agentteams.TaskResult{
					ProjectID: task.ProjectID, TaskID: task.TaskID,
					TraceID: agentteams.EffectiveTraceID(task), ExecutionTraceID: request.TraceID,
					Status: "PENDING_APPROVAL", Node: s.cfg.NodeName,
					Error: "waiting for human approval " + approval.ID,
					Meta:  &meta, Office: officeMapping,
				})
				return
			}
			if err == nil {
				request.ApprovedTools = appendUniqueSorted(
					append([]string(nil), request.Skills...), request.Tools...,
				)
			}
		}
		if err == nil {
			mapped, err = s.office.StartAgentTeamsStage(
				task.Office.WorkOrderID, task.Office.StageID, task.TaskID,
				task.ProjectID, request.TraceID, ownerPeerNodeID,
			)
			if err == nil {
				value := agentTeamsOfficeMapping(mapped)
				officeMapping = &value
				meta, err = s.agentTeamsStore.SetOfficeMapping(task.TaskID, value)
			}
		}
		if err == nil {
			var attempt office.TaskAttempt
			_, attempt, _, err = s.office.ClaimTask(
				task.Office.WorkOrderID,
				task.Office.StageID,
				office.TaskClaim{
					Node: s.cfg.NodeName, TraceID: request.TraceID,
					IdempotencyKey: officeTaskIdempotencyKey(
						task.Office.WorkOrderID, officeStage.ID,
					),
					Skills: officeStage.Skills, Tools: request.Tools,
					Connectors: officeStage.Connectors, Now: time.Now().UTC(),
				},
			)
			if err == nil {
				_, err = s.office.MarkAttemptDispatched(
					task.Office.WorkOrderID, task.Office.StageID,
					attempt.ID, time.Now().UTC(),
				)
				if err == nil {
					stopOfficeLease = s.maintainOfficeAttemptLease(
						r.Context(), task.Office.WorkOrderID,
						task.Office.StageID, attempt.ID,
					)
				}
			}
		}
		if err != nil {
			if officeMapping != nil &&
				!errors.Is(err, office.ErrApprovalRejected) &&
				!errors.Is(err, office.ErrApprovalExpired) {
				mapped, finishErr := s.office.FinishAgentTeamsStage(
					officeMapping.WorkOrderID, officeMapping.StageID, task.TaskID,
					ownerPeerNodeID, nil, err,
				)
				if finishErr == nil {
					value := agentTeamsOfficeMapping(mapped)
					officeMapping = &value
					if updatedMeta, updateErr := s.agentTeamsStore.SetOfficeMapping(task.TaskID, value); updateErr == nil {
						meta = updatedMeta
					}
				}
			}
			recorder.Finish("rejected", "AgentTeams task could not be mapped to Office stage", err)
			result := agentteams.TaskResult{
				ProjectID: task.ProjectID, TaskID: task.TaskID,
				TraceID: agentteams.EffectiveTraceID(task), ExecutionTraceID: request.TraceID,
				Status: "BLOCKED", Node: s.cfg.NodeName, Error: err.Error(), Meta: &meta,
				Office: officeMapping,
			}
			finalMeta, storeErr := s.agentTeamsStore.Fail(task, result, err)
			if storeErr == nil {
				result.Meta = &finalMeta
			}
			status := http.StatusConflict
			if errors.Is(err, os.ErrNotExist) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, result)
			return
		}
		officeStageSpan = recorder.StartSpan(recorder.RootID(), "office_stage", officeMapping.StageID, map[string]any{
			"agentteams_task_id": task.TaskID,
			"work_order_id":      officeMapping.WorkOrderID,
			"office_task_id":     officeMapping.OfficeTaskID,
		})
	}
	traceContext := trace.WithRecorder(r.Context(), recorder, recorder.RootID())
	bounded, err = boundApprovedRequest(r.Context(), request, request.ApprovedTools)
	if err != nil {
		result := agentteams.TaskResult{
			ProjectID: task.ProjectID, TaskID: task.TaskID,
			TraceID: agentteams.EffectiveTraceID(task), ExecutionTraceID: request.TraceID,
			Status: "BLOCKED", Node: s.cfg.NodeName, Error: err.Error(), Meta: &meta,
			Office: officeMapping,
		}
		if officeMapping != nil {
			mapped, finishErr := s.office.FinishAgentTeamsStage(
				officeMapping.WorkOrderID, officeMapping.StageID, task.TaskID,
				ownerPeerNodeID, nil, err,
			)
			if finishErr == nil {
				value := agentTeamsOfficeMapping(mapped)
				result.Office = &value
				if updatedMeta, updateErr := s.agentTeamsStore.SetOfficeMapping(task.TaskID, value); updateErr == nil {
					meta = updatedMeta
					result.Meta = &meta
				}
			}
		}
		if finalMeta, storeErr := s.agentTeamsStore.Fail(task, result, err); storeErr == nil {
			result.Meta = &finalMeta
		}
		recorder.FinishSpan(officeStageSpan, "rejected", result.Office, err, nil)
		recorder.Finish("rejected", "", err)
		writeJSON(w, http.StatusForbidden, result)
		return
	}
	response, runErr := s.agent.ReplyWithContext(traceContext, bounded, promptContext)
	stopOfficeLease()
	if runErr == nil && officeStage != nil {
		runErr = s.validateOfficeExecution(officeTaskExecutionRequest{
			Skills: officeStage.Skills, Tools: request.Tools, Connectors: officeStage.Connectors,
		}, response)
	}
	if runErr == nil && officeStage != nil && officeTaskRequiresReceipts(*officeStage) {
		receiptRequest := officeTaskExecutionRequest{
			WorkOrder: task.Office.WorkOrderID, TaskID: officeStage.ID,
			Skills: officeStage.Skills, Tools: request.Tools, Connectors: officeStage.Connectors,
			Approval:       officeStage.ApprovalRecord,
			IdempotencyKey: officeTaskIdempotencyKey(task.Office.WorkOrderID, officeStage.ID),
		}
		var receipts []office.ExecutionReceipt
		var verification *office.ReceiptVerification
		receipts, verification, runErr = s.validateOfficeReceipts(
			receiptRequest, *officeStage, response, time.Now().UTC(),
		)
		if runErr == nil {
			_, runErr = s.office.SetTaskReceipts(
				task.Office.WorkOrderID, officeStage.ID, ownerPeerNodeID,
				receipts, verification,
			)
		}
	}
	result := agentteams.TaskResult{
		ProjectID:        task.ProjectID,
		TaskID:           task.TaskID,
		TraceID:          agentteams.EffectiveTraceID(task),
		ExecutionTraceID: request.TraceID,
		Message:          response.Message,
		ToolRuns:         response.ToolRuns,
		SkillRuns:        response.SkillRuns,
		Node:             s.cfg.NodeName,
		Meta:             &meta,
		Office:           officeMapping,
	}
	if runErr != nil {
		if officeMapping != nil {
			mapped, finishErr := s.office.FinishAgentTeamsStage(
				officeMapping.WorkOrderID, officeMapping.StageID, task.TaskID,
				ownerPeerNodeID, nil, runErr,
			)
			if finishErr != nil {
				s.logger.Error("Office stage failure state could not be saved", "task_id", task.TaskID, "error", finishErr)
			} else {
				value := agentTeamsOfficeMapping(mapped)
				result.Office = &value
				if officeStage != nil && officeStage.OnFailure == "compensate" {
					compensation := s.executeLocalOfficeCompensation(
						traceContext, *officeOrder, *officeStage,
					)
					if compensatedOrder, compensationErr := s.office.SetTaskCompensation(
						officeMapping.WorkOrderID, officeMapping.StageID,
						ownerPeerNodeID, compensation,
					); compensationErr == nil {
						for _, compensatedTask := range compensatedOrder.Tasks {
							if compensatedTask.ID == officeStage.ID {
								value.Status = compensatedTask.Status
								result.Office = &value
								break
							}
						}
					}
				}
				if updatedMeta, updateErr := s.agentTeamsStore.SetOfficeMapping(task.TaskID, value); updateErr == nil {
					meta = updatedMeta
					result.Meta = &meta
				}
			}
		}
		recorder.FinishSpan(officeStageSpan, "blocked", result.Office, runErr, nil)
		recorder.Finish("blocked", "", runErr)
		result.Error = runErr.Error()
		finalMeta, storeErr := s.agentTeamsStore.Fail(task, result, runErr)
		if storeErr != nil {
			s.logger.Error("AgentTeams task failure state could not be saved", "task_id", task.TaskID, "error", storeErr)
		} else {
			result.Meta = &finalMeta
		}
		result.Status = "BLOCKED"
		s.logger.Error("AgentTeams task failed", "task_id", task.TaskID, "error", runErr)
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	response.RequestID = request.RequestID
	response.TraceID = request.TraceID
	response.Node = s.cfg.NodeName
	result.Message = response.Message
	result.ToolRuns = response.ToolRuns
	result.SkillRuns = response.SkillRuns
	if officeMapping != nil {
		encodedResponse, encodeErr := json.Marshal(response)
		if encodeErr == nil {
			var mapped office.AgentTeamsStageMapping
			mapped, err = s.office.FinishAgentTeamsStage(
				officeMapping.WorkOrderID, officeMapping.StageID, task.TaskID,
				ownerPeerNodeID, encodedResponse, nil,
			)
			if err == nil {
				value := agentTeamsOfficeMapping(mapped)
				result.Office = &value
				meta, err = s.agentTeamsStore.SetOfficeMapping(task.TaskID, value)
				result.Meta = &meta
			}
		} else {
			err = encodeErr
		}
		if err != nil {
			recorder.FinishSpan(officeStageSpan, "error", result.Office, err, nil)
			recorder.Finish("error", "Office stage result could not be saved", err)
			result.Error = err.Error()
			finalMeta, storeErr := s.agentTeamsStore.Fail(task, result, err)
			if storeErr == nil {
				result.Meta = &finalMeta
			}
			result.Status = "BLOCKED"
			writeJSON(w, http.StatusInternalServerError, result)
			return
		}
		recorder.FinishSpan(officeStageSpan, "completed", result.Office, nil, nil)
	}
	finalMeta, err := s.agentTeamsStore.Complete(task, result)
	if err != nil {
		recorder.Finish("error", response.Message.Content, err)
		writeError(w, http.StatusInternalServerError, "task completed but result could not be saved: "+err.Error())
		return
	}
	result.Meta = &finalMeta
	result.Status = "SUCCESS"
	recorder.Finish("success", result.Message.Content, nil)
	writeJSON(w, http.StatusOK, result)
}

func agentTeamsOfficeMapping(mapping office.AgentTeamsStageMapping) agentteams.OfficeTaskMapping {
	return agentteams.OfficeTaskMapping{
		WorkOrderID:  mapping.WorkOrderID,
		Component:    mapping.Component,
		StageID:      mapping.StageID,
		OfficeTaskID: mapping.OfficeTaskID,
		Status:       mapping.Status,
	}
}

// agentTeamsApprovalReadyToResume reports whether a stored pending-approval
// task may be retried in place. The human gate has approved the bound Office
// stage and the stage is still mapped to the same AgentTeams task, so
// re-submission must resume execution instead of replaying the stale pending
// state through the idempotency shortcut.
func (s *Server) agentTeamsApprovalReadyToResume(
	task agentteams.TaskRequest,
	meta agentteams.TaskMeta,
	ownerPeerNodeID string,
) bool {
	if task.Office == nil || meta.Status != "pending_approval" || s.office == nil {
		return false
	}
	_, stage, err := s.office.ResolveTask(
		task.Office.WorkOrderID, task.Office.StageID, ownerPeerNodeID,
	)
	if err != nil {
		return false
	}
	if stage.ApprovalRecord == nil || stage.AgentTeamsTaskID != task.TaskID {
		return false
	}
	return office.ValidateActiveApproval(stage.ApprovalRecord, time.Now().UTC()) == nil
}

func (s *Server) getAgentTeamsTask(w http.ResponseWriter, r *http.Request) {
	result, err := s.agentTeamsStore.LoadResult(r.PathValue("task"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "AgentTeams task not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if result.Meta == nil || !agentteams.OwnedBy(*result.Meta, requestOwner(r.Context())) {
		writeError(w, http.StatusNotFound, "AgentTeams task not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
