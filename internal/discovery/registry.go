package discovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"ecs/internal/config"
	"ecs/internal/protocol"
)

const registryBodyLimit = 1 << 20

var registryNodeIDPattern = regexp.MustCompile(`^ecs_[0-9a-f]{32}$`)

type registryRequest struct {
	Node         protocol.NodeCapabilities `json:"node"`
	LeaseSeconds int64                     `json:"lease_seconds,omitempty"`
}

type registryRecord struct {
	Node         protocol.NodeCapabilities `json:"node"`
	RegisteredAt time.Time                 `json:"registered_at"`
	ExpiresAt    time.Time                 `json:"expires_at"`
}

type registryList struct {
	Nodes []registryRecord `json:"nodes"`
}

// RegistryController is an intentionally small, in-memory directory. Losing
// its state is harmless because clients recreate entries on their next
// heartbeat; trust and permissions remain in networkauth.
type RegistryController struct {
	cfg   config.RegistryConfig
	mu    sync.Mutex
	nodes map[string]registryRecord
}

func newRegistryController(cfg config.RegistryConfig) *RegistryController {
	return &RegistryController{
		cfg:   cfg,
		nodes: make(map[string]registryRecord),
	}
}

func (c *RegistryController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !c.authorized(r) {
		registryWriteError(w, http.StatusUnauthorized, "invalid registry credential")
		return
	}
	switch {
	case r.Method == http.MethodPost &&
		(r.URL.Path == "/v1/registry/register" || r.URL.Path == "/v1/registry/heartbeat"):
		c.register(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/registry/nodes":
		c.list(w)
	default:
		registryWriteError(w, http.StatusNotFound, "registry endpoint not found")
	}
}

func (c *RegistryController) authorized(r *http.Request) bool {
	if c.cfg.Token == "" {
		return true
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	expected := sha256.Sum256([]byte(c.cfg.Token))
	actual := sha256.Sum256([]byte(strings.TrimPrefix(header, prefix)))
	return subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
}

func (c *RegistryController) register(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var request registryRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, registryBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		registryWriteError(w, http.StatusBadRequest, "invalid registry request: "+err.Error())
		return
	}
	if err := validateRegistryNode(request.Node); err != nil {
		registryWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	lease := c.cfg.LeaseTimeout.Duration()
	if request.LeaseSeconds > 0 {
		requested := time.Duration(request.LeaseSeconds) * time.Second
		if requested < lease {
			lease = requested
		}
	}
	now := time.Now().UTC()
	request.Node = compactAnnouncement(request.Node)
	request.Node.LastSeen = nil
	record := registryRecord{
		Node:         request.Node,
		RegisteredAt: now,
		ExpiresAt:    now.Add(lease),
	}
	c.mu.Lock()
	c.expireLocked(now)
	c.nodes[request.Node.NodeID] = record
	c.mu.Unlock()
	registryWriteJSON(w, http.StatusOK, record)
}

func (c *RegistryController) list(w http.ResponseWriter) {
	now := time.Now().UTC()
	c.mu.Lock()
	c.expireLocked(now)
	result := make([]registryRecord, 0, len(c.nodes))
	for _, record := range c.nodes {
		result = append(result, record)
	}
	c.mu.Unlock()
	slices.SortFunc(result, func(a, b registryRecord) int {
		return strings.Compare(a.Node.NodeID, b.Node.NodeID)
	})
	registryWriteJSON(w, http.StatusOK, registryList{Nodes: result})
}

func (c *RegistryController) expireLocked(now time.Time) {
	for nodeID, record := range c.nodes {
		if !record.ExpiresAt.After(now) {
			delete(c.nodes, nodeID)
		}
	}
}

func validateRegistryNode(node protocol.NodeCapabilities) error {
	if node.Protocol != protocolVersion {
		return errors.New("registry node protocol must be ecs/1")
	}
	if !registryNodeIDPattern.MatchString(node.NodeID) {
		return errors.New("registry node_id must be ecs_ followed by 32 lowercase hexadecimal characters")
	}
	if node.Node == "" {
		return errors.New("registry node name is required")
	}
	if err := validateAnnouncementURL(node.URL); err != nil {
		return err
	}
	return nil
}

func (m *Manager) RegistryHandler() http.Handler {
	if m.registryController == nil {
		return http.NotFoundHandler()
	}
	return m.registryController
}

func (m *Manager) RegistryServing() bool {
	return m.registryController != nil
}

func (m *Manager) registryLoop(ctx context.Context) {
	syncAll := func() {
		for _, controllerURL := range m.cfg.Registry.ControllerURLs {
			if err := m.syncRegistry(ctx, controllerURL); err != nil && ctx.Err() == nil {
				m.logger.Warn("registry discovery sync failed", "controller", controllerURL, "error", err)
			}
		}
	}
	syncAll()
	ticker := time.NewTicker(m.cfg.Registry.HeartbeatInterval.Duration())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncAll()
		}
	}
}

func (m *Manager) syncRegistry(ctx context.Context, controllerURL string) error {
	baseURL := strings.TrimRight(controllerURL, "/")
	request := registryRequest{
		Node:         compactAnnouncement(m.Capabilities()),
		LeaseSeconds: int64(m.cfg.Registry.LeaseTimeout.Duration() / time.Second),
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if err := m.registryRequest(ctx, http.MethodPost, baseURL+"/v1/registry/heartbeat", body, nil); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	var listed registryList
	if err := m.registryRequest(ctx, http.MethodGet, baseURL+"/v1/registry/nodes", nil, &listed); err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	for _, record := range listed.Nodes {
		if !record.ExpiresAt.After(time.Now()) {
			continue
		}
		payload, marshalErr := json.Marshal(record.Node)
		if marshalErr == nil {
			m.acceptAnnouncement(ctx, payload)
		}
	}
	return nil
}

func (m *Manager) registryRequest(
	ctx context.Context,
	method string,
	endpoint string,
	body []byte,
	result any,
) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if m.cfg.Registry.Token != "" {
		request.Header.Set("Authorization", "Bearer "+m.cfg.Registry.Token)
	}
	response, err := m.registryClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		diagnostic, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(diagnostic)))
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, registryBodyLimit)).Decode(result)
}

func registryWriteError(w http.ResponseWriter, status int, message string) {
	registryWriteJSON(w, status, map[string]string{"error": message})
}

func registryWriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
