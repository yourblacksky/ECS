package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/networkauth"
	"ecs/internal/observability"
	"ecs/internal/trace"
)

func TestAggregateTraceFetchesAuthorizedPeerEvidence(t *testing.T) {
	childTrace := trace.Trace{
		Version:   "ecs.trace/v1",
		TraceID:   "shared-trace",
		RequestID: "child-request",
		Node:      "worker-a",
		Status:    "success",
		Root:      &trace.Span{SpanID: "span-1", Status: "success"},
	}
	child := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces/shared-trace" ||
			r.Header.Get("Authorization") != "Bearer worker-token" {
			t.Fatalf("unexpected linked trace request: %s auth=%q", r.URL, r.Header.Get("Authorization"))
		}
		writeJSON(w, http.StatusOK, trace.EvidenceBundle{
			Version: "ecs.trace.bundle/v1", TraceID: childTrace.TraceID,
			GeneratedAt: time.Now().UTC(),
			Root:        trace.EvidenceNode{Node: childTrace.Node, Trace: &childTrace},
		})
	}))
	defer child.Close()

	server := &Server{
		cfg: config.Config{
			HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20},
			Groups: map[string][]config.Peer{
				"demo": {{Name: "worker-a", URL: child.URL, Token: "worker-token"}},
			},
		},
		client: child.Client(),
	}
	root := trace.Trace{
		Version: "ecs.trace/v1", TraceID: "shared-trace", Node: "controller",
		Root: &trace.Span{SpanID: "span-1", Status: "success"},
		Links: []trace.Link{
			{Node: "worker-a", URL: child.URL, TraceID: "shared-trace"},
		},
	}
	bundle := server.aggregateTrace(context.Background(), root, 2)
	if len(bundle.Root.Children) != 1 ||
		bundle.Root.Children[0].Trace == nil ||
		bundle.Root.Children[0].Trace.Node != "worker-a" {
		t.Fatalf("linked evidence was not aggregated: %#v", bundle)
	}
}

func TestTraceHandlersIsolateNetworkPrincipals(t *testing.T) {
	store, err := trace.NewStore(config.TraceConfig{
		Enabled: true, Directory: t.TempDir(), MaxValueChars: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"peer-a", "peer-b"} {
		recorder := store.StartOwned(owner, "trace-"+owner, "request-"+owner, "node", "/v1/reply", nil)
		recorder.Finish("success", owner, nil)
	}
	server := &Server{traces: store}
	ctx := context.WithValue(context.Background(), networkRelationshipKey{}, networkauth.PublicRelationship{PeerNodeID: "peer-a"})

	request := httptest.NewRequest(http.MethodGet, "/v1/traces", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	server.listTraces(response, request)
	var list struct {
		Count  int             `json:"count"`
		Traces []trace.Summary `json:"traces"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 1 || list.Traces[0].TraceID != "trace-peer-a" {
		t.Fatalf("trace list crossed principal boundary: %#v", list)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/traces/trace-peer-b", nil).WithContext(ctx)
	request.SetPathValue("trace", "trace-peer-b")
	response = httptest.NewRecorder()
	server.getTrace(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("another principal read trace details: %d %s", response.Code, response.Body.String())
	}
}

func TestTraceListAndMetricsHandlers(t *testing.T) {
	store, err := trace.NewStore(config.TraceConfig{
		Enabled: true, Directory: t.TempDir(), MaxValueChars: 1000,
		MaxTraces: 10, Retention: config.Duration(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := store.Start("trace-visible", "request-visible", "node-a", "/v1/reply", nil)
	recorder.Finish("success", "done", nil)
	server := &Server{
		cfg:           config.Config{NodeName: "node-a"},
		traces:        store,
		metrics:       observability.NewMetrics(),
		peerScheduler: newPeerScheduler(),
	}

	response := httptest.NewRecorder()
	server.listTraces(response, httptest.NewRequest(http.MethodGet, "/v1/traces?limit=1", nil))
	var list struct {
		Count  int             `json:"count"`
		Traces []trace.Summary `json:"traces"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || list.Count != 1 ||
		list.Traces[0].TraceID != "trace-visible" {
		t.Fatalf("unexpected trace list: %d %#v", response.Code, list)
	}

	response = httptest.NewRecorder()
	server.listTraces(response, httptest.NewRequest(http.MethodGet, "/v1/traces?since=invalid", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid since was accepted: %d", response.Code)
	}

	response = httptest.NewRecorder()
	server.getMetrics(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	var metrics map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics["node"] != "node-a" || metrics["trace_count"].(float64) != 1 {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
}
