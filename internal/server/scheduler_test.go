package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ecs/internal/config"
	"ecs/internal/discovery"
	"ecs/internal/protocol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPeerSchedulerUsesStableRoundRobinForEqualPeers(t *testing.T) {
	scheduler := newPeerScheduler()
	peers := []config.Peer{
		{NodeID: "node-c", Name: "c", URL: "http://c"},
		{NodeID: "node-a", Name: "a", URL: "http://a"},
		{NodeID: "node-b", Name: "b", URL: "http://b"},
	}
	var selected []string
	for range peers {
		peer := scheduler.selectAndReserve(peers, dispatchSingle, 1)[0]
		selected = append(selected, peer.Name)
		scheduler.complete(peer, 0, false, false)
	}
	want := []string{"a", "b", "c"}
	for index := range want {
		if selected[index] != want[index] {
			t.Fatalf("round robin selections = %v, want %v", selected, want)
		}
	}
}

func TestNormalizeDispatchUsesDefaultsAndValidatesRaceFanout(t *testing.T) {
	mode, fanout, ok := normalizeDispatch("", 0, dispatchRace, 3)
	if !ok || mode != dispatchRace || fanout != 3 {
		t.Fatalf("default dispatch = %q/%d/%v", mode, fanout, ok)
	}
	if _, _, ok := normalizeDispatch("unknown", 0, dispatchSingle, 2); ok {
		t.Fatal("unknown dispatch mode was accepted")
	}
	if _, _, ok := normalizeDispatch(dispatchRace, 33, dispatchSingle, 2); ok {
		t.Fatal("excessive race fanout was accepted")
	}
}

func TestPeerSchedulerAvoidsBusyAndUnhealthyPeers(t *testing.T) {
	scheduler := newPeerScheduler()
	peers := []config.Peer{
		{NodeID: "node-a", Name: "a", URL: "http://a"},
		{NodeID: "node-b", Name: "b", URL: "http://b"},
	}
	first := scheduler.selectAndReserve(peers, dispatchSingle, 1)[0]
	second := scheduler.selectAndReserve(peers, dispatchSingle, 1)[0]
	if first.Name == second.Name {
		t.Fatalf("busy peer %q was selected again", first.Name)
	}
	scheduler.complete(first, 100*time.Millisecond, false, true)
	scheduler.complete(second, 100*time.Millisecond, true, true)
	selected := scheduler.selectAndReserve(peers, dispatchSingle, 1)[0]
	defer scheduler.complete(selected, 0, false, false)
	if selected.Name != second.Name {
		t.Fatalf("healthy peer selection = %q, want %q", selected.Name, second.Name)
	}
}

func TestPeerFailurePenaltyExpires(t *testing.T) {
	stats := peerDispatchStats{
		failures:    1,
		lastFailure: time.Now().Add(-peerFailureCooldown),
	}
	if penalty := recentFailurePermille(stats, time.Now()); penalty != 0 {
		t.Fatalf("expired failure penalty = %d", penalty)
	}
}

func TestDispatchGroupRaceReturnsFirstSuccessAndCancelsLoser(t *testing.T) {
	slowCanceled := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Host, "slow") {
			<-request.Context().Done()
			close(slowCanceled)
			return nil, request.Context().Err()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"request_id":"request-1","node":"fast","message":{"role":"assistant","content":"done"}}`,
			)),
			Header: make(http.Header),
		}, nil
	})}

	server := &Server{
		cfg:           config.Config{HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20}},
		client:        client,
		peerScheduler: newPeerScheduler(),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	peers := []config.Peer{
		{Name: "slow", URL: "http://slow.test"},
		{Name: "fast", URL: "http://fast.test"},
	}
	server.peerScheduler.selectAndReserve(peers, dispatchRace, 2)
	result := protocol.GroupResponse{Results: make([]protocol.PeerResult, len(peers))}
	server.dispatchGroupRace(
		context.Background(),
		nil,
		"",
		peers,
		protocol.ReplyRequest{RequestID: "request-1", Prompt: "work"},
		&result,
	)
	if result.Winner != "fast" {
		t.Fatalf("race winner = %q, want fast; results=%#v", result.Winner, result.Results)
	}
	select {
	case <-slowCanceled:
	case <-time.After(time.Second):
		t.Fatal("slow peer did not observe cancellation")
	}
}

func TestGroupReplyDefaultsToSingleAndAllowsBroadcastOverride(t *testing.T) {
	var calls atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		node := request.URL.Host
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"request_id":"request-1","node":"` + node + `","message":{"role":"assistant","content":"done"}}`,
			)),
			Header: make(http.Header),
		}, nil
	})}
	cfg := config.Config{
		Agent: config.AgentConfig{
			GroupDispatchMode: dispatchSingle,
			GroupRaceFanout:   2,
		},
		HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20},
		Groups: map[string][]config.Peer{
			"workers": {
				{NodeID: "node-a", Name: "a", URL: "http://a.test"},
				{NodeID: "node-b", Name: "b", URL: "http://b.test"},
			},
		},
	}
	server := &Server{
		cfg:           cfg,
		client:        client,
		discovery:     discovery.New(cfg.Discovery, cfg.Nested, "parent", "", "", "off", nil, nil),
		peerScheduler: newPeerScheduler(),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	invoke := func(body string) protocol.GroupResponse {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/v1/groups/workers/reply", bytes.NewBufferString(body))
		request.SetPathValue("group", "workers")
		response := httptest.NewRecorder()
		server.groupReply(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("group reply status %d: %s", response.Code, response.Body.String())
		}
		var result protocol.GroupResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	single := invoke(`{"request_id":"request-1","prompt":"work"}`)
	if calls.Load() != 1 || single.DispatchMode != dispatchSingle ||
		len(single.Selected) != 1 || len(single.Results) != 1 {
		t.Fatalf("unexpected single dispatch: calls=%d result=%#v", calls.Load(), single)
	}
	broadcast := invoke(`{"request_id":"request-2","prompt":"work","dispatch_mode":"broadcast"}`)
	if calls.Load() != 3 || broadcast.DispatchMode != dispatchBroadcast ||
		len(broadcast.Selected) != 2 || len(broadcast.Results) != 2 {
		t.Fatalf("unexpected broadcast dispatch: calls=%d result=%#v", calls.Load(), broadcast)
	}
}
