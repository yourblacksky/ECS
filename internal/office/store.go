package office

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (r *Registry) ListOrders(component, status, ownerPeerNodeID string, limit int) ([]WorkOrderSummary, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	entries, err := os.ReadDir(r.cfg.OrderDirectory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []WorkOrderSummary{}, nil
		}
		return nil, err
	}
	result := make([]WorkOrderSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.cfg.OrderDirectory, entry.Name()))
		if err != nil {
			return nil, err
		}
		var order WorkOrder
		if err := json.Unmarshal(data, &order); err != nil {
			return nil, fmt.Errorf("decode work order %s: %w", entry.Name(), err)
		}
		if component != "" && order.Component != component {
			continue
		}
		if status != "" && order.Status != status {
			continue
		}
		if !OwnedBy(order, ownerPeerNodeID) {
			continue
		}
		pending, approvals, pendingApprovals := 0, 0, 0
		activeAttempts, recoveryTasks := 0, 0
		for _, task := range order.Tasks {
			if task.Status == "pending" || task.Status == "running" ||
				task.Status == "pending_approval" || task.Status == StatusRecoveryRequired {
				pending++
			}
			if task.Status == StatusRecoveryRequired {
				recoveryTasks++
			}
			if attempt := activeAttempt(&task); attempt != nil && activeAttemptStatus(attempt.Status) {
				activeAttempts++
			}
			if task.Approval.Mode == "policy" || task.Approval.Mode == "human" {
				approvals++
			}
			if task.ApprovalRecord != nil && task.ApprovalRecord.Status == "pending" {
				pendingApprovals++
			}
		}
		pendingOutbox := 0
		for _, event := range order.Outbox {
			if event.Status == OutboxPending || event.Status == OutboxDispatched ||
				event.Status == OutboxRecoveryRequired {
				pendingOutbox++
			}
		}
		result = append(result, WorkOrderSummary{
			ID: order.ID, Component: order.Component, ComponentVersion: order.ComponentVersion,
			Status: order.Status, TraceID: order.TraceID, ExecutionTraceID: order.ExecutionTraceID,
			CreatedAt:   order.CreatedAt,
			RequestedBy: order.RequestedBy, OwnerPeerNodeID: order.OwnerPeerNodeID, Ready: order.Ready,
			MissingConnectors: append([]string(nil), order.MissingConnectors...),
			Tasks:             len(order.Tasks), PendingTasks: pending, ApprovalTasks: approvals,
			PendingApprovals: pendingApprovals, ActiveAttempts: activeAttempts,
			PendingOutbox: pendingOutbox, RecoveryTasks: recoveryTasks,
			Handoffs: len(order.Handoffs),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// OwnedBy treats work orders created before ownership metadata as local-only.
func OwnedBy(order WorkOrder, ownerPeerNodeID string) bool {
	owner := order.OwnerPeerNodeID
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

func (r *Registry) saveOrder(order WorkOrder) error {
	if err := os.MkdirAll(r.cfg.OrderDirectory, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(order, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(r.cfg.OrderDirectory, ".office-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, filepath.Join(r.cfg.OrderDirectory, order.ID+".json"))
}
