package networkauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"ecs/internal/protocol"
)

var errNoDiscoveredParent = errors.New("no eligible parent discovered")

type parentTarget struct {
	URL    string
	NodeID string
}

func (m *Manager) Run(
	ctx context.Context,
	capabilities func() protocol.NodeCapabilities,
	logger *slog.Logger,
) {
	if m == nil || (m.cfg.Parent.URL == "" && !m.cfg.Parent.AutoDiscover) {
		return
	}
	logger.Info(
		"network enrollment enabled",
		"node_id", m.NodeID(),
		"parent_url", m.cfg.Parent.URL,
		"auto_discover", m.cfg.Parent.AutoDiscover,
		"discovery_groups", m.cfg.Parent.DiscoveryGroups,
		"pairing_code", m.PairingCode(),
	)
	client := &http.Client{Timeout: 10 * time.Second}
	run := func() {
		if err := m.syncParent(ctx, client, capabilities); err != nil && ctx.Err() == nil {
			if errors.Is(err, errNoDiscoveredParent) {
				logger.Debug("network parent not discovered yet")
			} else {
				logger.Warn("network parent synchronization failed", "error", err)
			}
		}
	}
	run()
	for {
		timer := time.NewTimer(m.nextSyncInterval())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			run()
		}
	}
}

func (m *Manager) nextSyncInterval() time.Duration {
	parent, _ := m.parentState()
	if parent != nil {
		return m.cfg.HeartbeatInterval.Duration()
	}
	interval := 5 * time.Second
	if configured := m.cfg.HeartbeatInterval.Duration(); configured < interval {
		return configured
	}
	return interval
}

func (m *Manager) syncParent(
	ctx context.Context,
	client *http.Client,
	capabilities func() protocol.NodeCapabilities,
) error {
	parent, pending := m.parentState()
	if parent != nil {
		return m.sendHeartbeat(ctx, client, *parent, capabilities())
	}
	if pending != nil {
		return m.claimParent(ctx, client, *pending)
	}
	return m.requestEnrollment(ctx, client, capabilities())
}

func (m *Manager) requestEnrollment(
	ctx context.Context,
	client *http.Client,
	capabilities protocol.NodeCapabilities,
) error {
	target, err := m.resolveParent(capabilities)
	if err != nil {
		return err
	}
	hash, err := CapabilityHash(capabilities)
	if err != nil {
		return err
	}
	capabilities.CapabilityHash = hash
	request := JoinRequest{
		NodeID:         m.NodeID(),
		NodeName:       m.nodeName,
		URL:            m.nodeURL,
		ParentNodeID:   target.NodeID,
		PairingCode:    m.PairingCode(),
		Capabilities:   capabilities,
		CapabilityHash: hash,
	}
	var response JoinResponse
	if err := postJSON(
		ctx,
		client,
		target.URL+"/v1/network/join",
		"",
		request,
		&response,
	); err != nil {
		return err
	}
	if response.EnrollmentID == "" || response.ClaimToken == "" {
		return fmt.Errorf("parent returned an incomplete enrollment response")
	}
	if target.NodeID != "" && response.ParentNodeID != target.NodeID {
		return fmt.Errorf("parent node_id does not match discovered or configured parent")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.doc.ParentPending = &parentEnrollment{
		URL:          target.URL,
		NodeID:       response.ParentNodeID,
		EnrollmentID: response.EnrollmentID,
		ClaimToken:   response.ClaimToken,
	}
	return m.saveLocked()
}

func (m *Manager) resolveParent(local protocol.NodeCapabilities) (parentTarget, error) {
	if configured := strings.TrimRight(strings.TrimSpace(m.cfg.Parent.URL), "/"); configured != "" {
		return parentTarget{URL: configured, NodeID: m.cfg.Parent.NodeID}, nil
	}
	if !m.cfg.Parent.AutoDiscover {
		return parentTarget{}, errNoDiscoveredParent
	}
	m.mu.Lock()
	source := m.discoverySource
	retryAfter := make(map[string]time.Time, len(m.retryAfter))
	for nodeID, retryAt := range m.retryAfter {
		retryAfter[nodeID] = retryAt
	}
	m.mu.Unlock()
	if source == nil {
		return parentTarget{}, errNoDiscoveredParent
	}
	now := time.Now().UTC()
	candidates := source()
	localMatchesParentGroup := len(m.cfg.Parent.DiscoveryGroups) == 0 ||
		matchesAnyGroup(local.Groups, m.cfg.Parent.DiscoveryGroups)
	localCanParent := local.Platform.AcceptsChildren && local.Platform.SupportsInbound
	slices.SortFunc(candidates, func(a, b protocol.NodeCapabilities) int {
		if result := strings.Compare(a.NodeID, b.NodeID); result != 0 {
			return result
		}
		return strings.Compare(a.URL, b.URL)
	})
	for _, candidate := range candidates {
		if candidate.NodeID == "" || candidate.NodeID == m.NodeID() || candidate.URL == "" ||
			!slices.Contains(candidate.Features, "network_auth") {
			continue
		}
		if !candidate.Platform.AcceptsChildren || !candidate.Platform.SupportsInbound {
			continue
		}
		if m.cfg.Parent.NodeID != "" && candidate.NodeID != m.cfg.Parent.NodeID {
			continue
		}
		if !matchesAnyGroup(candidate.Groups, m.cfg.Parent.DiscoveryGroups) {
			continue
		}
		// If both nodes are equally eligible to become the controller, the
		// smaller stable node_id wins. This prevents reciprocal enrollments.
		if m.cfg.Parent.NodeID == "" && localCanParent && localMatchesParentGroup &&
			strings.Compare(candidate.NodeID, m.NodeID()) >= 0 {
			continue
		}
		if retryAt := retryAfter[candidate.NodeID]; retryAt.After(now) {
			continue
		}
		return parentTarget{
			URL:    strings.TrimRight(candidate.URL, "/"),
			NodeID: candidate.NodeID,
		}, nil
	}
	return parentTarget{}, errNoDiscoveredParent
}

func matchesAnyGroup(candidate, required []string) bool {
	if len(required) == 0 {
		return true
	}
	for _, group := range required {
		if slices.Contains(candidate, group) {
			return true
		}
	}
	return false
}

func (m *Manager) claimParent(
	ctx context.Context,
	client *http.Client,
	pending parentEnrollment,
) error {
	var response ClaimResponse
	if err := postJSON(
		ctx,
		client,
		pending.URL+"/v1/network/enrollments/"+pending.EnrollmentID+"/claim",
		pending.ClaimToken,
		map[string]any{},
		&response,
	); err != nil {
		var responseErr *httpResponseError
		if errors.As(err, &responseErr) &&
			(responseErr.StatusCode == http.StatusUnauthorized ||
				responseErr.StatusCode == http.StatusNotFound ||
				responseErr.StatusCode == http.StatusConflict) {
			m.rejectPendingParent(pending)
		}
		return err
	}
	if response.Status == StatusPending {
		return nil
	}
	if response.Status != StatusActive {
		m.rejectPendingParent(pending)
		return fmt.Errorf("parent enrollment is %s", response.Status)
	}
	return m.InstallParent(response)
}

func (m *Manager) rejectPendingParent(pending parentEnrollment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.doc.ParentPending == nil ||
		m.doc.ParentPending.EnrollmentID != pending.EnrollmentID {
		return
	}
	nodeID := pending.NodeID
	if nodeID == "" {
		nodeID = m.cfg.Parent.NodeID
	}
	if nodeID != "" {
		m.retryAfter[nodeID] = time.Now().UTC().Add(m.cfg.EnrollmentTTL.Duration())
	}
	m.doc.ParentPending = nil
	_ = m.saveLocked()
}

func (m *Manager) sendHeartbeat(
	ctx context.Context,
	client *http.Client,
	parent Relationship,
	capabilities protocol.NodeCapabilities,
) error {
	hash, err := CapabilityHash(capabilities)
	if err != nil {
		return err
	}
	heartbeat := Heartbeat{
		NodeID:         m.NodeID(),
		CapabilityHash: hash,
		SentAt:         time.Now().UTC(),
	}
	if parent.CapabilityHash != hash {
		capabilities.CapabilityHash = hash
		heartbeat.Capabilities = &capabilities
	}
	var response HeartbeatResponse
	if err := postJSON(
		ctx,
		client,
		strings.TrimRight(parent.PeerURL, "/")+"/v1/network/heartbeat",
		parent.Token,
		heartbeat,
		&response,
	); err != nil {
		var responseErr *httpResponseError
		if errors.As(err, &responseErr) &&
			(responseErr.StatusCode == http.StatusUnauthorized ||
				responseErr.StatusCode == http.StatusForbidden) {
			_ = m.setParentConnectionStatus(parent.PeerNodeID, StatusSuspended)
		}
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	durableChanged := false
	for index := range m.doc.Relationships {
		item := &m.doc.Relationships[index]
		if item.Role == RoleController && item.PeerNodeID == parent.PeerNodeID {
			if item.Status != StatusActive {
				item.Status = StatusActive
				durableChanged = true
			}
			item.LastSeen = time.Now().UTC()
			if item.CapabilityHash != hash {
				item.CapabilityHash = hash
				item.Capabilities = capabilities
				durableChanged = true
			}
			if response.Token != "" && response.Token != item.Token {
				item.PreviousToken = item.Token
				item.Token = response.Token
				durableChanged = true
			}
			if response.PolicyVersion > item.PolicyVersion {
				item.PolicyVersion = response.PolicyVersion
				item.Groups = append([]string(nil), response.Groups...)
				item.AllowedTools = append([]string(nil), response.AllowedTools...)
				item.AllowedSkills = append([]string(nil), response.AllowedSkills...)
				item.Scopes = append([]string(nil), response.Scopes...)
				item.AllowUpstreamExecute = boolPointer(
					response.AllowUpstreamExecute == nil || *response.AllowUpstreamExecute,
				)
				durableChanged = true
			}
			if response.Token == "" && item.PreviousToken != "" &&
				secureEqual(item.Token, parent.Token) {
				item.PreviousToken = ""
				durableChanged = true
			}
			break
		}
	}
	if durableChanged {
		return m.saveLocked()
	}
	return nil
}

func (m *Manager) setParentConnectionStatus(nodeID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.doc.Relationships {
		item := &m.doc.Relationships[index]
		if item.Role == RoleController && item.PeerNodeID == nodeID {
			item.Status = status
			return m.saveLocked()
		}
	}
	return nil
}

func (m *Manager) parentState() (*Relationship, *parentEnrollment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if parent := m.activeParentLocked(); parent != nil {
		copy := *parent
		return &copy, nil
	}
	if m.doc.ParentPending != nil {
		copy := *m.doc.ParentPending
		return nil, &copy
	}
	return nil, nil
}

func postJSON(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	token string,
	input any,
	output any,
) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		diagnostic, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return &httpResponseError{
			StatusCode: response.StatusCode,
			Status:     response.Status,
			Diagnostic: strings.TrimSpace(string(diagnostic)),
		}
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return err
	}
	return nil
}

type httpResponseError struct {
	StatusCode int
	Status     string
	Diagnostic string
}

func (e *httpResponseError) Error() string {
	return fmt.Sprintf("%s: %s", e.Status, e.Diagnostic)
}
