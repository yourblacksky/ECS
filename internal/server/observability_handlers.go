package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ecs/internal/config"
	"ecs/internal/networkauth"
	"ecs/internal/trace"
)

func (s *Server) listTraces(w http.ResponseWriter, r *http.Request) {
	if s.traces == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"count": 0, "traces": []trace.Summary{},
		})
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			writeError(w, http.StatusBadRequest, "limit must be 1..200")
			return
		}
		limit = value
	}
	var since time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be RFC3339")
			return
		}
		since = value
	}
	var values []trace.Summary
	var err error
	if relationship, ok := r.Context().Value(networkRelationshipKey{}).(networkauth.PublicRelationship); ok {
		values, err = s.traces.ListOwned(relationship.PeerNodeID, limit, since)
	} else {
		values, err = s.traces.List(limit, since)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(values), "traces": values,
	})
}

func (s *Server) getMetrics(w http.ResponseWriter, _ *http.Request) {
	now := time.Now().UTC()
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "ecs.metrics/v1",
		"node":           s.cfg.NodeName,
		"runtime":        s.metrics.Snapshot(now),
		"peers":          s.peerScheduler.snapshot(),
		"trace_count":    s.traces.Count(),
	})
}

func (s *Server) aggregateTrace(
	ctx context.Context,
	value trace.Trace,
	depth int,
) trace.EvidenceBundle {
	root := trace.EvidenceNode{Node: value.Node, Trace: &value}
	if depth > 0 {
		for _, link := range value.Links {
			root.Children = append(root.Children, s.fetchLinkedTrace(ctx, link, depth-1))
		}
	}
	return trace.EvidenceBundle{
		Version: "ecs.trace.bundle/v1", TraceID: value.TraceID,
		GeneratedAt: time.Now().UTC(), Root: root,
	}
}

func (s *Server) fetchLinkedTrace(
	ctx context.Context,
	link trace.Link,
	depth int,
) trace.EvidenceNode {
	result := trace.EvidenceNode{Node: link.Node, URL: link.URL}
	peer, ok := s.tracePeer(link)
	if !ok {
		result.Error = "linked peer is no longer configured or authorized for trace:read"
		return result
	}
	peer = s.routedPeer(peer)
	endpoint := strings.TrimRight(peer.URL, "/") + "/v1/traces/" +
		url.PathEscape(link.TraceID) + "?aggregate=true&depth=" + strconv.Itoa(depth)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if peer.Token != "" {
		request.Header.Set("Authorization", "Bearer "+peer.Token)
	}
	response, err := s.client.Do(request)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, s.cfg.HTTP.MaxBodyBytes))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if response.StatusCode == http.StatusNotFound {
		result.Error = "linked trace not found"
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Error = response.Status + ": " + strings.TrimSpace(string(data))
		return result
	}
	var bundle trace.EvidenceBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		result.Error = err.Error()
		return result
	}
	return bundle.Root
}

func (s *Server) tracePeer(link trace.Link) (config.Peer, bool) {
	var peers []config.Peer
	for _, group := range s.cfg.Groups {
		peers = append(peers, group...)
	}
	peers = append(peers, s.cfg.Nested.Children...)
	if s.network != nil {
		peers = append(peers, s.network.PeersFor("all", nil, nil, "trace:read")...)
	}
	for _, peer := range uniquePeers(peers) {
		if peer.Name == link.Node ||
			strings.TrimRight(peer.URL, "/") == strings.TrimRight(link.URL, "/") {
			return peer, true
		}
	}
	return config.Peer{}, false
}
