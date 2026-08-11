package trace

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"ecs/internal/config"
)

func TestRecorderPersistsTreeAndConcurrentChildren(t *testing.T) {
	store, err := NewStore(config.TraceConfig{
		Enabled:       true,
		Directory:     t.TempDir(),
		MaxValueChars: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := store.Start("request-1", "request-1", "node-a", "/v1/recursive/reply", map[string]any{
		"prompt": "do work",
	})
	planner := recorder.StartSpan(recorder.RootID(), "planner", "plan", map[string]any{"agent": "worker"})
	recorder.FinishSpan(planner, "success", "two tasks", nil, nil)

	var wait sync.WaitGroup
	for _, name := range []string{"executor-a", "nested-b"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			span := recorder.StartSpan(planner, "executor", name, map[string]any{"depth": 1})
			recorder.FinishSpan(span, "success", map[string]string{"result": name}, nil, nil)
		}()
	}
	wait.Wait()
	verifier := recorder.StartSpan(planner, "verifier", "accept", nil)
	recorder.FinishSpan(verifier, "accepted", "ok", nil, map[string]any{"validation_result": "accepted"})
	recorder.Finish("success", "final answer", nil)

	loaded, err := store.Load("request-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "success" || loaded.Root == nil || len(loaded.Root.Children) != 1 {
		t.Fatalf("unexpected trace root: %#v", loaded)
	}
	if len(loaded.Root.Children[0].Children) != 3 {
		t.Fatalf("concurrent trace children were lost: %#v", loaded.Root.Children[0].Children)
	}
}

func TestStartNeverOverwritesExistingTraceID(t *testing.T) {
	store, err := NewStore(config.TraceConfig{
		Enabled: true, Directory: t.TempDir(), MaxValueChars: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := store.Start("caller-trace", "request-a", "node-a", "/v1/reply", nil)
	first.Finish("success", "first evidence", nil)
	second := store.Start("caller-trace", "request-b", "node-a", "/v1/reply", nil)
	if second == nil || second.TraceID() == "caller-trace" {
		t.Fatalf("colliding caller trace id was reused: %#v", second)
	}
	second.Finish("success", "second evidence", nil)
	loaded, err := store.Load("caller-trace")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RequestID != "request-a" || loaded.Summary != "first evidence" {
		t.Fatalf("existing audit evidence was overwritten: %#v", loaded)
	}
	secondLoaded, err := store.Load(second.TraceID())
	if err != nil || secondLoaded.RequestID != "request-b" {
		t.Fatalf("colliding trace was not stored separately: %#v err=%v", secondLoaded, err)
	}
}

func TestOwnedTraceQueriesDoNotCrossPrincipals(t *testing.T) {
	store, err := NewStore(config.TraceConfig{
		Enabled: true, Directory: t.TempDir(), MaxValueChars: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := store.StartOwned("peer-a", "trace-a", "request-a", "node", "/v1/reply", nil)
	a.Finish("success", "a", nil)
	b := store.StartOwned("peer-b", "trace-b", "request-b", "node", "/v1/reply", nil)
	b.Finish("success", "b", nil)
	if _, err := store.LoadOwned("peer-b", "trace-a"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("another principal loaded trace evidence: %v", err)
	}
	values, err := store.ListOwned("peer-a", 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].TraceID != "trace-a" {
		t.Fatalf("owned trace list leaked another principal: %#v", values)
	}
}

func TestStoreListsLinksAndPrunesCompletedTraces(t *testing.T) {
	store, err := NewStore(config.TraceConfig{
		Enabled: true, Directory: t.TempDir(), MaxValueChars: 200,
		MaxTraces: 2, Retention: config.Duration(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"trace-a", "trace-b", "trace-c"} {
		recorder := store.Start(id, "request-"+id, "node-a", "/v1/reply", nil)
		if id == "trace-c" {
			recorder.AddLink(Link{
				Node: "node-b", URL: "http://node-b", TraceID: "remote-c",
			})
		}
		recorder.Finish("success", id, nil)
		time.Sleep(time.Millisecond)
	}
	values, err := store.List(10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].TraceID != "trace-c" ||
		values[0].LinkCount != 1 || values[1].TraceID != "trace-b" {
		t.Fatalf("unexpected retained traces: %#v", values)
	}
	if _, err := store.Load("trace-a"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest completed trace was not pruned: %v", err)
	}
	if count := store.Count(); count != 2 {
		t.Fatalf("trace count = %d, want 2", count)
	}
}

func TestStorePrunesByCompletionRetention(t *testing.T) {
	store, err := NewStore(config.TraceConfig{
		Enabled: true, Directory: t.TempDir(), MaxValueChars: 200,
		MaxTraces: 10, Retention: config.Duration(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := store.Start("expired-trace", "expired-request", "node-a", "/v1/reply", nil)
	recorder.Finish("success", "done", nil)
	recorder.mu.Lock()
	expired := time.Now().UTC().Add(-2 * time.Hour)
	recorder.trace.CompletedAt = &expired
	if err := recorder.persistLocked(); err != nil {
		recorder.mu.Unlock()
		t.Fatal(err)
	}
	recorder.mu.Unlock()
	if err := store.Prune(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("expired-trace"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired completed trace was not pruned: %v", err)
	}
}

func TestRecorderPersistsErrorsAndTruncatesValues(t *testing.T) {
	store, err := NewStore(config.TraceConfig{
		Enabled:       true,
		Directory:     t.TempDir(),
		MaxValueChars: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := store.Start("request-2", "request-2", "node-a", "/v1/reply", "1234567890123456789012345")
	span := recorder.StartSpan(recorder.RootID(), "tool", "broken", nil)
	recorder.FinishSpan(span, "error", nil, errors.New("tool failed"), nil)
	running, err := store.Load("request-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(running.Root.Children) != 0 {
		t.Fatalf("in-progress span caused a trace rewrite: %#v", running.Root.Children)
	}
	recorder.Finish("error", "", errors.New("request failed"))
	loaded, err := store.Load("request-2")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Error != "request failed" || loaded.Root.Children[0].Error != "tool failed" {
		t.Fatalf("errors were not persisted: %#v", loaded)
	}
	if value, ok := loaded.Root.Input.(string); !ok || len([]rune(value)) > 20 {
		t.Fatalf("trace value was not truncated: %#v", loaded.Root.Input)
	}
}
