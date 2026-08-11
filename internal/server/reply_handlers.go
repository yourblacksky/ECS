package server

import (
	"crypto/sha256"
	"ecs/internal/agent"
	"ecs/internal/protocol"
	"ecs/internal/trace"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"
)

func (s *Server) reply(w http.ResponseWriter, r *http.Request) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "node is busy")
		return
	}
	request, ok := s.decodeRequest(w, r)
	if !ok {
		return
	}
	if request.RequestID == "" {
		request.RequestID = requestID()
	}
	principal := requestOwner(r.Context())
	requestKey := principal + "\x00" + request.RequestID
	fingerprintData, _ := json.Marshal(request)
	entry, owner, mismatch := s.beginRequest(requestKey, sha256.Sum256(fingerprintData))
	if mismatch {
		writeError(w, http.StatusConflict, "request_id was already used with a different request")
		return
	}
	if !owner {
		select {
		case <-r.Context().Done():
			writeError(w, http.StatusRequestTimeout, r.Context().Err().Error())
		case <-entry.done:
			if entry.response == nil {
				writeError(w, http.StatusServiceUnavailable, "previous attempt failed; retry the request")
				return
			}
			writeJSON(w, http.StatusOK, *entry.response)
		}
		return
	}
	completed := false
	defer func() {
		if !completed {
			s.finishRequest(requestKey, entry, nil)
		}
	}()
	newMessages := append([]protocol.Message(nil), request.Messages...)
	if request.Prompt != "" {
		newMessages = append(newMessages, protocol.Message{Role: "user", Content: request.Prompt})
	}
	if request.SessionID != "" {
		conversation, err := s.sessions.Load(principal, request.SessionID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err == nil {
			request.Messages = append(contextWindow(conversation.Messages, s.cfg.Sessions.ContextMessages, s.cfg.Sessions.MaxContextChars), request.Messages...)
		}
	}
	if !trace.IsValidID(request.TraceID) {
		request.TraceID = trace.NewID()
	}
	recorder := s.traces.StartOwned(principal, request.TraceID, request.RequestID, s.cfg.NodeName, "/v1/reply", map[string]any{
		"request": request,
		"depth":   0,
	})
	if recorder.TraceID() != "" {
		request.TraceID = recorder.TraceID()
	}
	traceContext := trace.WithRecorder(r.Context(), recorder, recorder.RootID())
	bounded, err := boundRequest(traceContext, request)
	if err != nil {
		recorder.Finish("rejected", "", err)
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	var response protocol.ReplyResponse
	if request.DirectTool != nil {
		response, err = s.runDirectTool(traceContext, bounded)
	} else {
		response, err = s.agent.Reply(traceContext, bounded)
	}
	s.metrics.ObserveReply(response)
	if err != nil {
		var approval *agent.ApprovalError
		if errors.As(err, &approval) {
			recorder.Finish("approval_required", "", err)
			writeJSON(w, http.StatusConflict, protocol.ApprovalRequired{
				Error:       "approval_required",
				RequestID:   request.RequestID,
				TraceID:     request.TraceID,
				Tool:        approval.Tool,
				Description: approval.Description,
				Risk:        approval.Risk,
				Arguments:   approval.Arguments,
			})
			return
		}
		recorder.Finish("error", "", err)
		s.logger.Error("reply failed", "request_id", request.RequestID, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":      err.Error(),
			"request_id": request.RequestID,
			"trace_id":   request.TraceID,
			"session_id": request.SessionID,
			"node":       s.cfg.NodeName,
			"message":    response.Message,
			"tool_runs":  response.ToolRuns,
			"skill_runs": response.SkillRuns,
		})
		return
	}
	response.RequestID = request.RequestID
	response.TraceID = request.TraceID
	response.SessionID = request.SessionID
	response.Node = s.cfg.NodeName
	if request.SessionID != "" {
		newMessages = append(newMessages, response.Message)
		if _, err := s.sessions.Append(principal, request.SessionID, newMessages...); err != nil {
			recorder.Finish("error", response.Message.Content, err)
			s.logger.Error("session save failed", "session_id", request.SessionID, "error", err)
			writeError(w, http.StatusInternalServerError, "reply generated but session could not be saved")
			return
		}
	}
	completed = true
	s.finishRequest(requestKey, entry, &response)
	recorder.Finish("success", response.Message.Content, nil)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) beginRequest(id string, fingerprint [32]byte) (*requestEntry, bool, bool) {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	now := time.Now()
	for key, entry := range s.requests {
		if !entry.completedAt.IsZero() && now.Sub(entry.completedAt) > 10*time.Minute {
			delete(s.requests, key)
		}
	}
	if entry, exists := s.requests[id]; exists {
		return entry, false, entry.fingerprint != fingerprint
	}
	entry := &requestEntry{fingerprint: fingerprint, done: make(chan struct{})}
	s.requests[id] = entry
	return entry, true, false
}

func (s *Server) finishRequest(id string, entry *requestEntry, response *protocol.ReplyResponse) {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	current, exists := s.requests[id]
	if !exists || current != entry {
		return
	}
	if response == nil {
		delete(s.requests, id)
		close(entry.done)
		return
	}
	copyOfResponse := *response
	entry.response = &copyOfResponse
	entry.completedAt = time.Now()
	close(entry.done)
}
