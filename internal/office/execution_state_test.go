package office

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTaskAttemptAndOutboxAreDurableBeforeDispatch(t *testing.T) {
	registry := newTestRegistry(t, validTestManifest())
	order, err := registry.Compile(
		"brief",
		CompileRequest{Input: json.RawMessage(`{"topic":"durable execution"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	order, attempt, event, err := registry.ClaimTask(order.ID, "plan", TaskClaim{
		Node: "worker-a", TraceID: "trace-attempt-1",
		IdempotencyKey: "office:" + order.ID + ":plan",
		Skills:         []string{"calculate"}, Tools: []string{"calculate"},
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Status != AttemptClaimed || event.Status != OutboxPending ||
		order.Tasks[0].ActiveAttemptID != attempt.ID ||
		order.Outbox[0].AttemptID != attempt.ID {
		t.Fatalf("claim and outbox were not committed together: %#v %#v %#v", order.Tasks[0], attempt, event)
	}
	persisted, err := registry.LoadOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Tasks[0].Attempts) != 1 || len(persisted.Outbox) != 1 ||
		persisted.Outbox[0].Status != OutboxPending {
		t.Fatalf("durable intent was not persisted before dispatch: %#v", persisted)
	}
	persisted, err = registry.MarkAttemptDispatched(
		order.ID, "plan", attempt.ID, now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Tasks[0].Attempts[0].Status != AttemptDispatched ||
		persisted.Outbox[0].Status != OutboxDispatched {
		t.Fatalf("dispatch boundary was not persisted: %#v", persisted)
	}
	persisted, err = registry.CompleteTaskAttempt(
		order.ID, "plan", attempt.ID, TaskAttemptResult{
			Status: "completed", Result: json.RawMessage(`{"result":"ok"}`),
			Actor: "worker-a", CompletedAt: now.Add(2 * time.Second),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Tasks[0].Status != "completed" ||
		persisted.Tasks[0].Attempts[0].Status != AttemptSucceeded ||
		persisted.Tasks[0].ActiveAttemptID != "" ||
		persisted.Outbox[0].Status != OutboxConfirmed ||
		persisted.Outbox[0].ConfirmedAt == nil {
		t.Fatalf("attempt and outbox were not confirmed together: %#v", persisted)
	}
	if _, _, _, err := registry.ClaimTask(order.ID, "research", TaskClaim{
		Node: "worker-b", TraceID: "trace-attempt-2",
		IdempotencyKey: "office:" + order.ID + ":research",
		Now:            now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("dependent task could not be claimed after its predecessor completed: %v", err)
	}
}

func TestConcurrentTaskClaimAllowsOneAttempt(t *testing.T) {
	registry := newTestRegistry(t, validTestManifest())
	order, err := registry.Compile(
		"brief",
		CompileRequest{Input: json.RawMessage(`{"topic":"single claim"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, node := range []string{"worker-a", "worker-b"} {
		wait.Add(1)
		go func(node string) {
			defer wait.Done()
			<-start
			_, _, _, claimErr := registry.ClaimTask(order.ID, "plan", TaskClaim{
				Node: node, TraceID: "trace-" + node,
				IdempotencyKey: "office:" + order.ID + ":plan", Now: now,
			})
			results <- claimErr
		}(node)
	}
	close(start)
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for claimErr := range results {
		switch {
		case claimErr == nil:
			successes++
		case errors.Is(claimErr, ErrTaskAlreadyClaimed):
			conflicts++
		default:
			t.Fatalf("unexpected claim error: %v", claimErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one durable claim, successes=%d conflicts=%d", successes, conflicts)
	}
	persisted, err := registry.LoadOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Tasks[0].AttemptCount != 1 ||
		len(persisted.Tasks[0].Attempts) != 1 || len(persisted.Outbox) != 1 {
		t.Fatalf("concurrent claim created duplicate durable state: %#v", persisted)
	}
}

func TestExpiredAttemptIsReapedAndCanResumeWithSameIdempotencyKey(t *testing.T) {
	registry := newTestRegistry(t, validTestManifest())
	order, err := registry.Compile(
		"brief",
		CompileRequest{Input: json.RawMessage(`{"topic":"recover after crash"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	claimedAt := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	idempotencyKey := "office:" + order.ID + ":plan"
	_, first, _, err := registry.ClaimTask(order.ID, "plan", TaskClaim{
		Node: "worker-a", TraceID: "trace-first",
		IdempotencyKey: idempotencyKey, Now: claimedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.MarkAttemptDispatched(
		order.ID, "plan", first.ID, claimedAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	recovered, err := registry.ReapExpiredAttempts(
		claimedAt.Add(defaultOfficeTaskLease + time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].AttemptID != first.ID {
		t.Fatalf("expired attempt was not reaped: %#v", recovered)
	}
	persisted, err := registry.LoadOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != StatusRecoveryRequired ||
		persisted.Tasks[0].Status != StatusRecoveryRequired ||
		persisted.Tasks[0].Attempts[0].Status != AttemptOutcomeUnknown ||
		persisted.Outbox[0].Status != OutboxRecoveryRequired {
		t.Fatalf("recovery state was not persisted: %#v", persisted)
	}
	resumedAt := claimedAt.Add(defaultOfficeTaskLease + 2*time.Second)
	persisted, second, secondEvent, err := registry.ClaimTask(
		order.ID, "plan", TaskClaim{
			Node: "worker-b", TraceID: "trace-second",
			IdempotencyKey: idempotencyKey, Now: resumedAt,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Number != 2 || second.ID == first.ID ||
		second.IdempotencyKey != first.IdempotencyKey ||
		secondEvent.Status != OutboxPending ||
		persisted.Tasks[0].AttemptCount != 2 ||
		len(persisted.Outbox) != 2 {
		t.Fatalf("recovery did not create a new durable attempt: %#v %#v", persisted, second)
	}
	if _, err := registry.MarkAttemptDispatched(
		order.ID, "plan", second.ID, resumedAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	persisted, err = registry.CompleteTaskAttempt(
		order.ID, "plan", second.ID, TaskAttemptResult{
			Status: "completed", Actor: "worker-b",
			CompletedAt: resumedAt.Add(2 * time.Second),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Outbox[0].Status != OutboxSuperseded ||
		persisted.Outbox[1].Status != OutboxConfirmed {
		t.Fatalf("successful recovery did not resolve the old outbox event: %#v", persisted.Outbox)
	}
}

func TestAttemptLeaseRenewalPreventsPrematureRecovery(t *testing.T) {
	registry := newTestRegistry(t, validTestManifest())
	order, err := registry.Compile(
		"brief",
		CompileRequest{Input: json.RawMessage(`{"topic":"long running task"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	claimedAt := time.Date(2026, 7, 31, 12, 30, 0, 0, time.UTC)
	_, attempt, _, err := registry.ClaimTask(order.ID, "plan", TaskClaim{
		Node: "worker-a", TraceID: "trace-renew",
		IdempotencyKey: "office:" + order.ID + ":plan", Now: claimedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	renewedAt := claimedAt.Add(time.Minute)
	if _, err := registry.RenewTaskAttempt(
		order.ID, "plan", attempt.ID, renewedAt,
	); err != nil {
		t.Fatal(err)
	}
	recovered, err := registry.ReapExpiredAttempts(
		claimedAt.Add(defaultOfficeTaskLease + time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 0 {
		t.Fatalf("renewed attempt was reaped using its old deadline: %#v", recovered)
	}
	recovered, err = registry.ReapExpiredAttempts(
		renewedAt.Add(defaultOfficeTaskLease + time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].AttemptID != attempt.ID {
		t.Fatalf("renewed attempt was not reaped after its new deadline: %#v", recovered)
	}
}

func TestUnknownOutcomeMovesAttemptToRecoveryWithoutCompensation(t *testing.T) {
	registry := newTestRegistry(t, validTestManifest())
	order, err := registry.Compile(
		"brief",
		CompileRequest{Input: json.RawMessage(`{"topic":"unknown outcome"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 13, 0, 0, 0, time.UTC)
	_, attempt, _, err := registry.ClaimTask(order.ID, "plan", TaskClaim{
		Node: "worker-a", TraceID: "trace-unknown",
		IdempotencyKey: "office:" + order.ID + ":plan", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := registry.MarkAttemptRecoveryRequired(
		order.ID, "plan", attempt.ID, "connection closed before response", now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	task := persisted.Tasks[0]
	if task.Status != StatusRecoveryRequired ||
		task.CompensationRecord != nil ||
		task.Attempts[0].Status != AttemptOutcomeUnknown ||
		persisted.Outbox[0].Status != OutboxRecoveryRequired {
		t.Fatalf("unknown outcome was treated as a final failure: %#v", persisted)
	}
}

func TestReaperMigratesLegacyRunningTaskToRecovery(t *testing.T) {
	registry := newTestRegistry(t, validTestManifest())
	order, err := registry.Compile(
		"brief",
		CompileRequest{Input: json.RawMessage(`{"topic":"legacy running task"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.UpdateOrder(order.ID, func(current *WorkOrder) error {
		current.Status = "running"
		current.Tasks[0].Status = "running"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := registry.ReapExpiredAttempts(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].AttemptID != "" ||
		recovered[0].Reason != legacyRunningTaskText {
		t.Fatalf("legacy running task was not migrated: %#v", recovered)
	}
	persisted, err := registry.LoadOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != StatusRecoveryRequired ||
		persisted.Tasks[0].Status != StatusRecoveryRequired {
		t.Fatalf("legacy task recovery state was not persisted: %#v", persisted)
	}
}
