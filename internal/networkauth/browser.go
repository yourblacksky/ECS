package networkauth

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ecs/internal/config"
	"ecs/internal/protocol"
)

type BrowserConnectRequest struct {
	NodeID        string
	NodeName      string
	PairingCode   string
	PublicKeyHash string
	Capabilities  protocol.NodeCapabilities
}

// ConnectBrowser creates an enrollment or resumes a browser relationship
// after the caller has verified proof of possession of the bound public key.
func (m *Manager) ConnectBrowser(request BrowserConnectRequest) (PublicRelationship, bool, error) {
	m.browserMu.Lock()
	defer m.browserMu.Unlock()

	request.Capabilities.NodeID = request.NodeID
	request.Capabilities.Node = request.NodeName
	request.Capabilities.URL = "browser://" + request.NodeID
	hash, err := CapabilityHash(request.Capabilities)
	if err != nil {
		return PublicRelationship{}, false, err
	}
	join := JoinRequest{
		NodeID:         request.NodeID,
		NodeName:       request.NodeName,
		URL:            request.Capabilities.URL,
		Transport:      TransportBrowserWebSocket,
		PublicKeyHash:  request.PublicKeyHash,
		BrowserProofOK: true,
		ParentNodeID:   m.NodeID(),
		PairingCode:    request.PairingCode,
		Capabilities:   request.Capabilities,
		CapabilityHash: hash,
	}
	if err := m.validateJoin(join); err != nil {
		return PublicRelationship{}, false, err
	}

	m.mu.Lock()
	m.expirePendingLocked(time.Now().UTC())
	for index := range m.doc.Relationships {
		item := &m.doc.Relationships[index]
		if item.PeerNodeID != request.NodeID || item.Status == StatusRevoked {
			continue
		}
		if item.Transport != TransportBrowserWebSocket || item.PublicKeyHash != request.PublicKeyHash {
			m.mu.Unlock()
			return PublicRelationship{}, false, errors.New("browser identity key does not match enrollment")
		}
		switch item.Status {
		case StatusPending:
			result := m.publicLocked(*item)
			m.mu.Unlock()
			return result, false, nil
		case StatusSuspended:
			m.mu.Unlock()
			return PublicRelationship{}, false, errors.New("browser relationship is suspended")
		case StatusApproved, StatusActive:
			now := time.Now().UTC()
			item.Status = StatusActive
			item.LastSeen = now
			if item.ClaimedAt.IsZero() {
				item.ClaimedAt = now
			}
			item.ClaimToken = ""
			item.IssuedToken = ""
			item.Token = ""
			item.PreviousToken = ""
			item.PairingCode = ""
			item.PeerName = request.NodeName
			item.PeerURL = request.Capabilities.URL
			item.Capabilities = request.Capabilities
			item.CapabilityHash = hash
			item.AllowedTools = keepAllowed(item.AllowedTools, browserToolNames(request.Capabilities))
			if err := m.saveLocked(); err != nil {
				m.mu.Unlock()
				return PublicRelationship{}, false, err
			}
			result := m.publicLocked(*item)
			m.mu.Unlock()
			return result, false, nil
		default:
			m.mu.Unlock()
			return PublicRelationship{}, false, fmt.Errorf("browser enrollment is %s", item.Status)
		}
	}
	m.mu.Unlock()

	response, err := m.CreateJoin(join)
	if err != nil {
		return PublicRelationship{}, false, err
	}
	for _, item := range m.List() {
		if item.EnrollmentID == response.EnrollmentID {
			return item, true, nil
		}
	}
	return PublicRelationship{}, true, errors.New("browser enrollment was not persisted")
}

func (m *Manager) BrowserHeartbeat(nodeID, publicKeyHash string) (HeartbeatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.doc.Relationships {
		item := &m.doc.Relationships[index]
		if item.PeerNodeID != nodeID || item.Transport != TransportBrowserWebSocket ||
			item.PublicKeyHash != publicKeyHash {
			continue
		}
		if item.Status != StatusActive {
			return HeartbeatResponse{}, fmt.Errorf("browser relationship is %s", item.Status)
		}
		now := time.Now().UTC()
		item.LastSeen = now
		return HeartbeatResponse{
			Status:        StatusActive,
			ServerTime:    now,
			LeaseExpires:  now.Add(m.cfg.LeaseTimeout.Duration()),
			PolicyVersion: item.PolicyVersion,
			Groups:        append([]string(nil), item.Groups...),
			AllowedTools:  append([]string(nil), item.AllowedTools...),
			AllowedSkills: append([]string(nil), item.AllowedSkills...),
			Scopes:        append([]string(nil), item.Scopes...),
		}, nil
	}
	return HeartbeatResponse{}, errors.New("browser relationship was not found")
}

func (m *Manager) BrowserAllows(nodeID, tool string) (PublicRelationship, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.doc.Relationships {
		if item.PeerNodeID != nodeID || item.Transport != TransportBrowserWebSocket ||
			item.Status != StatusActive {
			continue
		}
		if !item.LastSeen.IsZero() &&
			time.Now().UTC().After(item.LastSeen.Add(m.cfg.LeaseTimeout.Duration())) {
			return PublicRelationship{}, false
		}
		if !slices.Contains(item.AllowedTools, tool) {
			return PublicRelationship{}, false
		}
		return m.publicLocked(item), true
	}
	return PublicRelationship{}, false
}

// BrowserPeersFor returns online browser leaves whose relationship policy and
// declared manifest cover every requested tool. Connection presence is checked
// separately by the Browser Hub because it owns the live WebSocket.
func (m *Manager) BrowserPeersFor(group string, requiredTools []string) []config.Peer {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var result []config.Peer
	for _, item := range m.doc.Relationships {
		if item.Transport != TransportBrowserWebSocket || item.Status != StatusActive {
			continue
		}
		if !item.LastSeen.IsZero() && now.After(item.LastSeen.Add(m.cfg.LeaseTimeout.Duration())) {
			continue
		}
		if group != "" && group != "all" && !slices.Contains(item.Groups, group) {
			continue
		}
		allowed := true
		for _, tool := range requiredTools {
			if !slices.Contains(item.AllowedTools, tool) || !hasTool(item.Capabilities, tool) {
				allowed = false
				break
			}
		}
		if !allowed {
			continue
		}
		result = append(result, config.Peer{
			NodeID:    item.PeerNodeID,
			Name:      item.PeerName,
			URL:       item.PeerURL,
			Transport: TransportBrowserWebSocket,
		})
	}
	return result
}

func (m *Manager) BrowserDisconnected(nodeID, publicKeyHash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.doc.Relationships {
		item := &m.doc.Relationships[index]
		if item.PeerNodeID != nodeID || item.Transport != TransportBrowserWebSocket ||
			item.PublicKeyHash != publicKeyHash || item.Status != StatusActive {
			continue
		}
		item.LastSeen = time.Now().UTC().Add(-m.cfg.LeaseTimeout.Duration() - time.Second)
		return
	}
}

func validateBrowserLeaf(capabilities protocol.NodeCapabilities) error {
	platform := capabilities.Platform
	if platform.Name != "browser" || platform.RuntimeMode != "client" ||
		platform.TopologyRole != "leaf" || platform.AcceptsChildren ||
		platform.SupportsInbound || platform.SupportsBackground ||
		platform.SupportsSubprocess || platform.SupportsMulticast {
		return errors.New("browser capability must declare a client-only leaf without inbound, background, subprocess, or multicast support")
	}
	if capabilities.Protocol != "ecs/1" {
		return errors.New("browser capability protocol must be ecs/1")
	}
	if strings.TrimSpace(capabilities.Node) == "" {
		return errors.New("browser capability node is required")
	}
	return nil
}

func browserToolNames(capabilities protocol.NodeCapabilities) []string {
	result := make([]string, 0, len(capabilities.Tools))
	for _, tool := range capabilities.Tools {
		result = append(result, tool.Name)
	}
	return result
}
