package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"ecs/internal/networkauth"
	"ecs/internal/protocol"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Server) decodeRequest(w http.ResponseWriter, r *http.Request) (protocol.ReplyRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes)
	defer r.Body.Close()
	var request protocol.ReplyRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return protocol.ReplyRequest{}, false
	}
	if request.Prompt == "" && len(request.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "prompt or messages is required")
		return protocol.ReplyRequest{}, false
	}
	for _, message := range request.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			writeError(w, http.StatusBadRequest, "message role must be user or assistant")
			return protocol.ReplyRequest{}, false
		}
	}
	if err := normalizeExpectedText(&request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return protocol.ReplyRequest{}, false
	}
	if err := normalizeDirectTool(&request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return protocol.ReplyRequest{}, false
	}
	if err := applyNetworkPolicy(r.Context(), &request); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return protocol.ReplyRequest{}, false
	}
	return request, true
}

func requestHistory(request protocol.ReplyRequest) []protocol.Message {
	messages := append([]protocol.Message(nil), request.Messages...)
	if request.Prompt != "" {
		messages = append(messages, protocol.Message{Role: "user", Content: request.Prompt})
	}
	return messages
}

func contextWindow(messages []protocol.Message, limit int, maxChars int) []protocol.Message {
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	if maxChars > 0 {
		total := 0
		keep := len(messages)
		for index := len(messages) - 1; index >= 0; index-- {
			total += len([]rune(messages[index].Content))
			if total > maxChars && keep < len(messages) {
				break
			}
			keep = index
		}
		messages = messages[keep:]
	}
	return append([]protocol.Message(nil), messages...)
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.withAuthScope("", next)
}

func (s *Server) withAuthScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthToken == "" && s.network == nil {
			next(w, r)
			return
		}
		given := bearerToken(r)
		if tokenMatches(s.cfg.AuthToken, given) {
			next(w, r)
			return
		}
		if tokenMatches(s.cfg.AdminToken, given) {
			next(w, r)
			return
		}
		if s.network != nil {
			if relationship, ok := s.network.Authenticate(given, scope); ok {
				direction := "upstream"
				if relationship.Role == networkauth.RoleController {
					direction = "downstream"
				}
				if s.logger != nil {
					s.logger.Debug(
						"network relationship authorized",
						"peer_node_id", relationship.PeerNodeID,
						"relationship_role", relationship.Role,
						"direction", direction,
						"scope", scope,
						"allow_upstream_execute", relationship.AllowUpstreamExecute,
						"policy_version", relationship.PolicyVersion,
					)
				}
				context := context.WithValue(r.Context(), networkRelationshipKey{}, relationship)
				next(w, r.WithContext(context))
				return
			}
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "unauthorized")
	}
}

func (s *Server) withAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Add("Vary", "Authorization")
		// Admin authorization is intentionally independent from the shared node
		// credential. An empty admin token must fail closed, and auth_token must
		// never grant approval, relationship, or runtime configuration access.
		if !tokenMatches(s.cfg.AdminToken, bearerToken(r)) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "admin authorization required")
			return
		}
		next(w, r)
	}
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(header, "Bearer ")
}

func tokenMatches(expected, given string) bool {
	return expected != "" && len(given) == len(expected) &&
		subtle.ConstantTimeCompare([]byte(given), []byte(expected)) == 1
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		if r.Method == http.MethodGet &&
			(r.URL.Path == "/v1/admin/overview" || r.URL.Path == "/v1/admin/logs") {
			return
		}
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("request panic", "error", recovered)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
