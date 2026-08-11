package office

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// BindAgentTeamsStage establishes and persists the one-to-one relationship
// between an AgentTeams task and an Office stage before model execution starts.
func (r *Registry) BindAgentTeamsStage(
	orderID string,
	stageID string,
	agentTeamsTaskID string,
	projectID string,
	ownerPeerNodeID string,
) (AgentTeamsStageMapping, error) {
	var mapping AgentTeamsStageMapping
	_, err := r.UpdateOrder(orderID, func(order *WorkOrder) error {
		if !OwnedBy(*order, ownerPeerNodeID) {
			return os.ErrNotExist
		}
		taskIndex := officeStageTaskIndex(*order, stageID)
		if taskIndex < 0 {
			return fmt.Errorf("office stage %q not found", stageID)
		}
		task := &order.Tasks[taskIndex]
		if task.AgentTeamsTaskID != "" && task.AgentTeamsTaskID != agentTeamsTaskID {
			return fmt.Errorf("office stage %q is already mapped to another AgentTeams task", stageID)
		}
		if order.AgentTeamsProjectID != "" && projectID != "" && order.AgentTeamsProjectID != projectID {
			return errors.New("office work order is already mapped to another AgentTeams project")
		}
		changed := task.AgentTeamsTaskID == ""
		task.StageID = stageID
		task.AgentTeamsTaskID = agentTeamsTaskID
		if order.AgentTeamsProjectID == "" {
			order.AgentTeamsProjectID = projectID
		}
		if changed {
			order.Audit = append(order.Audit, AuditEvent{
				At: time.Now().UTC(), Action: "agentteams.task.mapped", Actor: agentTeamsTaskID,
				Details: map[string]any{
					"agentteams_task_id": agentTeamsTaskID,
					"project_id":         projectID,
					"stage_id":           stageID,
					"office_task_id":     task.ID,
				},
			})
		}
		mapping = stageMapping(*order, *task)
		return nil
	})
	return mapping, err
}

func (r *Registry) StartAgentTeamsStage(
	orderID string,
	stageID string,
	agentTeamsTaskID string,
	projectID string,
	executionTraceID string,
	ownerPeerNodeID string,
) (AgentTeamsStageMapping, error) {
	if _, err := r.BindAgentTeamsStage(
		orderID, stageID, agentTeamsTaskID, projectID, ownerPeerNodeID,
	); err != nil {
		return AgentTeamsStageMapping{}, err
	}
	var mapping AgentTeamsStageMapping
	_, err := r.UpdateOrder(orderID, func(order *WorkOrder) error {
		if !OwnedBy(*order, ownerPeerNodeID) {
			return os.ErrNotExist
		}
		taskIndex := officeStageTaskIndex(*order, stageID)
		if taskIndex < 0 {
			return fmt.Errorf("office stage %q not found", stageID)
		}
		task := &order.Tasks[taskIndex]
		if task.AgentTeamsTaskID != "" && task.AgentTeamsTaskID != agentTeamsTaskID {
			return fmt.Errorf("office stage %q is already mapped to another AgentTeams task", stageID)
		}
		if order.AgentTeamsProjectID != "" && projectID != "" && order.AgentTeamsProjectID != projectID {
			return errors.New("office work order is already mapped to another AgentTeams project")
		}
		for _, dependencyID := range task.DependsOn {
			dependencyIndex := officeTaskIndex(*order, dependencyID)
			if dependencyIndex < 0 || order.Tasks[dependencyIndex].Status != "completed" {
				return fmt.Errorf("office stage %q dependencies are not completed", stageID)
			}
		}
		if task.Status != "pending" && task.Status != "running" && task.Status != "completed" {
			return fmt.Errorf("office stage %q cannot start from status %q", stageID, task.Status)
		}
		if RequiresApproval(task.Approval) {
			if err := ValidateActiveApproval(task.ApprovalRecord, time.Now().UTC()); err != nil {
				return err
			}
		}
		changed := task.Status == "pending"
		task.StageID = stageID
		task.AgentTeamsTaskID = agentTeamsTaskID
		if task.Status != "completed" {
			task.Status = "running"
			if task.StartedAt == nil {
				startedAt := time.Now().UTC()
				task.StartedAt = &startedAt
			}
			task.TraceID = executionTraceID
			order.Status = "running"
		}
		if order.AgentTeamsProjectID == "" {
			order.AgentTeamsProjectID = projectID
		}
		if changed {
			order.Audit = append(order.Audit, AuditEvent{
				At: time.Now().UTC(), Action: "agentteams.task.started", Actor: agentTeamsTaskID,
				Details: map[string]any{
					"agentteams_task_id": agentTeamsTaskID,
					"project_id":         projectID,
					"stage_id":           stageID,
					"office_task_id":     task.ID,
					"trace_id":           executionTraceID,
				},
			})
		}
		mapping = stageMapping(*order, *task)
		return nil
	})
	return mapping, err
}

func (r *Registry) FinishAgentTeamsStage(
	orderID string,
	stageID string,
	agentTeamsTaskID string,
	ownerPeerNodeID string,
	result json.RawMessage,
	taskError error,
) (AgentTeamsStageMapping, error) {
	var mapping AgentTeamsStageMapping
	_, err := r.UpdateOrder(orderID, func(order *WorkOrder) error {
		if !OwnedBy(*order, ownerPeerNodeID) {
			return os.ErrNotExist
		}
		taskIndex := officeStageTaskIndex(*order, stageID)
		if taskIndex < 0 {
			return fmt.Errorf("office stage %q not found", stageID)
		}
		task := &order.Tasks[taskIndex]
		if task.AgentTeamsTaskID != agentTeamsTaskID {
			return fmt.Errorf("office stage %q is not mapped to AgentTeams task %q", stageID, agentTeamsTaskID)
		}
		completedAt := time.Now().UTC()
		task.CompletedAt = &completedAt
		if taskError != nil {
			task.Status = "failed"
			task.Error = taskError.Error()
			order.Status = mappedFailureStatus(task.OnFailure)
			settleAgentTeamsAttempt(order, task, AttemptFailed, OutboxFailed, completedAt, task.Error)
			order.Audit = append(order.Audit, AuditEvent{
				At: completedAt, Action: "agentteams.task.blocked", Actor: agentTeamsTaskID,
				Details: map[string]any{
					"agentteams_task_id": agentTeamsTaskID,
					"stage_id":           stageID,
					"office_task_id":     task.ID,
					"error":              task.Error,
					"work_order_status":  order.Status,
				},
			})
		} else {
			task.Status = "completed"
			task.Error = ""
			task.Result = append(json.RawMessage(nil), result...)
			settleAgentTeamsAttempt(order, task, AttemptSucceeded, OutboxConfirmed, completedAt, "")
			for index := range order.Handoffs {
				if order.Handoffs[index].FromTask == task.ID {
					order.Handoffs[index].Integrity = "verified"
				}
			}
			order.Status = "running"
			if allOfficeTasksCompleted(order.Tasks) {
				order.Status = "completed"
			}
			order.Audit = append(order.Audit, AuditEvent{
				At: completedAt, Action: "agentteams.task.completed", Actor: agentTeamsTaskID,
				Details: map[string]any{
					"agentteams_task_id": agentTeamsTaskID,
					"stage_id":           stageID,
					"office_task_id":     task.ID,
					"trace_id":           task.TraceID,
				},
			})
		}
		mapping = stageMapping(*order, *task)
		return nil
	})
	return mapping, err
}

func settleAgentTeamsAttempt(
	order *WorkOrder,
	task *Task,
	attemptStatus string,
	outboxStatus string,
	completedAt time.Time,
	taskError string,
) {
	attempt := activeAttempt(task)
	if attempt == nil {
		return
	}
	attempt.Status = attemptStatus
	attempt.CompletedAt = timePointer(completedAt)
	attempt.Error = taskError
	task.ActiveAttemptID = ""
	for index := range order.Outbox {
		if order.Outbox[index].ID == attempt.OutboxEventID {
			order.Outbox[index].Status = outboxStatus
			order.Outbox[index].ConfirmedAt = timePointer(completedAt)
			order.Outbox[index].LastError = taskError
			break
		}
	}
}

func officeStageTaskIndex(order WorkOrder, stageID string) int {
	for index, task := range order.Tasks {
		candidate := task.StageID
		if candidate == "" {
			candidate = strings.TrimPrefix(task.ID, order.ID+".")
		}
		if candidate == stageID {
			return index
		}
	}
	return -1
}

func officeTaskIndex(order WorkOrder, taskID string) int {
	for index, task := range order.Tasks {
		if task.ID == taskID {
			return index
		}
	}
	return -1
}

func stageMapping(order WorkOrder, task Task) AgentTeamsStageMapping {
	return AgentTeamsStageMapping{
		WorkOrderID:  order.ID,
		Component:    order.Component,
		StageID:      task.StageID,
		OfficeTaskID: task.ID,
		Status:       task.Status,
	}
}

func allOfficeTasksCompleted(tasks []Task) bool {
	for _, task := range tasks {
		if task.Status != "completed" {
			return false
		}
	}
	return true
}

func mappedFailureStatus(policy string) string {
	switch policy {
	case "hold":
		return "blocked"
	case "compensate":
		return "compensation_required"
	default:
		return "failed"
	}
}
