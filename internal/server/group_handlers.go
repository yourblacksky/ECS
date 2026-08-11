package server

import (
	"bytes"
	"context"
	"ecs/internal/config"
	"ecs/internal/protocol"
	"ecs/internal/trace"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

func (s *Server) groupReply(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("group")
	request, decoded := s.decodeRequest(w, r)
	if !decoded {
		return
	}
	if request.RequestID == "" {
		request.RequestID = requestID()
	}
	principal := requestOwner(r.Context())
	initialBounded, err := boundRequest(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	request = initialBounded.Request()
	peers := s.groupPeersFor(groupName, request)
	if len(peers) == 0 {
		writeError(w, http.StatusNotFound, "group has no online peer covering the requested capabilities")
		return
	}
	dispatchMode, dispatchFanout, valid := normalizeDispatch(
		request.DispatchMode,
		request.DispatchFanout,
		s.cfg.Agent.GroupDispatchMode,
		s.cfg.Agent.GroupRaceFanout,
	)
	if !valid {
		writeError(w, http.StatusBadRequest, "dispatch_mode must be single, broadcast, or race and dispatch_fanout must be 1..32")
		return
	}
	if !trace.IsValidID(request.TraceID) {
		request.TraceID = trace.NewID()
	}
	recorder := s.traces.StartOwned(principal, request.TraceID, request.RequestID, s.cfg.NodeName, "/v1/groups/"+groupName+"/reply", map[string]any{
		"request": request,
		"group":   groupName,
		"depth":   0,
	})
	if recorder.TraceID() != "" {
		request.TraceID = recorder.TraceID()
	}
	newHistory := requestHistory(request)
	if request.SessionID != "" {
		conversation, err := s.sessions.Load(principal, request.SessionID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			recorder.Finish("error", "", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err == nil {
			request.Messages = append(contextWindow(conversation.Messages, s.cfg.Sessions.ContextMessages, s.cfg.Sessions.MaxContextChars), request.Messages...)
		}
	}
	selected := s.peerScheduler.selectAndReserve(peers, dispatchMode, dispatchFanout)
	plannerSpan := recorder.StartSpan(recorder.RootID(), "planner", "group-dispatch", map[string]any{
		"group":           groupName,
		"dispatch_mode":   dispatchMode,
		"candidate_count": len(peers),
		"selected_count":  len(selected),
	})
	recorder.FinishSpan(plannerSpan, "success", map[string]any{
		"candidates": peerNames(peers),
		"selected":   peerNames(selected),
	}, nil, nil)
	traceContext := trace.WithRecorder(r.Context(), recorder, plannerSpan)
	peerRequest := request
	peerRequest.SessionID = ""
	peerRequest.DispatchMode = ""
	peerRequest.DispatchFanout = 0
	result := protocol.GroupResponse{
		RequestID:    request.RequestID,
		TraceID:      request.TraceID,
		Group:        groupName,
		DispatchMode: dispatchMode,
		Candidates:   peerNames(peers),
		Selected:     peerNames(selected),
		Results:      make([]protocol.PeerResult, len(selected)),
	}
	if dispatchMode == dispatchRace {
		s.dispatchGroupRace(traceContext, recorder, plannerSpan, selected, peerRequest, &result)
	} else {
		s.dispatchGroupAll(traceContext, recorder, plannerSpan, selected, peerRequest, &result)
	}
	var synthesisErr error
	if s.cfg.Agent.GroupSynthesis {
		synthesisResults := result.Results
		if dispatchMode == dispatchRace && result.Winner != "" {
			for _, peerResult := range result.Results {
				if peerResult.Peer == result.Winner && peerResult.Error == "" {
					synthesisResults = []protocol.PeerResult{peerResult}
					break
				}
			}
		}
		bounded, boundErr := boundRequest(traceContext, request)
		if boundErr != nil {
			synthesisErr = boundErr
			s.logger.Error("group synthesis authorization failed", "request_id", request.RequestID, "error", boundErr)
		} else {
			summary, verification, err := s.agent.SummarizeWithVerification(traceContext, bounded, synthesisResults)
			if err != nil {
				synthesisErr = err
				s.logger.Error("group synthesis failed", "request_id", request.RequestID, "error", err)
			} else {
				result.Summary = &summary
				result.Verification = verification
				s.metrics.ObserveVerification(verification)
				if request.SessionID != "" {
					newHistory = append(newHistory, summary)
					if _, err := s.sessions.Append(principal, request.SessionID, newHistory...); err != nil {
						recorder.Event(plannerSpan, "error", "session-save", "error", map[string]any{"error": err.Error()})
						s.logger.Error("group session save failed", "session_id", request.SessionID, "error", err)
					}
				}
			}
		}
	}
	if synthesisErr != nil {
		recorder.Finish("error", "", synthesisErr)
	} else if result.Summary != nil {
		recorder.Finish("success", result.Summary.Content, nil)
	} else {
		recorder.Finish("completed", "", nil)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) dispatchGroupAll(
	ctx context.Context,
	recorder *trace.Recorder,
	plannerSpan string,
	peers []config.Peer,
	request protocol.ReplyRequest,
	result *protocol.GroupResponse,
) {
	var wg sync.WaitGroup
	for index, peer := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result.Results[index] = s.dispatchGroupPeer(ctx, recorder, plannerSpan, peer, request)
		}()
	}
	wg.Wait()
	if len(result.Results) == 1 && result.Results[0].Error == "" {
		result.Winner = result.Results[0].Peer
	}
}

func (s *Server) dispatchGroupRace(
	ctx context.Context,
	recorder *trace.Recorder,
	plannerSpan string,
	peers []config.Peer,
	request protocol.ReplyRequest,
	result *protocol.GroupResponse,
) {
	raceContext, cancel := context.WithCancel(ctx)
	defer cancel()
	type completedPeer struct {
		index  int
		result protocol.PeerResult
	}
	completed := make(chan completedPeer, len(peers))
	for index, peer := range peers {
		go func() {
			completed <- completedPeer{
				index:  index,
				result: s.dispatchGroupPeer(raceContext, recorder, plannerSpan, peer, request),
			}
		}()
	}
	for range peers {
		item := <-completed
		result.Results[item.index] = item.result
		if result.Winner == "" && item.result.Error == "" && item.result.Response != nil {
			result.Winner = item.result.Peer
			cancel()
		}
	}
}

func (s *Server) dispatchGroupPeer(
	ctx context.Context,
	recorder *trace.Recorder,
	plannerSpan string,
	peer config.Peer,
	request protocol.ReplyRequest,
) protocol.PeerResult {
	started := time.Now()
	peerSpan := recorder.StartSpan(plannerSpan, "executor", peer.Name, map[string]any{
		"node": peer.Name,
		"url":  peer.URL,
	})
	peerResult := s.dispatcherFor(peer).Dispatch(trace.WithParent(ctx, peerSpan), peer, request)
	success := peerResult.Error == "" && peerResult.Response != nil
	observed := success || !errors.Is(ctx.Err(), context.Canceled)
	s.peerScheduler.complete(peer, time.Since(started), success, observed)
	status := "success"
	var peerErr error
	if !success {
		status = "error"
		peerErr = errors.New(peerResult.Error)
	} else if peerResult.Response.TraceID != "" {
		recorder.AddLink(trace.Link{
			Node: peerResult.Response.Node, URL: peer.URL,
			TraceID: peerResult.Response.TraceID,
		})
	}
	recorder.FinishSpan(peerSpan, status, peerResult, peerErr, nil)
	return peerResult
}

func (s *Server) groupPeers(group string) []config.Peer {
	return s.groupPeersFor(group, protocol.ReplyRequest{})
}

func (s *Server) groupPeersFor(group string, request protocol.ReplyRequest) []config.Peer {
	peers := append([]config.Peer(nil), s.cfg.Groups[group]...)
	if s.network != nil {
		peers = append(peers, s.network.PeersFor(
			group, request.Tools, request.Skills, "reply:execute",
		)...)
		if request.DirectTool != nil && s.browserHub != nil {
			for _, peer := range s.network.BrowserPeersFor(group, request.Tools) {
				if s.browserHub.Connected(peer.NodeID) {
					peers = append(peers, peer)
				}
			}
		}
	}
	peers = append(peers, s.discovery.PeersFor(group, request.Tools, request.Skills)...)
	return uniquePeers(peers)
}

func (s *Server) callPeer(ctx context.Context, peer config.Peer, request protocol.ReplyRequest) protocol.PeerResult {
	peer = s.routedPeer(peer)
	var result protocol.PeerResult
	recorder, parentID := trace.FromContext(ctx)
	for attempt := 0; attempt <= s.cfg.HTTP.PeerRetries; attempt++ {
		attemptSpan := recorder.StartSpan(parentID, "network_attempt", "peer-reply", map[string]any{
			"attempt": attempt + 1,
			"node":    peer.Name,
		})
		var retry bool
		result, retry = s.callPeerOnce(ctx, peer, request)
		status := "success"
		var attemptErr error
		if result.Error != "" {
			status = "error"
			attemptErr = errors.New(result.Error)
		}
		recorder.FinishSpan(attemptSpan, status, result, attemptErr, map[string]any{"will_retry": retry})
		if !retry || attempt == s.cfg.HTTP.PeerRetries {
			return result
		}
		timer := time.NewTimer(s.cfg.HTTP.RetryBackoff.Duration() * time.Duration(attempt+1))
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Error = ctx.Err().Error()
			return result
		case <-timer.C:
		}
	}
	return result
}

// routedPeer keeps discovery and relationship identity bound to the address
// announced by the peer while allowing this controller to use a local tunnel
// or port-forward for transport. This is useful for mobile simulators and
// nodes behind NAT, whose advertised address is not directly routable here.
func (s *Server) routedPeer(peer config.Peer) config.Peer {
	if peer.NodeID == "" {
		return peer
	}
	if route := strings.TrimSpace(s.cfg.Network.RouteOverrides[peer.NodeID]); route != "" {
		peer.URL = strings.TrimRight(route, "/")
	}
	return peer
}

func (s *Server) callPeerOnce(ctx context.Context, peer config.Peer, request protocol.ReplyRequest) (protocol.PeerResult, bool) {
	result := protocol.PeerResult{Peer: peer.Name}
	body, err := json.Marshal(request)
	if err != nil {
		result.Error = err.Error()
		return result, false
	}
	endpoint := strings.TrimRight(peer.URL, "/") + "/v1/reply"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		result.Error = err.Error()
		return result, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ECS-Request-ID", request.RequestID)
	if peer.Token != "" {
		req.Header.Set("Authorization", "Bearer "+peer.Token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result, true
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, s.cfg.HTTP.MaxBodyBytes))
	_ = resp.Body.Close()
	if err != nil {
		result.Error = err.Error()
		return result, true
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = fmt.Sprintf("%s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
		return result, resp.StatusCode >= 500
	}
	var reply protocol.ReplyResponse
	if err := json.Unmarshal(responseBody, &reply); err != nil {
		result.Error = err.Error()
		return result, false
	}
	result.Response = &reply
	return result, false
}
