package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"ecs/internal/authz"
	"ecs/internal/browsernode"
	"ecs/internal/networkauth"
	"ecs/internal/protocol"
)

type networkRelationshipKey struct{}

func (s *Server) networkIdentity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"node_id":  s.network.NodeID(),
		"node":     s.cfg.NodeName,
		"protocol": "ecs/1",
	})
}

func (s *Server) networkJoin(w http.ResponseWriter, r *http.Request) {
	var request networkauth.JoinRequest
	if !s.decodeNetworkJSON(w, r, &request) {
		return
	}
	response, err := s.network.CreateJoin(request)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already has a relationship") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	s.logger.Info(
		"network enrollment requested",
		"enrollment_id", response.EnrollmentID,
		"node_id", request.NodeID,
		"node", request.NodeName,
	)
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) networkClaim(w http.ResponseWriter, r *http.Request) {
	response, err := s.network.Claim(r.PathValue("enrollment"), bearerToken(r))
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		} else if !strings.Contains(err.Error(), "claim token") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) networkHeartbeat(w http.ResponseWriter, r *http.Request) {
	var heartbeat networkauth.Heartbeat
	if !s.decodeNetworkJSON(w, r, &heartbeat) {
		return
	}
	response, err := s.network.RecordHeartbeat(bearerToken(r), heartbeat)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) adminNetworkRelationships(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"identity":      s.network.Identity(),
		"relationships": s.network.List(),
	})
}

func (s *Server) adminNetworkApprove(w http.ResponseWriter, r *http.Request) {
	var approval networkauth.Approval
	if !s.decodeNetworkJSON(w, r, &approval) {
		return
	}
	relationship, err := s.network.Approve(r.PathValue("enrollment"), approval)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.logger.Info(
		"network enrollment approved",
		"enrollment_id", relationship.EnrollmentID,
		"node_id", relationship.PeerNodeID,
	)
	writeJSON(w, http.StatusOK, relationship)
}

func (s *Server) adminNetworkStatus(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status string `json:"status"`
	}
	if !s.decodeNetworkJSON(w, r, &input) {
		return
	}
	relationship, err := s.network.SetStatus(r.PathValue("enrollment"), input.Status)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.logger.Info(
		"network relationship status changed",
		"enrollment_id", relationship.EnrollmentID,
		"node_id", relationship.PeerNodeID,
		"status", relationship.Status,
	)
	writeJSON(w, http.StatusOK, relationship)
}

func (s *Server) adminNetworkPermissions(w http.ResponseWriter, r *http.Request) {
	var permissions networkauth.Approval
	if !s.decodeNetworkJSON(w, r, &permissions) {
		return
	}
	relationship, err := s.network.UpdatePermissions(r.PathValue("enrollment"), permissions)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "undeclared") ||
			strings.Contains(err.Error(), "unsupported") ||
			strings.Contains(err.Error(), "required") {
			status = http.StatusBadRequest
		}
		writeError(w, status, err.Error())
		return
	}
	if s.logger != nil {
		s.logger.Info(
			"network relationship permissions changed",
			"enrollment_id", relationship.EnrollmentID,
			"node_id", relationship.PeerNodeID,
			"policy_version", relationship.PolicyVersion,
		)
	}
	writeJSON(w, http.StatusOK, relationship)
}

func (s *Server) adminBrowserTask(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
		Timeout   string          `json:"timeout,omitempty"`
	}
	if !s.decodeNetworkJSON(w, r, &input) {
		return
	}
	timeout := 30 * time.Second
	if input.Timeout != "" {
		parsed, err := time.ParseDuration(input.Timeout)
		if err != nil || parsed < 100*time.Millisecond || parsed > 60*time.Second {
			writeError(w, http.StatusBadRequest, "timeout must be between 100ms and 60s")
			return
		}
		timeout = parsed
	}
	result, err := s.browserHub.Dispatch(r.Context(), r.PathValue("node"), browsernode.TaskRequest{
		Tool:      input.Tool,
		Arguments: input.Arguments,
		Timeout:   timeout,
	})
	if err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "required") ||
			strings.Contains(err.Error(), "valid JSON") {
			status = http.StatusBadRequest
		} else if strings.Contains(err.Error(), "not authorized") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
		return
	}
	if result.Error != "" {
		writeJSON(w, http.StatusUnprocessableEntity, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) decodeNetworkJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	reader := io.LimitReader(r.Body, s.cfg.HTTP.MaxBodyBytes+1)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON value")
		return false
	}
	return true
}

func applyNetworkPolicy(ctx context.Context, request *protocol.ReplyRequest) error {
	bounded, err := boundRequest(ctx, *request)
	if err != nil {
		return err
	}
	*request = bounded.Request()
	return nil
}

func boundRequest(ctx context.Context, request protocol.ReplyRequest) (authz.Bounded, error) {
	relationship, ok := ctx.Value(networkRelationshipKey{}).(networkauth.PublicRelationship)
	if !ok {
		return authz.Trusted(request), nil
	}
	return authz.Narrow(relationship, request)
}

func boundApprovedRequest(
	ctx context.Context,
	request protocol.ReplyRequest,
	verifiedApprovals []string,
) (authz.Bounded, error) {
	if relationship, ok := ctx.Value(networkRelationshipKey{}).(networkauth.PublicRelationship); ok {
		return authz.NarrowApproved(relationship, request, verifiedApprovals)
	}
	request.ApprovedTools = append([]string(nil), verifiedApprovals...)
	return authz.Trusted(request), nil
}

func requestOwner(ctx context.Context) string {
	relationship, ok := ctx.Value(networkRelationshipKey{}).(networkauth.PublicRelationship)
	if !ok || relationship.PeerNodeID == "" {
		return "local"
	}
	return relationship.PeerNodeID
}
