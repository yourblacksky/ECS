package browsernode

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"ecs/internal/networkauth"
	"ecs/internal/protocol"

	"github.com/gorilla/websocket"
)

const (
	heartbeatInterval = 15 * time.Second
	connectionTimeout = 60 * time.Second
	maxMessageBytes   = 1 << 20
)

type TaskRequest struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Timeout   time.Duration   `json:"-"`
}

type TaskResponse struct {
	TaskID   string          `json:"task_id"`
	NodeID   string          `json:"node_id"`
	Tool     string          `json:"tool"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Duration string          `json:"duration,omitempty"`
}

type wireMessage struct {
	Type           string                    `json:"type"`
	Challenge      string                    `json:"challenge,omitempty"`
	ExpiresAt      time.Time                 `json:"expires_at,omitempty"`
	NodeID         string                    `json:"node_id,omitempty"`
	NodeName       string                    `json:"node_name,omitempty"`
	PairingCode    string                    `json:"pairing_code,omitempty"`
	PublicKey      json.RawMessage           `json:"public_key,omitempty"`
	Signature      string                    `json:"signature,omitempty"`
	Capabilities   protocol.NodeCapabilities `json:"capabilities,omitempty"`
	Status         string                    `json:"status,omitempty"`
	EnrollmentID   string                    `json:"enrollment_id,omitempty"`
	PolicyVersion  uint64                    `json:"policy_version,omitempty"`
	Groups         []string                  `json:"groups,omitempty"`
	AllowedTools   []string                  `json:"allowed_tools,omitempty"`
	HeartbeatMS    int64                     `json:"heartbeat_ms,omitempty"`
	TaskID         string                    `json:"task_id,omitempty"`
	Tool           string                    `json:"tool,omitempty"`
	Arguments      json.RawMessage           `json:"arguments,omitempty"`
	Deadline       time.Time                 `json:"deadline,omitempty"`
	Result         json.RawMessage           `json:"result,omitempty"`
	Error          string                    `json:"error,omitempty"`
	Duration       string                    `json:"duration,omitempty"`
	ServerTime     time.Time                 `json:"server_time,omitempty"`
	LeaseExpiresAt time.Time                 `json:"lease_expires_at,omitempty"`
}

type Hub struct {
	network *networkauth.Manager
	logger  *slog.Logger

	mu          sync.Mutex
	connections map[string]*connection
}

type connection struct {
	hub           *Hub
	socket        *websocket.Conn
	nodeID        string
	nodeName      string
	publicKeyHash string
	writeMu       sync.Mutex
	pendingMu     sync.Mutex
	pending       map[string]*pendingTask
}

type pendingTask struct {
	ack       chan struct{}
	ackOnce   sync.Once
	completed chan TaskResponse
}

func New(network *networkauth.Manager, logger *slog.Logger) *Hub {
	return &Hub{
		network:     network,
		logger:      logger,
		connections: make(map[string]*connection),
	}
}

func (h *Hub) Connected(nodeID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.connections[nodeID] != nil
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		CheckOrigin:      sameOrigin,
	}
	socket, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	socket.SetReadLimit(maxMessageBytes)
	defer socket.Close()

	challenge, err := randomToken(32)
	if err != nil {
		return
	}
	expires := time.Now().UTC().Add(10 * time.Second)
	if err := socket.WriteJSON(wireMessage{
		Type: "challenge", Challenge: challenge, ExpiresAt: expires,
	}); err != nil {
		return
	}
	_ = socket.SetReadDeadline(expires)
	var auth wireMessage
	if err := socket.ReadJSON(&auth); err != nil {
		return
	}
	if auth.Type != "authenticate" {
		h.writeSocketError(socket, "expected authenticate message")
		return
	}
	publicKeyHash, err := verifyBrowserIdentity(auth.NodeID, auth.PublicKey, challenge, auth.Signature)
	if err != nil {
		h.writeSocketError(socket, err.Error())
		return
	}
	relationship, created, err := h.network.ConnectBrowser(networkauth.BrowserConnectRequest{
		NodeID:        auth.NodeID,
		NodeName:      strings.TrimSpace(auth.NodeName),
		PairingCode:   auth.PairingCode,
		PublicKeyHash: publicKeyHash,
		Capabilities:  auth.Capabilities,
	})
	if err != nil {
		h.writeSocketError(socket, err.Error())
		return
	}
	if relationship.Status != networkauth.StatusActive {
		_ = socket.WriteJSON(wireMessage{
			Type:         "enrollment",
			Status:       relationship.Status,
			EnrollmentID: relationship.EnrollmentID,
		})
		if created && h.logger != nil {
			h.logger.Info("browser enrollment requested",
				"node_id", auth.NodeID,
				"node", auth.NodeName,
				"enrollment_id", relationship.EnrollmentID)
		}
		return
	}

	client := &connection{
		hub: h, socket: socket, nodeID: auth.NodeID, nodeName: auth.NodeName,
		publicKeyHash: publicKeyHash,
		pending:       make(map[string]*pendingTask),
	}
	h.register(client)
	defer h.unregister(client)
	if err := client.write(wireMessage{
		Type:          "authenticated",
		Status:        networkauth.StatusActive,
		EnrollmentID:  relationship.EnrollmentID,
		PolicyVersion: relationship.PolicyVersion,
		Groups:        relationship.Groups,
		AllowedTools:  relationship.AllowedTools,
		HeartbeatMS:   heartbeatInterval.Milliseconds(),
	}); err != nil {
		return
	}
	if h.logger != nil {
		h.logger.Info("browser leaf connected", "node_id", client.nodeID, "node", client.nodeName)
	}
	_ = socket.SetReadDeadline(time.Now().Add(connectionTimeout))
	client.readLoop()
}

func (h *Hub) Dispatch(ctx context.Context, nodeID string, request TaskRequest) (TaskResponse, error) {
	if strings.TrimSpace(request.Tool) == "" {
		return TaskResponse{}, errors.New("tool is required")
	}
	if len(request.Arguments) == 0 {
		request.Arguments = json.RawMessage(`{}`)
	}
	if !json.Valid(request.Arguments) {
		return TaskResponse{}, errors.New("arguments must be valid JSON")
	}
	relationship, allowed := h.network.BrowserAllows(nodeID, request.Tool)
	if !allowed {
		return TaskResponse{}, errors.New("browser node is offline or tool is not authorized")
	}
	h.mu.Lock()
	client := h.connections[nodeID]
	h.mu.Unlock()
	if client == nil {
		return TaskResponse{}, errors.New("browser node is not connected")
	}
	taskID, err := randomToken(16)
	if err != nil {
		return TaskResponse{}, err
	}
	if request.Timeout <= 0 {
		request.Timeout = 30 * time.Second
	}
	deadline := time.Now().UTC().Add(request.Timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	pending := &pendingTask{
		ack:       make(chan struct{}),
		completed: make(chan TaskResponse, 1),
	}
	client.pendingMu.Lock()
	client.pending[taskID] = pending
	client.pendingMu.Unlock()
	defer func() {
		client.pendingMu.Lock()
		delete(client.pending, taskID)
		client.pendingMu.Unlock()
	}()
	if err := client.write(wireMessage{
		Type:      "task",
		TaskID:    taskID,
		Tool:      request.Tool,
		Arguments: request.Arguments,
		Deadline:  deadline,
	}); err != nil {
		return TaskResponse{}, err
	}
	ackTimeout := min(5*time.Second, time.Until(deadline))
	ackTimer := time.NewTimer(ackTimeout)
	select {
	case <-ctx.Done():
		ackTimer.Stop()
		client.cancel(taskID)
		return TaskResponse{}, ctx.Err()
	case <-ackTimer.C:
		client.cancel(taskID)
		return TaskResponse{}, errors.New("browser task acknowledgement deadline exceeded")
	case result := <-pending.completed:
		ackTimer.Stop()
		result.NodeID = nodeID
		result.Tool = request.Tool
		return result, nil
	case <-pending.ack:
		ackTimer.Stop()
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		client.cancel(taskID)
		return TaskResponse{}, ctx.Err()
	case <-timer.C:
		client.cancel(taskID)
		return TaskResponse{}, errors.New("browser task deadline exceeded")
	case result := <-pending.completed:
		result.NodeID = nodeID
		result.Tool = request.Tool
		if relationship.PeerNodeID != nodeID {
			return TaskResponse{}, errors.New("browser relationship changed during task")
		}
		return result, nil
	}
}

func (c *connection) cancel(taskID string) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.socket.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
	_ = c.socket.WriteJSON(wireMessage{Type: "task_cancel", TaskID: taskID})
}

func (c *connection) readLoop() {
	for {
		var message wireMessage
		if err := c.socket.ReadJSON(&message); err != nil {
			return
		}
		switch message.Type {
		case "heartbeat":
			response, err := c.hub.network.BrowserHeartbeat(c.nodeID, c.publicKeyHash)
			if err != nil {
				_ = c.write(wireMessage{Type: "error", Error: err.Error()})
				return
			}
			_ = c.socket.SetReadDeadline(time.Now().Add(connectionTimeout))
			if err := c.write(wireMessage{
				Type:           "heartbeat_ack",
				Status:         response.Status,
				ServerTime:     response.ServerTime,
				LeaseExpiresAt: response.LeaseExpires,
				PolicyVersion:  response.PolicyVersion,
				Groups:         response.Groups,
				AllowedTools:   response.AllowedTools,
			}); err != nil {
				return
			}
		case "task_result":
			_ = c.socket.SetReadDeadline(time.Now().Add(connectionTimeout))
			c.pendingMu.Lock()
			pending := c.pending[message.TaskID]
			c.pendingMu.Unlock()
			if pending != nil {
				result := TaskResponse{
					TaskID:   message.TaskID,
					Result:   message.Result,
					Error:    message.Error,
					Duration: message.Duration,
				}
				select {
				case pending.completed <- result:
				default:
				}
			}
		case "task_ack":
			_ = c.socket.SetReadDeadline(time.Now().Add(connectionTimeout))
			c.pendingMu.Lock()
			pending := c.pending[message.TaskID]
			c.pendingMu.Unlock()
			if pending != nil {
				pending.ackOnce.Do(func() { close(pending.ack) })
			}
		default:
			_ = c.write(wireMessage{Type: "error", Error: "unsupported message type"})
		}
	}
}

func (c *connection) write(message wireMessage) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.socket.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.socket.WriteJSON(message)
}

func (h *Hub) register(client *connection) {
	h.mu.Lock()
	previous := h.connections[client.nodeID]
	h.connections[client.nodeID] = client
	h.mu.Unlock()
	if previous != nil {
		_ = previous.socket.Close()
	}
}

func (h *Hub) unregister(client *connection) {
	h.mu.Lock()
	current := h.connections[client.nodeID]
	if current == client {
		delete(h.connections, client.nodeID)
	}
	h.mu.Unlock()
	if current != client {
		return
	}
	client.pendingMu.Lock()
	for taskID, pending := range client.pending {
		delete(client.pending, taskID)
		select {
		case pending.completed <- TaskResponse{TaskID: taskID, Error: "browser node disconnected"}:
		default:
		}
	}
	client.pendingMu.Unlock()
	h.network.BrowserDisconnected(client.nodeID, client.publicKeyHash)
	if h.logger != nil {
		h.logger.Info("browser leaf disconnected", "node_id", client.nodeID, "node", client.nodeName)
	}
}

func (h *Hub) writeSocketError(socket *websocket.Conn, message string) {
	_ = socket.WriteJSON(wireMessage{Type: "error", Error: message})
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		strings.EqualFold(parsed.Host, r.Host)
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
