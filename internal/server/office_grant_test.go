package server

import (
	"testing"
	"time"

	"ecs/internal/office"
)

func TestOfficeExecutionGrantBindsApprovalAndTaskRequest(t *testing.T) {
	request := officeTaskExecutionRequest{
		RequestID: "attempt-1", TraceID: "trace-1", WorkOrder: "order-1", TaskID: "task-1",
		Prompt: "dispatch", Skills: []string{"dispatch_skill"}, Tools: []string{"connector_tool"},
		Connectors: []string{"office.task.write"}, IdempotencyKey: "order-1.task-1",
		Approval: &office.ApprovalRecord{
			ID: "approval-1", Status: "approved", PolicyVersion: "policy-v1",
			ExpiresAt: time.Now().UTC().Add(time.Minute),
		},
	}
	grant, err := signOfficeExecutionGrant("shared-secret", "controller-id", "worker-id", request)
	if err != nil {
		t.Fatal(err)
	}
	request.ApprovalGrant = grant
	if err := verifyOfficeExecutionGrant(
		"shared-secret", "controller-id", "worker-id", "worker", request,
	); err != nil {
		t.Fatalf("valid grant was rejected: %v", err)
	}
	tampered := request
	tampered.Tools = []string{"admin_tool"}
	if err := verifyOfficeExecutionGrant(
		"shared-secret", "controller-id", "worker-id", "worker", tampered,
	); err == nil {
		t.Fatal("grant remained valid after tool escalation")
	}
	tampered = request
	tampered.Approval.PolicyVersion = "forged-policy"
	if err := verifyOfficeExecutionGrant(
		"shared-secret", "controller-id", "worker-id", "worker", tampered,
	); err == nil {
		t.Fatal("grant remained valid after approval mutation")
	}
}
