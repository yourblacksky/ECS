package office

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	AttemptClaimed          = "claimed"
	AttemptDispatched       = "dispatched"
	AttemptSucceeded        = "succeeded"
	AttemptFailed           = "failed"
	AttemptOutcomeUnknown   = "outcome_unknown"
	OutboxPending           = "pending"
	OutboxDispatched        = "dispatched"
	OutboxConfirmed         = "confirmed"
	OutboxFailed            = "failed"
	OutboxRecoveryRequired  = "recovery_required"
	OutboxSuperseded        = "superseded"
	StatusRecoveryRequired  = "recovery_required"
	defaultOfficeTaskLease  = 2 * time.Minute
	officeTaskOutboxEvent   = "office.task.execute"
	recoveryLeaseExpiryText = "execution lease expired before a durable outcome was recorded"
	legacyRunningTaskText   = "running task has no durable execution attempt"
)

var (
	ErrTaskAlreadyClaimed = errors.New("Office task already has an active execution attempt")
	ErrAttemptNotActive   = errors.New("Office task execution attempt is not active")
	errNoExpiredAttempts  = errors.New("Office work order has no expired attempts")
)

type TaskClaim struct {
	Node           string
	TraceID        string
	IdempotencyKey string
	Skills         []string
	Tools          []string
	Connectors     []string
	Now            time.Time
}

type TaskAttemptResult struct {
	Status              string
	Result              json.RawMessage
	Error               string
	Receipts            []ExecutionReceipt
	ReceiptVerification *ReceiptVerification
	CompensationRecord  *CompensationRecord
	Actor               string
	CompletedAt         time.Time
}

// ClaimTask durably creates both an execution attempt and its outbox intent in
// the same WorkOrder update. No connector call may happen before this succeeds.
func (r *Registry) ClaimTask(
	orderID string,
	taskReference string,
	claim TaskClaim,
) (WorkOrder, TaskAttempt, OutboxEvent, error) {
	var attempt TaskAttempt
	var event OutboxEvent
	order, err := r.UpdateOrder(orderID, func(order *WorkOrder) error {
		taskIndex := officeTaskReferenceIndex(*order, taskReference)
		if taskIndex < 0 {
			return fmt.Errorf("office task %q not found", taskReference)
		}
		task := &order.Tasks[taskIndex]
		if task.Status == "completed" {
			return errors.New("Office task is already completed")
		}
		if task.Status == "pending_approval" {
			return errors.New("Office task is still pending approval")
		}
		now := claim.Now.UTC()
		if now.IsZero() {
			now = time.Now().UTC()
		}
		if active := activeAttempt(task); active != nil {
			if activeAttemptStatus(active.Status) && !now.After(active.LeaseExpiresAt) {
				return ErrTaskAlreadyClaimed
			}
			if activeAttemptStatus(active.Status) {
				markAttemptRecovery(order, task, active, now, recoveryLeaseExpiryText)
			}
		}
		for _, dependency := range task.DependsOn {
			dependencyIndex := officeTaskIndex(*order, dependency)
			if dependencyIndex < 0 || order.Tasks[dependencyIndex].Status != "completed" {
				return fmt.Errorf("Office task dependency %q is not completed", dependency)
			}
		}
		attemptID, err := randomID("attempt_")
		if err != nil {
			return err
		}
		outboxID, err := randomID("outbox_")
		if err != nil {
			return err
		}
		lease := r.cfg.AttemptLease.Duration()
		if lease <= 0 {
			lease = defaultOfficeTaskLease
		}
		task.AttemptCount++
		attempt = TaskAttempt{
			ID: attemptID, Number: task.AttemptCount, Status: AttemptClaimed,
			Node: strings.TrimSpace(claim.Node), TraceID: strings.TrimSpace(claim.TraceID),
			IdempotencyKey: strings.TrimSpace(claim.IdempotencyKey), OutboxEventID: outboxID,
			ClaimedAt: now, LeaseExpiresAt: now.Add(lease),
		}
		event = OutboxEvent{
			ID: outboxID, Type: officeTaskOutboxEvent, Status: OutboxPending,
			WorkOrderID: order.ID, TaskID: task.ID, AttemptID: attempt.ID,
			Destination: attempt.Node, IdempotencyKey: attempt.IdempotencyKey,
			Skills:     append([]string(nil), claim.Skills...),
			Tools:      append([]string(nil), claim.Tools...),
			Connectors: append([]string(nil), claim.Connectors...),
			CreatedAt:  now,
		}
		task.Attempts = append(task.Attempts, attempt)
		task.ActiveAttemptID = attempt.ID
		task.Status = "running"
		task.AssignedNode = attempt.Node
		task.TraceID = attempt.TraceID
		task.StartedAt = timePointer(now)
		task.CompletedAt = nil
		task.Error = ""
		order.Outbox = append(order.Outbox, event)
		order.Status = "running"
		order.Audit = append(order.Audit,
			AuditEvent{
				At: now, Action: "task.attempt.claimed", Actor: attempt.Node,
				Details: map[string]any{
					"task": task.ID, "attempt_id": attempt.ID, "attempt": attempt.Number,
					"lease_expires_at": attempt.LeaseExpiresAt,
				},
			},
			AuditEvent{
				At: now, Action: "outbox.enqueued", Actor: attempt.Node,
				Details: map[string]any{
					"task": task.ID, "attempt_id": attempt.ID, "outbox_id": event.ID,
					"idempotency_key": event.IdempotencyKey,
				},
			},
		)
		return nil
	})
	return order, attempt, event, err
}

// MarkAttemptDispatched records the hand-off boundary immediately before the
// controller performs the network call.
func (r *Registry) MarkAttemptDispatched(
	orderID string,
	taskReference string,
	attemptID string,
	now time.Time,
) (WorkOrder, error) {
	return r.UpdateOrder(orderID, func(order *WorkOrder) error {
		task, attempt, event, err := resolveAttempt(order, taskReference, attemptID)
		if err != nil {
			return err
		}
		if attempt.Status == AttemptDispatched {
			return nil
		}
		if attempt.Status != AttemptClaimed {
			return ErrAttemptNotActive
		}
		now = now.UTC()
		if now.IsZero() {
			now = time.Now().UTC()
		}
		attempt.Status = AttemptDispatched
		attempt.DispatchedAt = timePointer(now)
		event.Status = OutboxDispatched
		event.DispatchedAt = timePointer(now)
		order.Audit = append(order.Audit, AuditEvent{
			At: now, Action: "outbox.dispatched", Actor: attempt.Node,
			Details: map[string]any{
				"task": task.ID, "attempt_id": attempt.ID, "outbox_id": event.ID,
			},
		})
		return nil
	})
}

func (r *Registry) RenewTaskAttempt(
	orderID string,
	taskReference string,
	attemptID string,
	now time.Time,
) (WorkOrder, error) {
	return r.UpdateOrder(orderID, func(order *WorkOrder) error {
		_, attempt, _, err := resolveAttempt(order, taskReference, attemptID)
		if err != nil {
			return err
		}
		if !activeAttemptStatus(attempt.Status) {
			return ErrAttemptNotActive
		}
		now = now.UTC()
		if now.IsZero() {
			now = time.Now().UTC()
		}
		lease := r.cfg.AttemptLease.Duration()
		if lease <= 0 {
			lease = defaultOfficeTaskLease
		}
		attempt.LeaseExpiresAt = now.Add(lease)
		return nil
	})
}

func (r *Registry) CompleteTaskAttempt(
	orderID string,
	taskReference string,
	attemptID string,
	result TaskAttemptResult,
) (WorkOrder, error) {
	return r.UpdateOrder(orderID, func(order *WorkOrder) error {
		task, attempt, event, err := resolveAttempt(order, taskReference, attemptID)
		if err != nil {
			return err
		}
		if !activeAttemptStatus(attempt.Status) {
			return ErrAttemptNotActive
		}
		completedAt := result.CompletedAt.UTC()
		if completedAt.IsZero() {
			completedAt = time.Now().UTC()
		}
		status := strings.TrimSpace(result.Status)
		if status == "" {
			status = "completed"
		}
		task.Status = status
		task.Result = append(json.RawMessage(nil), result.Result...)
		task.Error = strings.TrimSpace(result.Error)
		task.CompletedAt = timePointer(completedAt)
		task.Receipts = append([]ExecutionReceipt(nil), result.Receipts...)
		task.ReceiptVerification = result.ReceiptVerification
		task.CompensationRecord = result.CompensationRecord
		task.ActiveAttemptID = ""
		attempt.CompletedAt = timePointer(completedAt)
		attempt.Error = task.Error
		event.ConfirmedAt = timePointer(completedAt)
		event.LastError = task.Error
		if status == "completed" {
			attempt.Status = AttemptSucceeded
			event.Status = OutboxConfirmed
			for index := range order.Outbox {
				previous := &order.Outbox[index]
				if previous.ID != event.ID &&
					previous.TaskID == task.ID &&
					previous.IdempotencyKey == event.IdempotencyKey &&
					previous.Status == OutboxRecoveryRequired {
					previous.Status = OutboxSuperseded
					previous.ConfirmedAt = timePointer(completedAt)
					previous.LastError = "resolved by attempt " + attempt.ID
				}
			}
			for index := range order.Handoffs {
				if order.Handoffs[index].FromTask == task.ID {
					order.Handoffs[index].Integrity = "verified"
				}
			}
		} else {
			attempt.Status = AttemptFailed
			event.Status = OutboxFailed
			order.Status = status
		}
		action := "task." + status
		if status == "completed" {
			action = "task.completed"
		}
		order.Audit = append(order.Audit, AuditEvent{
			At: completedAt, Action: action, Actor: strings.TrimSpace(result.Actor),
			Details: map[string]any{
				"task": task.ID, "attempt_id": attempt.ID, "outbox_id": event.ID,
				"receipt_count": len(task.Receipts), "error": task.Error,
			},
		})
		if result.CompensationRecord != nil {
			order.Audit = append(order.Audit, AuditEvent{
				At:     completedAt,
				Action: "compensation." + result.CompensationRecord.Status,
				Actor:  strings.TrimSpace(result.Actor),
				Details: map[string]any{
					"task": task.ID, "attempt_id": attempt.ID,
					"action":        result.CompensationRecord.Action,
					"connector":     result.CompensationRecord.Connector,
					"tool":          result.CompensationRecord.Tool,
					"receipt_count": len(result.CompensationRecord.Receipts),
					"error":         result.CompensationRecord.Error,
				},
			})
		}
		return nil
	})
}

func (r *Registry) MarkAttemptRecoveryRequired(
	orderID string,
	taskReference string,
	attemptID string,
	reason string,
	now time.Time,
) (WorkOrder, error) {
	return r.UpdateOrder(orderID, func(order *WorkOrder) error {
		task, attempt, _, err := resolveAttempt(order, taskReference, attemptID)
		if err != nil {
			return err
		}
		if !activeAttemptStatus(attempt.Status) {
			return ErrAttemptNotActive
		}
		now = now.UTC()
		if now.IsZero() {
			now = time.Now().UTC()
		}
		markAttemptRecovery(order, task, attempt, now, reason)
		return nil
	})
}

// ReapExpiredAttempts durably converts abandoned leases into an explicit
// recovery state. It is safe to run at startup and periodically.
func (r *Registry) ReapExpiredAttempts(now time.Time) ([]RecoveryRecord, error) {
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	entries, err := os.ReadDir(r.cfg.OrderDirectory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []RecoveryRecord{}, nil
		}
		return nil, err
	}
	var recovered []RecoveryRecord
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		orderID := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		var orderRecovered []RecoveryRecord
		_, err := r.UpdateOrder(orderID, func(order *WorkOrder) error {
			for taskIndex := range order.Tasks {
				task := &order.Tasks[taskIndex]
				attempt := activeAttempt(task)
				if attempt == nil && task.Status == "running" {
					task.Status = StatusRecoveryRequired
					task.Error = legacyRunningTaskText
					task.CompletedAt = timePointer(now)
					order.Status = StatusRecoveryRequired
					order.Audit = append(order.Audit, AuditEvent{
						At: now, Action: "task.recovery_required", Actor: "system",
						Details: map[string]any{
							"task": task.ID, "reason": legacyRunningTaskText,
						},
					})
					orderRecovered = append(orderRecovered, RecoveryRecord{
						WorkOrderID: order.ID, TaskID: task.ID,
						Reason: legacyRunningTaskText,
					})
					continue
				}
				if attempt == nil || !activeAttemptStatus(attempt.Status) ||
					!now.After(attempt.LeaseExpiresAt) {
					continue
				}
				markAttemptRecovery(order, task, attempt, now, recoveryLeaseExpiryText)
				orderRecovered = append(orderRecovered, RecoveryRecord{
					WorkOrderID: order.ID, TaskID: task.ID,
					AttemptID: attempt.ID, Reason: recoveryLeaseExpiryText,
				})
			}
			if len(orderRecovered) == 0 {
				return errNoExpiredAttempts
			}
			return nil
		})
		if errors.Is(err, errNoExpiredAttempts) {
			continue
		}
		if err != nil {
			return recovered, fmt.Errorf("reap work order %s: %w", orderID, err)
		}
		recovered = append(recovered, orderRecovered...)
	}
	return recovered, nil
}

func resolveAttempt(
	order *WorkOrder,
	taskReference string,
	attemptID string,
) (*Task, *TaskAttempt, *OutboxEvent, error) {
	taskIndex := officeTaskReferenceIndex(*order, taskReference)
	if taskIndex < 0 {
		return nil, nil, nil, fmt.Errorf("office task %q not found", taskReference)
	}
	task := &order.Tasks[taskIndex]
	if task.ActiveAttemptID != attemptID {
		return nil, nil, nil, ErrAttemptNotActive
	}
	var attempt *TaskAttempt
	for index := range task.Attempts {
		if task.Attempts[index].ID == attemptID {
			attempt = &task.Attempts[index]
			break
		}
	}
	if attempt == nil {
		return nil, nil, nil, ErrAttemptNotActive
	}
	var event *OutboxEvent
	for index := range order.Outbox {
		if order.Outbox[index].ID == attempt.OutboxEventID {
			event = &order.Outbox[index]
			break
		}
	}
	if event == nil {
		return nil, nil, nil, errors.New("Office task attempt outbox event is missing")
	}
	return task, attempt, event, nil
}

func activeAttempt(task *Task) *TaskAttempt {
	if task.ActiveAttemptID == "" {
		return nil
	}
	for index := range task.Attempts {
		if task.Attempts[index].ID == task.ActiveAttemptID {
			return &task.Attempts[index]
		}
	}
	return nil
}

func activeAttemptStatus(status string) bool {
	return status == AttemptClaimed || status == AttemptDispatched
}

func markAttemptRecovery(
	order *WorkOrder,
	task *Task,
	attempt *TaskAttempt,
	now time.Time,
	reason string,
) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "execution outcome is unknown"
	}
	attempt.Status = AttemptOutcomeUnknown
	attempt.CompletedAt = timePointer(now)
	attempt.Error = reason
	task.Status = StatusRecoveryRequired
	task.Error = reason
	task.CompletedAt = timePointer(now)
	task.ActiveAttemptID = ""
	order.Status = StatusRecoveryRequired
	for index := range order.Outbox {
		if order.Outbox[index].ID == attempt.OutboxEventID {
			order.Outbox[index].Status = OutboxRecoveryRequired
			order.Outbox[index].ConfirmedAt = timePointer(now)
			order.Outbox[index].LastError = reason
			break
		}
	}
	order.Audit = append(order.Audit, AuditEvent{
		At: now, Action: "task.recovery_required", Actor: "system",
		Details: map[string]any{
			"task": task.ID, "attempt_id": attempt.ID,
			"outbox_id": attempt.OutboxEventID, "reason": reason,
		},
	})
}
