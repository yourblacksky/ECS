package office

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

func RequiresApproval(policy ApprovalPolicy) bool {
	return policy.Mode == "policy" || policy.Mode == "human"
}

func ValidateApprovalRecord(
	record *ApprovalRecord,
	skills []string,
	tools []string,
	connectors []string,
	now time.Time,
) error {
	if err := ValidateActiveApproval(record, now); err != nil {
		return err
	}
	policy := ApprovalPolicy{
		Mode: record.Mode, Risk: record.Risk,
		RequiredScopes: append([]string(nil), record.RequiredScopes...),
	}
	if approvalPolicyVersion(policy, skills, tools, connectors) != record.PolicyVersion {
		return errors.New("Office approval policy snapshot does not match the execution request")
	}
	return nil
}

func ValidateActiveApproval(record *ApprovalRecord, now time.Time) error {
	if record == nil {
		return errors.New("Office approval record is required")
	}
	if record.Status != "approved" {
		return errors.New("Office approval is not approved")
	}
	if now.UTC().After(record.ExpiresAt) {
		return ErrApprovalExpired
	}
	return nil
}

func (r *Registry) ResolveTask(orderID, taskReference, ownerPeerNodeID string) (WorkOrder, Task, error) {
	order, err := r.LoadOrder(orderID)
	if err != nil {
		return WorkOrder{}, Task{}, err
	}
	if !OwnedBy(order, ownerPeerNodeID) {
		return WorkOrder{}, Task{}, os.ErrNotExist
	}
	taskIndex := officeTaskReferenceIndex(order, taskReference)
	if taskIndex < 0 {
		return WorkOrder{}, Task{}, fmt.Errorf("office task %q not found", taskReference)
	}
	return order, order.Tasks[taskIndex], nil
}

func (r *Registry) SetTaskReceipts(
	orderID string,
	taskReference string,
	ownerPeerNodeID string,
	receipts []ExecutionReceipt,
	verification *ReceiptVerification,
) (WorkOrder, error) {
	if verification == nil || verification.Status != "verified" ||
		verification.Count != len(receipts) || len(receipts) == 0 {
		return WorkOrder{}, errors.New("verified receipt evidence is required")
	}
	return r.UpdateOrder(orderID, func(order *WorkOrder) error {
		if !OwnedBy(*order, ownerPeerNodeID) {
			return os.ErrNotExist
		}
		taskIndex := officeTaskReferenceIndex(*order, taskReference)
		if taskIndex < 0 {
			return fmt.Errorf("office task %q not found", taskReference)
		}
		task := &order.Tasks[taskIndex]
		task.Receipts = append([]ExecutionReceipt(nil), receipts...)
		if verification == nil {
			task.ReceiptVerification = nil
		} else {
			value := *verification
			task.ReceiptVerification = &value
		}
		order.Audit = append(order.Audit, AuditEvent{
			At: time.Now().UTC(), Action: "receipt.verified", Actor: task.AgentTeamsTaskID,
			Details: map[string]any{
				"task": task.ID, "receipt_count": len(receipts),
			},
		})
		return nil
	})
}

func (r *Registry) SetTaskCompensation(
	orderID string,
	taskReference string,
	ownerPeerNodeID string,
	record CompensationRecord,
) (WorkOrder, error) {
	if record.Status != "compensated" && record.Status != "compensation_failed" {
		return WorkOrder{}, errors.New("invalid compensation status")
	}
	if record.Status == "compensated" && len(record.Receipts) == 0 {
		return WorkOrder{}, errors.New("compensated state requires a compensation receipt")
	}
	return r.UpdateOrder(orderID, func(order *WorkOrder) error {
		if !OwnedBy(*order, ownerPeerNodeID) {
			return os.ErrNotExist
		}
		taskIndex := officeTaskReferenceIndex(*order, taskReference)
		if taskIndex < 0 {
			return fmt.Errorf("office task %q not found", taskReference)
		}
		task := &order.Tasks[taskIndex]
		task.CompensationRecord = &record
		task.Status = record.Status
		order.Status = record.Status
		order.Audit = append(order.Audit, AuditEvent{
			At: time.Now().UTC(), Action: "compensation." + record.Status,
			Actor: task.AgentTeamsTaskID,
			Details: map[string]any{
				"task": task.ID, "action": record.Action,
				"connector": record.Connector, "tool": record.Tool,
				"receipt_count": len(record.Receipts), "error": record.Error,
			},
		})
		return nil
	})
}

func (r *Registry) EnsureTaskApproval(
	orderID string,
	taskReference string,
	requestedBy string,
	skills []string,
	tools []string,
	connectors []string,
	now time.Time,
) (WorkOrder, ApprovalRecord, bool, error) {
	var record ApprovalRecord
	approved := false
	order, err := r.UpdateOrder(orderID, func(order *WorkOrder) error {
		taskIndex := officeTaskReferenceIndex(*order, taskReference)
		if taskIndex < 0 {
			return fmt.Errorf("office task %q not found", taskReference)
		}
		task := &order.Tasks[taskIndex]
		if !RequiresApproval(task.Approval) {
			approved = true
			return nil
		}
		now = now.UTC()
		policyVersion := approvalPolicyVersion(task.Approval, skills, tools, connectors)
		if current := task.ApprovalRecord; current != nil {
			switch {
			case current.Status == "rejected":
				return ErrApprovalRejected
			case current.Status == "approved" && !now.After(current.ExpiresAt) &&
				current.PolicyVersion == policyVersion:
				record = *current
				approved = true
				return nil
			case current.Status == "pending" && !now.After(current.ExpiresAt) &&
				current.PolicyVersion == policyVersion:
				record = *current
				task.Status = "pending_approval"
				order.Status = "pending_approval"
				return nil
			case (current.Status == "pending" || current.Status == "approved") &&
				(now.After(current.ExpiresAt) || current.PolicyVersion != policyVersion):
				expired := *current
				expired.Status = "expired"
				expired.DecidedAt = timePointer(now)
				expired.DecidedBy = "system"
				task.ApprovalRecord = &expired
				order.Audit = append(order.Audit, AuditEvent{
					At: now, Action: "approval.expired", Actor: "system",
					Details: map[string]any{
						"task": task.ID, "approval_id": expired.ID,
						"policy_version": expired.PolicyVersion,
					},
				})
			}
		}
		approvalID, generateErr := randomID("approval_")
		if generateErr != nil {
			return generateErr
		}
		ttl := r.cfg.ApprovalTTL.Duration()
		if ttl <= 0 {
			ttl = 15 * time.Minute
		}
		record = ApprovalRecord{
			ID: approvalID, Status: "pending", Mode: task.Approval.Mode,
			Risk:           task.Approval.Risk,
			RequiredScopes: append([]string(nil), task.Approval.RequiredScopes...),
			Skills:         appendUniqueStrings(nil, skills...),
			Tools:          appendUniqueStrings(nil, tools...),
			Connectors:     appendUniqueStrings(nil, connectors...),
			PolicyVersion:  policyVersion,
			RequestedAt:    now, ExpiresAt: now.Add(ttl),
			RequestedBy: strings.TrimSpace(requestedBy),
		}
		if record.RequestedBy == "" {
			record.RequestedBy = "system"
		}
		task.ApprovalRecord = &record
		task.Status = "pending_approval"
		order.Status = "pending_approval"
		order.Audit = append(order.Audit, AuditEvent{
			At: now, Action: "approval.requested", Actor: record.RequestedBy,
			Details: map[string]any{
				"task": task.ID, "approval_id": record.ID,
				"mode": record.Mode, "risk": record.Risk,
				"required_scopes": record.RequiredScopes,
				"skills":          record.Skills, "tools": record.Tools, "connectors": record.Connectors,
				"policy_version": record.PolicyVersion, "expires_at": record.ExpiresAt,
			},
		})
		return nil
	})
	return order, record, approved, err
}

func (r *Registry) DecideTaskApproval(
	orderID string,
	taskReference string,
	approvalID string,
	decision string,
	decidedBy string,
	opinion string,
	now time.Time,
) (WorkOrder, error) {
	var decisionErr error
	order, err := r.UpdateOrder(orderID, func(order *WorkOrder) error {
		taskIndex := officeTaskReferenceIndex(*order, taskReference)
		if taskIndex < 0 {
			return fmt.Errorf("office task %q not found", taskReference)
		}
		task := &order.Tasks[taskIndex]
		if task.ApprovalRecord == nil || task.ApprovalRecord.ID != approvalID {
			return errors.New("approval record does not match the current Office task")
		}
		record := *task.ApprovalRecord
		now = now.UTC()
		if (decision == "approve" && record.Status == "approved") ||
			(decision == "reject" && record.Status == "rejected") {
			return nil
		}
		if record.Status != "pending" {
			return ErrApprovalAlreadyDecided
		}
		if now.After(record.ExpiresAt) {
			record.Status = "expired"
			record.DecidedAt = timePointer(now)
			record.DecidedBy = "system"
			task.ApprovalRecord = &record
			task.Status = "pending_approval"
			order.Status = "pending_approval"
			order.Audit = append(order.Audit, AuditEvent{
				At: now, Action: "approval.expired", Actor: "system",
				Details: map[string]any{"task": task.ID, "approval_id": record.ID},
			})
			decisionErr = ErrApprovalExpired
			return nil
		}
		record.DecidedAt = timePointer(now)
		record.DecidedBy = strings.TrimSpace(decidedBy)
		if record.DecidedBy == "" {
			record.DecidedBy = "admin"
		}
		record.Opinion = strings.TrimSpace(opinion)
		switch decision {
		case "approve":
			record.Status = "approved"
			task.Status = "pending"
			order.Status = "running"
			order.Audit = append(order.Audit, AuditEvent{
				At: now, Action: "approval.approved", Actor: record.DecidedBy,
				Details: map[string]any{
					"task": task.ID, "approval_id": record.ID,
					"opinion": record.Opinion, "policy_version": record.PolicyVersion,
				},
			})
		case "reject":
			record.Status = "rejected"
			task.Status = "rejected"
			task.Error = "approval rejected"
			task.CompletedAt = timePointer(now)
			order.Status = "rejected"
			order.Audit = append(order.Audit, AuditEvent{
				At: now, Action: "approval.rejected", Actor: record.DecidedBy,
				Details: map[string]any{
					"task": task.ID, "approval_id": record.ID,
					"opinion": record.Opinion, "policy_version": record.PolicyVersion,
				},
			})
		default:
			return errors.New("decision must be approve or reject")
		}
		task.ApprovalRecord = &record
		return nil
	})
	if err != nil {
		return WorkOrder{}, err
	}
	return order, decisionErr
}

func officeTaskReferenceIndex(order WorkOrder, reference string) int {
	for index, task := range order.Tasks {
		if task.ID == reference || task.StageID == reference {
			return index
		}
	}
	return -1
}

func approvalPolicyVersion(
	policy ApprovalPolicy,
	skills []string,
	tools []string,
	connectors []string,
) string {
	value := struct {
		Policy     ApprovalPolicy `json:"policy"`
		Skills     []string       `json:"skills"`
		Tools      []string       `json:"tools"`
		Connectors []string       `json:"connectors"`
	}{
		Policy:     policy,
		Skills:     appendUniqueStrings(nil, skills...),
		Tools:      appendUniqueStrings(nil, tools...),
		Connectors: appendUniqueStrings(nil, connectors...),
	}
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func appendUniqueStrings(values []string, additional ...string) []string {
	seen := make(map[string]bool, len(values)+len(additional))
	result := make([]string, 0, len(values)+len(additional))
	for _, value := range append(append([]string(nil), values...), additional...) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func timePointer(value time.Time) *time.Time {
	return &value
}
