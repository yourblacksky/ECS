package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"ecs/internal/agent"
	"ecs/internal/authz"
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

func (s *Server) recursiveReply(w http.ResponseWriter, r *http.Request) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "node is busy")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes)
	defer r.Body.Close()
	var request protocol.RecursiveReplyRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if request.Reply.Prompt == "" && len(request.Reply.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "reply.prompt or reply.messages is required")
		return
	}
	if err := normalizeExpectedText(&request.Reply); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := normalizeDirectTool(&request.Reply); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := applyNetworkPolicy(r.Context(), &request.Reply); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	for _, message := range request.Reply.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			writeError(w, http.StatusBadRequest, "reply message role must be user or assistant")
			return
		}
	}
	if request.Depth < 0 || (request.MaxDepth != nil && *request.MaxDepth < 0) {
		writeError(w, http.StatusBadRequest, "depth values cannot be negative")
		return
	}
	if request.Reply.RequestID == "" {
		request.Reply.RequestID = requestID()
	}
	if request.MaxDepth == nil {
		value := s.cfg.Nested.DefaultMaxDepth
		request.MaxDepth = &value
	}
	principal := requestOwner(r.Context())
	requestKey := principal + "\x00" + request.Reply.RequestID
	requestData, _ := json.Marshal(request)
	recursiveEntry, owner, mismatch := s.beginRecursiveRequest(
		requestKey,
		sha256.Sum256(requestData),
	)
	if mismatch {
		writeError(w, http.StatusConflict, "request_id was already used with a different recursive request")
		return
	}
	if !owner {
		select {
		case <-r.Context().Done():
			writeError(w, http.StatusRequestTimeout, r.Context().Err().Error())
		case <-recursiveEntry.done:
			if recursiveEntry.response == nil {
				writeError(w, http.StatusServiceUnavailable, "previous recursive attempt failed; retry")
				return
			}
			writeJSON(w, http.StatusOK, *recursiveEntry.response)
		}
		return
	}
	completed := false
	defer func() {
		if !completed {
			s.finishRecursiveRequest(requestKey, recursiveEntry, nil)
		}
	}()
	identity := s.nodeIdentity()
	if !trace.IsValidID(request.Reply.TraceID) {
		request.Reply.TraceID = trace.NewID()
	}
	recorder := s.traces.StartOwned(
		principal,
		request.Reply.TraceID,
		request.Reply.RequestID,
		s.cfg.NodeName,
		"/v1/recursive/reply",
		map[string]any{"request": request.Reply, "depth": request.Depth, "path": request.Path},
	)
	if recorder.TraceID() != "" {
		request.Reply.TraceID = recorder.TraceID()
	}
	if pathContains(request.Path, identity) {
		recorder.Finish("rejected", "", errors.New("recursive ECS loop detected"))
		writeError(w, http.StatusLoopDetected, "recursive ECS loop detected")
		return
	}
	newHistory := requestHistory(request.Reply)
	originalFingerprint, _ := json.Marshal(request.Reply)
	if request.Depth == 0 && request.Reply.SessionID != "" {
		conversation, err := s.sessions.Load(principal, request.Reply.SessionID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			recorder.Finish("error", "", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err == nil {
			request.Reply.Messages = append(contextWindow(conversation.Messages, s.cfg.Sessions.ContextMessages, s.cfg.Sessions.MaxContextChars), request.Reply.Messages...)
		}
	}
	traceContext := trace.WithRecorder(r.Context(), recorder, recorder.RootID())
	bounded, err := boundRequest(traceContext, request.Reply)
	if err != nil {
		recorder.Finish("rejected", "", err)
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	request.Reply = bounded.Request()
	response := s.runRecursive(traceContext, request, bounded, sha256.Sum256(originalFingerprint))
	response.TraceID = request.Reply.TraceID
	if request.Depth == 0 && request.Reply.SessionID != "" && response.Summary != nil {
		newHistory = append(newHistory, *response.Summary)
		if _, err := s.sessions.Append(principal, request.Reply.SessionID, newHistory...); err != nil {
			recorder.Event(recorder.RootID(), "error", "session-save", "error", map[string]any{"error": err.Error()})
			s.logger.Error("recursive session save failed", "session_id", request.Reply.SessionID, "error", err)
		}
	}
	completed = true
	s.finishRecursiveRequest(requestKey, recursiveEntry, &response)
	status := "success"
	var traceErr error
	if response.Error != "" {
		status = "error"
		traceErr = errors.New(response.Error)
	}
	summary := ""
	if response.Summary != nil {
		summary = response.Summary.Content
	}
	recorder.Finish(status, summary, traceErr)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) beginRecursiveRequest(
	id string,
	fingerprint [32]byte,
) (*recursiveRequestEntry, bool, bool) {
	s.recursiveMu.Lock()
	defer s.recursiveMu.Unlock()
	now := time.Now()
	for key, entry := range s.recursiveRequests {
		if !entry.completedAt.IsZero() && now.Sub(entry.completedAt) > 10*time.Minute {
			delete(s.recursiveRequests, key)
		}
	}
	if entry, exists := s.recursiveRequests[id]; exists {
		return entry, false, entry.fingerprint != fingerprint
	}
	entry := &recursiveRequestEntry{fingerprint: fingerprint, done: make(chan struct{})}
	s.recursiveRequests[id] = entry
	return entry, true, false
}

func (s *Server) finishRecursiveRequest(
	id string,
	entry *recursiveRequestEntry,
	response *protocol.RecursiveReplyResponse,
) {
	s.recursiveMu.Lock()
	defer s.recursiveMu.Unlock()
	current, exists := s.recursiveRequests[id]
	if !exists || current != entry {
		return
	}
	if response == nil {
		delete(s.recursiveRequests, id)
		close(entry.done)
		return
	}
	copyOfResponse := *response
	entry.response = &copyOfResponse
	entry.completedAt = time.Now()
	close(entry.done)
}

func (s *Server) runRecursive(
	ctx context.Context,
	request protocol.RecursiveReplyRequest,
	bounded authz.Bounded,
	fingerprint [32]byte,
) protocol.RecursiveReplyResponse {
	identity := s.nodeIdentity()
	nodeURL := strings.TrimRight(s.discovery.Capabilities().URL, "/")
	recorder, parentID := trace.FromContext(ctx)
	nodeSpan := recorder.StartSpan(parentID, "ecs_node", s.cfg.NodeName, map[string]any{
		"node":    s.cfg.NodeName,
		"node_id": identity,
		"depth":   request.Depth,
		"url":     nodeURL,
	})
	ctx = trace.WithParent(ctx, nodeSpan)
	response := protocol.RecursiveReplyResponse{
		RequestID: request.Reply.RequestID,
		Node:      s.cfg.NodeName,
		URL:       nodeURL,
		Depth:     request.Depth,
	}
	local, err := s.runLocalIdempotent(ctx, bounded, fingerprint)
	if err != nil {
		var approval *agent.ApprovalError
		if errors.As(err, &approval) {
			response.Approval = &protocol.ApprovalRequired{
				Error:       "approval_required",
				RequestID:   request.Reply.RequestID,
				TraceID:     request.Reply.TraceID,
				Tool:        approval.Tool,
				Description: approval.Description,
				Risk:        approval.Risk,
				Arguments:   approval.Arguments,
			}
		} else {
			response.Error = err.Error()
		}
	} else {
		response.Reply = &local
	}

	canDescend := s.cfg.Nested.Enabled &&
		(*request.MaxDepth == 0 || request.Depth+1 < *request.MaxDepth)
	if canDescend {
		path := append(append([]string(nil), request.Path...), identity)
		peers := s.nestedPeers(path)
		response.Children = make([]protocol.RecursiveReplyResponse, len(peers))
		var wg sync.WaitGroup
		for index, peer := range peers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				peerSpan := recorder.StartSpan(nodeSpan, "nested_ecs", peer.Name, map[string]any{
					"node":  peer.Name,
					"depth": request.Depth + 1,
					"url":   peer.URL,
				})
				childRequest := request
				childRequest.Depth++
				childRequest.Path = path
				childRequest.Reply.SessionID = ""
				response.Children[index] = s.callRecursivePeer(trace.WithParent(ctx, peerSpan), peer, childRequest)
				childStatus := "success"
				var childErr error
				if response.Children[index].Error != "" {
					childStatus = "error"
					childErr = errors.New(response.Children[index].Error)
				} else if response.Children[index].TraceID != "" {
					recorder.AddLink(trace.Link{
						Node: response.Children[index].Node, URL: peer.URL,
						TraceID: response.Children[index].TraceID,
					})
				}
				recorder.FinishSpan(peerSpan, childStatus, response.Children[index], childErr, nil)
			}()
		}
		wg.Wait()
	}

	results := make([]protocol.PeerResult, 0, len(response.Children)+1)
	if response.Reply != nil {
		results = append(results, protocol.PeerResult{Peer: s.cfg.NodeName, Response: response.Reply})
	} else if response.Error != "" {
		results = append(results, protocol.PeerResult{Peer: s.cfg.NodeName, Error: response.Error})
	}
	for index := range response.Children {
		child := &response.Children[index]
		result := protocol.PeerResult{Peer: child.Node, Error: child.Error}
		if child.Summary != nil {
			result.Response = &protocol.ReplyResponse{Message: *child.Summary}
		} else if child.Reply != nil {
			result.Response = child.Reply
		} else if child.Approval != nil {
			result.Error = "approval required for tool " + child.Approval.Tool
		}
		results = append(results, result)
	}
	if len(response.Children) == 0 && response.Reply != nil {
		summary := response.Reply.Message
		response.Summary = &summary
		response.Verification = response.Reply.Verification
	} else if len(results) > 0 {
		summary, verification, err := s.agent.SummarizeWithVerification(ctx, bounded, results)
		if err != nil {
			if response.Error == "" {
				response.Error = "recursive synthesis: " + err.Error()
			}
		} else {
			response.Summary = &summary
			response.Verification = verification
			s.metrics.ObserveVerification(verification)
		}
	}
	nodeStatus := "success"
	var nodeErr error
	if response.Error != "" {
		nodeStatus = "error"
		nodeErr = errors.New(response.Error)
	}
	recorder.FinishSpan(nodeSpan, nodeStatus, response.Summary, nodeErr, nil)
	return response
}

func (s *Server) runLocalIdempotent(
	ctx context.Context,
	bounded authz.Bounded,
	fingerprint [32]byte,
) (protocol.ReplyResponse, error) {
	request := bounded.Request()
	entry, owner, mismatch := s.beginRequest(request.RequestID, fingerprint)
	if mismatch {
		return protocol.ReplyResponse{}, errors.New("request_id was already used with a different request")
	}
	if !owner {
		select {
		case <-ctx.Done():
			return protocol.ReplyResponse{}, ctx.Err()
		case <-entry.done:
			if entry.response == nil {
				return protocol.ReplyResponse{}, errors.New("previous attempt failed")
			}
			return *entry.response, nil
		}
	}
	var response protocol.ReplyResponse
	var err error
	if request.DirectTool != nil {
		response, err = s.runDirectTool(ctx, bounded)
	} else {
		response, err = s.agent.Reply(ctx, bounded)
	}
	s.metrics.ObserveReply(response)
	if err != nil {
		s.finishRequest(request.RequestID, entry, nil)
		return protocol.ReplyResponse{}, err
	}
	response.RequestID = request.RequestID
	response.TraceID = request.TraceID
	response.Node = s.cfg.NodeName
	s.finishRequest(request.RequestID, entry, &response)
	return response, nil
}

func (s *Server) nestedPeers(path []string) []config.Peer {
	peers := append([]config.Peer(nil), s.cfg.Nested.Children...)
	if s.network != nil {
		peers = append(peers, s.network.PeersFor(
			"all", nil, nil, "recursive:execute",
		)...)
	}
	for _, group := range s.cfg.Nested.DiscoveryGroups {
		peers = append(peers, s.discovery.Peers(group)...)
	}
	result := make([]config.Peer, 0, len(peers))
	for _, peer := range uniquePeers(peers) {
		identity := peer.NodeID
		if identity == "" {
			identity = strings.TrimRight(peer.URL, "/")
		}
		if pathContains(path, identity) {
			continue
		}
		result = append(result, peer)
	}
	return result
}

func (s *Server) callRecursivePeer(
	ctx context.Context,
	peer config.Peer,
	request protocol.RecursiveReplyRequest,
) protocol.RecursiveReplyResponse {
	peer = s.routedPeer(peer)
	recorder, parentID := trace.FromContext(ctx)
	for attempt := 0; attempt <= s.cfg.HTTP.PeerRetries; attempt++ {
		attemptSpan := recorder.StartSpan(parentID, "network_attempt", "recursive-request", map[string]any{
			"attempt": attempt + 1,
			"node":    peer.Name,
		})
		response, retry := s.callRecursivePeerOnce(ctx, peer, request)
		status := "success"
		var attemptErr error
		if response.Error != "" {
			status = "error"
			attemptErr = errors.New(response.Error)
		}
		recorder.FinishSpan(attemptSpan, status, response, attemptErr, map[string]any{"will_retry": retry})
		if !retry || attempt == s.cfg.HTTP.PeerRetries {
			return response
		}
		timer := time.NewTimer(s.cfg.HTTP.RetryBackoff.Duration() * time.Duration(attempt+1))
		select {
		case <-ctx.Done():
			timer.Stop()
			response.Error = ctx.Err().Error()
			return response
		case <-timer.C:
		}
	}
	return protocol.RecursiveReplyResponse{Node: peer.Name, URL: peer.URL, Error: "retry loop ended"}
}

func (s *Server) callRecursivePeerOnce(
	ctx context.Context,
	peer config.Peer,
	request protocol.RecursiveReplyRequest,
) (protocol.RecursiveReplyResponse, bool) {
	result := protocol.RecursiveReplyResponse{
		RequestID: request.Reply.RequestID,
		Node:      peer.Name,
		URL:       peer.URL,
		Depth:     request.Depth,
	}
	body, err := json.Marshal(request)
	if err != nil {
		result.Error = err.Error()
		return result, false
	}
	endpoint := strings.TrimRight(peer.URL, "/") + "/v1/recursive/reply"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		result.Error = err.Error()
		return result, false
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if peer.Token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+peer.Token)
	}
	httpResponse, err := s.client.Do(httpRequest)
	if err != nil {
		result.Error = err.Error()
		return result, true
	}
	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, s.cfg.HTTP.MaxBodyBytes))
	_ = httpResponse.Body.Close()
	if err != nil {
		result.Error = err.Error()
		return result, true
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		result.Error = fmt.Sprintf("%s: %s", httpResponse.Status, strings.TrimSpace(string(responseBody)))
		return result, httpResponse.StatusCode >= 500
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		result.Error = err.Error()
		return result, false
	}
	return result, false
}
