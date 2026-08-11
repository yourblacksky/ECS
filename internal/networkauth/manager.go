package networkauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"ecs/internal/config"
	"ecs/internal/protocol"
)

const (
	StatusPending   = "pending"
	StatusApproved  = "approved"
	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusRevoked   = "revoked"
	StatusOffline   = "offline"

	RoleChild      = "child"
	RoleController = "controller"

	TransportHTTP             = "http"
	TransportBrowserWebSocket = "browser_websocket"
)

var (
	nodeIDPattern      = regexp.MustCompile(`^ecs_[0-9a-f]{32}$`)
	pairingCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{6,12}$`)
	publicKeyPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Identity struct {
	NodeID      string    `json:"node_id"`
	PairingCode string    `json:"pairing_code,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type JoinRequest struct {
	NodeID         string                    `json:"node_id"`
	NodeName       string                    `json:"node_name"`
	URL            string                    `json:"url"`
	Transport      string                    `json:"transport,omitempty"`
	PublicKeyHash  string                    `json:"public_key_hash,omitempty"`
	BrowserProofOK bool                      `json:"-"`
	ParentNodeID   string                    `json:"parent_node_id,omitempty"`
	PairingCode    string                    `json:"pairing_code"`
	Capabilities   protocol.NodeCapabilities `json:"capabilities"`
	CapabilityHash string                    `json:"capability_hash"`
}

type JoinResponse struct {
	EnrollmentID string    `json:"enrollment_id"`
	ClaimToken   string    `json:"claim_token"`
	Status       string    `json:"status"`
	ParentNodeID string    `json:"parent_node_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type Approval struct {
	Groups               []string `json:"groups,omitempty"`
	AllowedTools         []string `json:"allowed_tools,omitempty"`
	AllowedSkills        []string `json:"allowed_skills,omitempty"`
	Scopes               []string `json:"scopes,omitempty"`
	AllowUpstreamExecute *bool    `json:"allow_upstream_execute,omitempty"`
}

type ClaimResponse struct {
	Status               string    `json:"status"`
	Token                string    `json:"token,omitempty"`
	ParentNodeID         string    `json:"parent_node_id"`
	ParentName           string    `json:"parent_name"`
	ParentURL            string    `json:"parent_url"`
	Groups               []string  `json:"groups,omitempty"`
	AllowedTools         []string  `json:"allowed_tools,omitempty"`
	AllowedSkills        []string  `json:"allowed_skills,omitempty"`
	Scopes               []string  `json:"scopes,omitempty"`
	AllowUpstreamExecute *bool     `json:"allow_upstream_execute,omitempty"`
	LeaseTimeout         string    `json:"lease_timeout,omitempty"`
	PolicyVersion        uint64    `json:"policy_version,omitempty"`
	ApprovedAt           time.Time `json:"approved_at,omitempty"`
}

type Heartbeat struct {
	NodeID         string                     `json:"node_id"`
	CapabilityHash string                     `json:"capability_hash,omitempty"`
	Capabilities   *protocol.NodeCapabilities `json:"capabilities,omitempty"`
	SentAt         time.Time                  `json:"sent_at,omitempty"`
}

type HeartbeatResponse struct {
	Status               string    `json:"status"`
	ServerTime           time.Time `json:"server_time"`
	LeaseExpires         time.Time `json:"lease_expires"`
	PolicyVersion        uint64    `json:"policy_version"`
	Groups               []string  `json:"groups,omitempty"`
	AllowedTools         []string  `json:"allowed_tools,omitempty"`
	AllowedSkills        []string  `json:"allowed_skills,omitempty"`
	Scopes               []string  `json:"scopes,omitempty"`
	AllowUpstreamExecute *bool     `json:"allow_upstream_execute,omitempty"`
	Token                string    `json:"token,omitempty"`
}

type Relationship struct {
	EnrollmentID         string                    `json:"enrollment_id"`
	PeerNodeID           string                    `json:"peer_node_id"`
	PeerName             string                    `json:"peer_name"`
	PeerURL              string                    `json:"peer_url"`
	Transport            string                    `json:"transport,omitempty"`
	PublicKeyHash        string                    `json:"public_key_hash,omitempty"`
	Role                 string                    `json:"role"`
	Status               string                    `json:"status"`
	PairingCode          string                    `json:"pairing_code,omitempty"`
	ClaimToken           string                    `json:"claim_token,omitempty"`
	IssuedToken          string                    `json:"issued_token,omitempty"`
	Token                string                    `json:"token,omitempty"`
	PreviousToken        string                    `json:"previous_token,omitempty"`
	Groups               []string                  `json:"groups,omitempty"`
	AllowedTools         []string                  `json:"allowed_tools,omitempty"`
	AllowedSkills        []string                  `json:"allowed_skills,omitempty"`
	Scopes               []string                  `json:"scopes,omitempty"`
	AllowUpstreamExecute *bool                     `json:"allow_upstream_execute,omitempty"`
	Capabilities         protocol.NodeCapabilities `json:"capabilities"`
	CapabilityHash       string                    `json:"capability_hash,omitempty"`
	PolicyVersion        uint64                    `json:"policy_version,omitempty"`
	CreatedAt            time.Time                 `json:"created_at"`
	ExpiresAt            time.Time                 `json:"expires_at,omitempty"`
	ApprovedAt           time.Time                 `json:"approved_at,omitempty"`
	ClaimedAt            time.Time                 `json:"claimed_at,omitempty"`
	LastSeen             time.Time                 `json:"last_seen,omitempty"`
}

type PublicRelationship struct {
	EnrollmentID         string                    `json:"enrollment_id"`
	PeerNodeID           string                    `json:"peer_node_id"`
	PeerName             string                    `json:"peer_name"`
	PeerURL              string                    `json:"peer_url"`
	Transport            string                    `json:"transport,omitempty"`
	Role                 string                    `json:"role"`
	Status               string                    `json:"status"`
	PairingCode          string                    `json:"pairing_code,omitempty"`
	Groups               []string                  `json:"groups,omitempty"`
	AllowedTools         []string                  `json:"allowed_tools,omitempty"`
	AllowedSkills        []string                  `json:"allowed_skills,omitempty"`
	Scopes               []string                  `json:"scopes,omitempty"`
	AllowUpstreamExecute bool                      `json:"allow_upstream_execute"`
	Capabilities         protocol.NodeCapabilities `json:"capabilities"`
	CapabilityHash       string                    `json:"capability_hash,omitempty"`
	PolicyVersion        uint64                    `json:"policy_version,omitempty"`
	CreatedAt            time.Time                 `json:"created_at"`
	ExpiresAt            time.Time                 `json:"expires_at,omitempty"`
	ApprovedAt           time.Time                 `json:"approved_at,omitempty"`
	ClaimedAt            time.Time                 `json:"claimed_at,omitempty"`
	LastSeen             time.Time                 `json:"last_seen,omitempty"`
	LeaseExpires         time.Time                 `json:"lease_expires,omitempty"`
}

type parentEnrollment struct {
	URL          string `json:"url"`
	NodeID       string `json:"node_id,omitempty"`
	EnrollmentID string `json:"enrollment_id"`
	ClaimToken   string `json:"claim_token"`
}

type document struct {
	Identity      Identity          `json:"identity"`
	Relationships []Relationship    `json:"relationships"`
	ParentPending *parentEnrollment `json:"parent_pending,omitempty"`
}

type Manager struct {
	cfg             config.NetworkAuthConfig
	nodeName        string
	nodeURL         string
	path            string
	discoverySource func() []protocol.NodeCapabilities
	retryAfter      map[string]time.Time
	browserMu       sync.Mutex
	mu              sync.Mutex
	doc             document
}

func New(cfg config.NetworkAuthConfig, nodeName, nodeURL string) (*Manager, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if err := os.MkdirAll(cfg.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("create network auth directory: %w", err)
	}
	manager := &Manager{
		cfg: cfg, nodeName: nodeName, nodeURL: strings.TrimRight(nodeURL, "/"),
		path:       filepath.Join(cfg.Directory, "network.json"),
		retryAfter: make(map[string]time.Time),
	}
	if err := manager.loadOrCreate(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) NodeID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.doc.Identity.NodeID
}

func (m *Manager) PairingCode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pairingCodeLocked()
}

func (m *Manager) SetDiscoverySource(source func() []protocol.NodeCapabilities) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.discoverySource = source
}

func (m *Manager) Identity() Identity {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Identity{
		NodeID:      m.doc.Identity.NodeID,
		PairingCode: m.pairingCodeLocked(),
		CreatedAt:   m.doc.Identity.CreatedAt,
	}
}

func (m *Manager) pairingCodeLocked() string {
	if value := strings.TrimSpace(m.cfg.Parent.PairingCode); value != "" {
		return value
	}
	return m.doc.Identity.PairingCode
}

func (m *Manager) CreateJoin(request JoinRequest) (JoinResponse, error) {
	if err := m.validateJoin(request); err != nil {
		return JoinResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expirePendingLocked(time.Now().UTC())
	pendingCount := 0
	for index := range m.doc.Relationships {
		item := &m.doc.Relationships[index]
		if item.PeerNodeID == request.NodeID && item.Status != StatusRevoked {
			if item.Status == StatusActive || item.Status == StatusSuspended {
				return JoinResponse{}, errors.New("node already has a relationship")
			}
			if item.Status == StatusPending || item.Status == StatusApproved {
				item.Status = StatusRevoked
				item.ClaimToken = ""
				item.IssuedToken = ""
				item.Token = ""
			}
		}
		if item.Status == StatusPending || item.Status == StatusApproved {
			pendingCount++
		}
	}
	if pendingCount >= 128 {
		return JoinResponse{}, errors.New("too many pending enrollments")
	}
	enrollmentID, err := randomID("enr_", 16)
	if err != nil {
		return JoinResponse{}, err
	}
	claimToken, err := randomID("claim_", 32)
	if err != nil {
		return JoinResponse{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(m.cfg.EnrollmentTTL.Duration())
	relationship := Relationship{
		EnrollmentID:   enrollmentID,
		PeerNodeID:     request.NodeID,
		PeerName:       request.NodeName,
		PeerURL:        strings.TrimRight(request.URL, "/"),
		Transport:      request.Transport,
		PublicKeyHash:  request.PublicKeyHash,
		Role:           RoleChild,
		Status:         StatusPending,
		PairingCode:    request.PairingCode,
		ClaimToken:     claimToken,
		Capabilities:   request.Capabilities,
		CapabilityHash: request.CapabilityHash,
		CreatedAt:      now,
		ExpiresAt:      expires,
	}
	m.doc.Relationships = append(m.doc.Relationships, relationship)
	if err := m.saveLocked(); err != nil {
		return JoinResponse{}, err
	}
	return JoinResponse{
		EnrollmentID: enrollmentID,
		ClaimToken:   claimToken,
		Status:       StatusPending,
		ParentNodeID: m.doc.Identity.NodeID,
		ExpiresAt:    expires,
	}, nil
}

func (m *Manager) List() []PublicRelationship {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expirePendingLocked(time.Now().UTC())
	result := make([]PublicRelationship, 0, len(m.doc.Relationships))
	for _, item := range m.doc.Relationships {
		result = append(result, m.publicLocked(item))
	}
	slices.SortFunc(result, func(a, b PublicRelationship) int {
		return strings.Compare(a.CreatedAt.Format(time.RFC3339Nano), b.CreatedAt.Format(time.RFC3339Nano))
	})
	return result
}

func (m *Manager) Approve(enrollmentID string, approval Approval) (PublicRelationship, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.relationshipLocked(enrollmentID)
	if err != nil {
		return PublicRelationship{}, err
	}
	if item.Status != StatusPending {
		return PublicRelationship{}, fmt.Errorf("enrollment is %s", item.Status)
	}
	if time.Now().UTC().After(item.ExpiresAt) {
		item.Status = StatusRevoked
		_ = m.saveLocked()
		return PublicRelationship{}, errors.New("enrollment expired")
	}
	approval, err = normalizePolicy(*item, approval, true)
	if err != nil {
		return PublicRelationship{}, err
	}
	token := ""
	if item.Transport != TransportBrowserWebSocket {
		token, err = randomID("ecsrel_", 32)
		if err != nil {
			return PublicRelationship{}, err
		}
	}
	item.Groups = uniqueStrings(approval.Groups)
	item.AllowedTools = uniqueStrings(approval.AllowedTools)
	item.AllowedSkills = uniqueStrings(approval.AllowedSkills)
	item.Scopes = uniqueStrings(approval.Scopes)
	item.AllowUpstreamExecute = boolPointer(*approval.AllowUpstreamExecute)
	item.IssuedToken = token
	item.Token = token
	item.PreviousToken = ""
	item.Status = StatusApproved
	item.PolicyVersion = 1
	item.ApprovedAt = time.Now().UTC()
	if err := m.saveLocked(); err != nil {
		return PublicRelationship{}, err
	}
	return m.publicLocked(*item), nil
}

func (m *Manager) UpdatePermissions(
	enrollmentID string,
	permissions Approval,
) (PublicRelationship, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.relationshipLocked(enrollmentID)
	if err != nil {
		return PublicRelationship{}, err
	}
	if item.Status != StatusApproved && item.Status != StatusActive && item.Status != StatusSuspended {
		return PublicRelationship{}, fmt.Errorf("permissions cannot be changed while enrollment is %s", item.Status)
	}
	if item.Role != RoleChild {
		return PublicRelationship{}, errors.New("controller relationship permissions are managed by the controller")
	}
	permissions, err = normalizePolicy(*item, permissions, false)
	if err != nil {
		return PublicRelationship{}, err
	}
	narrowed := policyNarrows(*item, permissions)
	item.Groups = uniqueStrings(permissions.Groups)
	item.AllowedTools = uniqueStrings(permissions.AllowedTools)
	item.AllowedSkills = uniqueStrings(permissions.AllowedSkills)
	item.Scopes = uniqueStrings(permissions.Scopes)
	item.AllowUpstreamExecute = boolPointer(*permissions.AllowUpstreamExecute)
	item.PolicyVersion++
	if item.PolicyVersion == 0 {
		item.PolicyVersion = 1
	}
	if narrowed && item.Transport != TransportBrowserWebSocket && item.Token != "" &&
		slices.Contains(item.Capabilities.Features, "relationship_token_rotation") {
		if item.Status == StatusApproved {
			token, tokenErr := randomID("ecsrel_", 32)
			if tokenErr != nil {
				return PublicRelationship{}, tokenErr
			}
			item.Token = token
			item.IssuedToken = token
		} else if item.PreviousToken == "" {
			token, tokenErr := randomID("ecsrel_", 32)
			if tokenErr != nil {
				return PublicRelationship{}, tokenErr
			}
			item.PreviousToken = item.Token
			item.Token = token
			if item.IssuedToken != "" {
				item.IssuedToken = token
			}
		}
	}
	if err := m.saveLocked(); err != nil {
		return PublicRelationship{}, err
	}
	return m.publicLocked(*item), nil
}

func (m *Manager) Claim(enrollmentID, claimToken string) (ClaimResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.relationshipLocked(enrollmentID)
	if err != nil {
		return ClaimResponse{}, err
	}
	if item.Transport == TransportBrowserWebSocket {
		return ClaimResponse{}, errors.New("browser relationship must use the WebSocket connection flow")
	}
	if !secureEqual(item.ClaimToken, claimToken) {
		return ClaimResponse{}, errors.New("invalid claim token")
	}
	response := ClaimResponse{
		Status:       item.Status,
		ParentNodeID: m.doc.Identity.NodeID,
		ParentName:   m.nodeName,
		ParentURL:    m.nodeURL,
	}
	if item.Status == StatusPending {
		if time.Now().UTC().After(item.ExpiresAt) {
			item.Status = StatusRevoked
			_ = m.saveLocked()
			return ClaimResponse{}, errors.New("enrollment expired")
		}
		return response, nil
	}
	if item.Status != StatusApproved && !(item.Status == StatusActive && item.IssuedToken != "") {
		return response, fmt.Errorf("enrollment is %s", item.Status)
	}
	response.Status = StatusActive
	response.Token = item.IssuedToken
	response.Groups = append([]string(nil), item.Groups...)
	response.AllowedTools = append([]string(nil), item.AllowedTools...)
	response.AllowedSkills = append([]string(nil), item.AllowedSkills...)
	response.Scopes = append([]string(nil), item.Scopes...)
	response.AllowUpstreamExecute = boolPointer(allowsUpstreamExecute(*item))
	response.LeaseTimeout = m.cfg.LeaseTimeout.Duration().String()
	response.PolicyVersion = item.PolicyVersion
	response.ApprovedAt = item.ApprovedAt
	item.Status = StatusActive
	if item.ClaimedAt.IsZero() {
		item.ClaimedAt = time.Now().UTC()
	}
	item.LastSeen = item.ClaimedAt
	if err := m.saveLocked(); err != nil {
		return ClaimResponse{}, err
	}
	return response, nil
}

func (m *Manager) SetStatus(enrollmentID, status string) (PublicRelationship, error) {
	if status != StatusSuspended && status != StatusRevoked && status != StatusActive {
		return PublicRelationship{}, errors.New("status must be active, suspended, or revoked")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.relationshipLocked(enrollmentID)
	if err != nil {
		return PublicRelationship{}, err
	}
	if item.Status == StatusRevoked && status != StatusRevoked {
		return PublicRelationship{}, errors.New("revoked relationship must enroll again")
	}
	if status == StatusActive && item.Transport != TransportBrowserWebSocket && item.Token == "" {
		return PublicRelationship{}, errors.New("relationship has no active credential")
	}
	item.Status = status
	if status == StatusRevoked {
		item.ClaimToken = ""
		item.IssuedToken = ""
		item.Token = ""
		item.PreviousToken = ""
	}
	if err := m.saveLocked(); err != nil {
		return PublicRelationship{}, err
	}
	return m.publicLocked(*item), nil
}

func (m *Manager) Authenticate(token, scope string) (PublicRelationship, bool) {
	if token == "" {
		return PublicRelationship{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range m.doc.Relationships {
		currentToken := secureEqual(item.Token, token)
		previousControllerToken := item.Role == RoleController &&
			secureEqual(item.PreviousToken, token)
		if item.Status != StatusActive || (!currentToken && !previousControllerToken) {
			continue
		}
		if item.Transport == TransportBrowserWebSocket {
			return PublicRelationship{}, false
		}
		if !item.LastSeen.IsZero() &&
			time.Now().UTC().After(item.LastSeen.Add(m.cfg.LeaseTimeout.Duration())) {
			return PublicRelationship{}, false
		}
		if scope != "" && !slices.Contains(item.Scopes, scope) {
			return PublicRelationship{}, false
		}
		if item.Role == RoleChild && isExecuteScope(scope) && !allowsUpstreamExecute(item) {
			return PublicRelationship{}, false
		}
		return m.publicLocked(item), true
	}
	return PublicRelationship{}, false
}

// PeersFor returns online non-browser peers whose declared and approved
// capabilities and scopes cover the requested work.
func (m *Manager) PeersFor(
	group string,
	requiredTools, requiredSkills []string,
	requiredScope string,
) []config.Peer {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var result []config.Peer
	for _, item := range m.doc.Relationships {
		if item.Status != StatusActive || item.Token == "" {
			continue
		}
		if item.Transport == TransportBrowserWebSocket {
			continue
		}
		if !item.LastSeen.IsZero() && now.After(item.LastSeen.Add(m.cfg.LeaseTimeout.Duration())) {
			continue
		}
		if group != "" && group != "all" && !slices.Contains(item.Groups, group) {
			continue
		}
		if requiredScope != "" && !slices.Contains(item.Scopes, requiredScope) {
			continue
		}
		if item.Role == RoleController && isExecuteScope(requiredScope) &&
			!allowsUpstreamExecute(item) {
			continue
		}
		if !relationshipCovers(item, requiredTools, requiredSkills) {
			continue
		}
		result = append(result, config.Peer{
			NodeID:    item.PeerNodeID,
			Name:      item.PeerName,
			URL:       item.PeerURL,
			Token:     outboundToken(item),
			Transport: TransportHTTP,
		})
	}
	return result
}

func outboundToken(item Relationship) string {
	// During a narrowing rotation the peer still accepts the previous token
	// until it confirms the replacement with a heartbeat.
	if item.Role == RoleChild && item.PreviousToken != "" {
		return item.PreviousToken
	}
	return item.Token
}

func relationshipCovers(item Relationship, requiredTools, requiredSkills []string) bool {
	for _, required := range requiredTools {
		if !slices.Contains(item.AllowedTools, required) || !hasTool(item.Capabilities, required) {
			return false
		}
	}
	for _, required := range requiredSkills {
		if !slices.Contains(item.AllowedSkills, required) || !hasSkill(item.Capabilities, required) {
			return false
		}
	}
	return true
}

func hasTool(capabilities protocol.NodeCapabilities, name string) bool {
	for _, item := range capabilities.Tools {
		if item.Name == name {
			return true
		}
	}
	return false
}

func hasSkill(capabilities protocol.NodeCapabilities, name string) bool {
	for _, item := range capabilities.Skills {
		if item.Name == name {
			return true
		}
	}
	return false
}

func (m *Manager) RecordHeartbeat(token string, heartbeat Heartbeat) (HeartbeatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.doc.Relationships {
		item := &m.doc.Relationships[index]
		currentToken := secureEqual(item.Token, token)
		previousToken := secureEqual(item.PreviousToken, token)
		if item.Status != StatusActive || item.Role != RoleChild ||
			item.Transport == TransportBrowserWebSocket ||
			(!currentToken && !previousToken) ||
			!slices.Contains(item.Scopes, "heartbeat:write") {
			continue
		}
		if item.PeerNodeID != heartbeat.NodeID {
			return HeartbeatResponse{}, errors.New("heartbeat node_id does not match credential")
		}
		now := time.Now().UTC()
		item.LastSeen = now
		durableChanged := item.IssuedToken != "" || item.ClaimToken != "" || item.PairingCode != ""
		if heartbeat.Capabilities != nil {
			if heartbeat.Capabilities.NodeID != "" &&
				heartbeat.Capabilities.NodeID != item.PeerNodeID {
				return HeartbeatResponse{}, errors.New("heartbeat capability node_id mismatch")
			}
			if heartbeat.Capabilities.Node != item.PeerName ||
				strings.TrimRight(heartbeat.Capabilities.URL, "/") != strings.TrimRight(item.PeerURL, "/") {
				return HeartbeatResponse{}, errors.New("heartbeat capability identity mismatch")
			}
			digest, err := CapabilityHash(*heartbeat.Capabilities)
			if err != nil {
				return HeartbeatResponse{}, err
			}
			if heartbeat.CapabilityHash != "" && heartbeat.CapabilityHash != digest {
				return HeartbeatResponse{}, errors.New("heartbeat capability hash mismatch")
			}
			if item.CapabilityHash != digest {
				previousTools := append([]string(nil), item.AllowedTools...)
				previousSkills := append([]string(nil), item.AllowedSkills...)
				item.Capabilities = *heartbeat.Capabilities
				item.CapabilityHash = digest
				toolNames := declaredCapabilityNames(item.Capabilities, "tool")
				skillNames := declaredCapabilityNames(item.Capabilities, "skill")
				item.AllowedTools = keepAllowed(item.AllowedTools, toolNames)
				item.AllowedSkills = keepAllowed(item.AllowedSkills, skillNames)
				if !slices.Equal(previousTools, item.AllowedTools) ||
					!slices.Equal(previousSkills, item.AllowedSkills) {
					item.PolicyVersion++
					if item.PolicyVersion == 0 {
						item.PolicyVersion = 1
					}
				}
				durableChanged = true
			}
		} else if heartbeat.CapabilityHash != "" {
			if item.CapabilityHash != "" && heartbeat.CapabilityHash != item.CapabilityHash {
				return HeartbeatResponse{}, errors.New("changed capability hash requires full capabilities")
			}
			if item.CapabilityHash == "" {
				item.CapabilityHash = heartbeat.CapabilityHash
				durableChanged = true
			}
		}
		if durableChanged {
			item.IssuedToken = ""
			item.ClaimToken = ""
			item.PairingCode = ""
			if err := m.saveLocked(); err != nil {
				return HeartbeatResponse{}, err
			}
		}
		response := HeartbeatResponse{
			Status:               StatusActive,
			ServerTime:           now,
			LeaseExpires:         now.Add(m.cfg.LeaseTimeout.Duration()),
			PolicyVersion:        item.PolicyVersion,
			Groups:               append([]string(nil), item.Groups...),
			AllowedTools:         append([]string(nil), item.AllowedTools...),
			AllowedSkills:        append([]string(nil), item.AllowedSkills...),
			Scopes:               append([]string(nil), item.Scopes...),
			AllowUpstreamExecute: boolPointer(allowsUpstreamExecute(*item)),
		}
		if previousToken {
			response.Token = item.Token
		}
		if currentToken && item.PreviousToken != "" {
			item.PreviousToken = ""
			if err := m.saveLocked(); err != nil {
				return HeartbeatResponse{}, err
			}
		}
		return response, nil
	}
	return HeartbeatResponse{}, errors.New("invalid or inactive relationship token")
}

func declaredCapabilityNames(
	capabilities protocol.NodeCapabilities,
	kind string,
) []string {
	var result []string
	switch kind {
	case "tool":
		for _, capability := range capabilities.Tools {
			result = append(result, capability.Name)
		}
	case "skill":
		for _, capability := range capabilities.Skills {
			result = append(result, capability.Name)
		}
	}
	for _, status := range capabilities.Availability {
		if status.Kind == kind {
			result = append(result, status.Name)
		}
	}
	return uniqueStrings(result)
}

func (m *Manager) InstallParent(response ClaimResponse) error {
	if response.Status != StatusActive || response.Token == "" || response.ParentNodeID == "" {
		return errors.New("active parent claim with token is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rotatePairingCode := func() error {
		if strings.TrimSpace(m.cfg.Parent.PairingCode) != "" {
			return nil
		}
		code, err := randomNumber(6)
		if err != nil {
			return err
		}
		m.doc.Identity.PairingCode = code
		return nil
	}
	for index := range m.doc.Relationships {
		item := &m.doc.Relationships[index]
		if item.PeerNodeID != response.ParentNodeID || item.Role != RoleController {
			continue
		}
		item.PeerName = response.ParentName
		item.PeerURL = strings.TrimRight(response.ParentURL, "/")
		item.Status = StatusActive
		item.Token = response.Token
		item.Groups = append([]string(nil), response.Groups...)
		item.AllowedTools = append([]string(nil), response.AllowedTools...)
		item.AllowedSkills = append([]string(nil), response.AllowedSkills...)
		item.Scopes = append([]string(nil), response.Scopes...)
		item.AllowUpstreamExecute = boolPointer(
			response.AllowUpstreamExecute == nil || *response.AllowUpstreamExecute,
		)
		item.PolicyVersion = response.PolicyVersion
		item.ApprovedAt = response.ApprovedAt
		item.ClaimedAt = time.Now().UTC()
		item.LastSeen = item.ClaimedAt
		m.doc.ParentPending = nil
		if err := rotatePairingCode(); err != nil {
			return err
		}
		return m.saveLocked()
	}
	now := time.Now().UTC()
	m.doc.Relationships = append(m.doc.Relationships, Relationship{
		EnrollmentID:  "parent-" + response.ParentNodeID,
		PeerNodeID:    response.ParentNodeID,
		PeerName:      response.ParentName,
		PeerURL:       strings.TrimRight(response.ParentURL, "/"),
		Role:          RoleController,
		Status:        StatusActive,
		Token:         response.Token,
		Groups:        append([]string(nil), response.Groups...),
		AllowedTools:  append([]string(nil), response.AllowedTools...),
		AllowedSkills: append([]string(nil), response.AllowedSkills...),
		Scopes:        append([]string(nil), response.Scopes...),
		AllowUpstreamExecute: boolPointer(
			response.AllowUpstreamExecute == nil || *response.AllowUpstreamExecute,
		),
		PolicyVersion: response.PolicyVersion,
		CreatedAt:     now,
		ApprovedAt:    response.ApprovedAt,
		ClaimedAt:     now,
		LastSeen:      now,
	})
	m.doc.ParentPending = nil
	if err := rotatePairingCode(); err != nil {
		return err
	}
	return m.saveLocked()
}

func (m *Manager) activeParentLocked() *Relationship {
	for index := range m.doc.Relationships {
		item := &m.doc.Relationships[index]
		if item.Role == RoleController &&
			(item.Status == StatusActive || item.Status == StatusSuspended) &&
			(m.cfg.Parent.NodeID == "" || item.PeerNodeID == m.cfg.Parent.NodeID) {
			return item
		}
	}
	return nil
}

func (m *Manager) validateJoin(request JoinRequest) error {
	if !nodeIDPattern.MatchString(request.NodeID) {
		return errors.New("node_id must be ecs_ followed by 32 lowercase hexadecimal characters")
	}
	if request.NodeID == m.NodeID() {
		return errors.New("node cannot enroll with itself")
	}
	if strings.TrimSpace(request.NodeName) == "" {
		return errors.New("node_name is required")
	}
	parsed, err := url.Parse(request.URL)
	if request.Transport == TransportBrowserWebSocket {
		if !request.BrowserProofOK {
			return errors.New("browser enrollment requires verified public-key proof")
		}
		if err != nil || parsed.Scheme != "browser" || parsed.Host != request.NodeID ||
			parsed.Path != "" {
			return errors.New("browser url must be browser:// followed by its node_id")
		}
		if !publicKeyPattern.MatchString(request.PublicKeyHash) {
			return errors.New("browser public_key_hash must be a SHA-256 hexadecimal digest")
		}
		if err := validateBrowserLeaf(request.Capabilities); err != nil {
			return err
		}
	} else {
		if request.Transport != "" {
			return errors.New("unsupported enrollment transport")
		}
		if err != nil || parsed.Host == "" ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("url must be an http(s) URL")
		}
	}
	if request.ParentNodeID != "" && request.ParentNodeID != m.NodeID() {
		return errors.New("parent_node_id does not match this node")
	}
	if !pairingCodePattern.MatchString(request.PairingCode) {
		return errors.New("pairing_code must contain 6 to 12 letters or digits")
	}
	if request.Capabilities.NodeID != "" && request.Capabilities.NodeID != request.NodeID {
		return errors.New("capability node_id does not match enrollment")
	}
	if request.Capabilities.Node != request.NodeName ||
		strings.TrimRight(request.Capabilities.URL, "/") != strings.TrimRight(request.URL, "/") {
		return errors.New("capability identity does not match enrollment")
	}
	digest, err := CapabilityHash(request.Capabilities)
	if err != nil {
		return err
	}
	if request.CapabilityHash == "" || digest != request.CapabilityHash {
		return errors.New("capability hash does not match enrollment")
	}
	return nil
}

func CapabilityHash(capabilities protocol.NodeCapabilities) (string, error) {
	capabilities.CapabilityHash = ""
	capabilities.LastSeen = nil
	encoded, err := json.Marshal(capabilities)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:8]), nil
}

func (m *Manager) publicLocked(item Relationship) PublicRelationship {
	status := item.Status
	var leaseExpires time.Time
	if status == StatusActive && !item.LastSeen.IsZero() {
		leaseExpires = item.LastSeen.Add(m.cfg.LeaseTimeout.Duration())
		if time.Now().UTC().After(leaseExpires) {
			status = StatusOffline
		}
	}
	return PublicRelationship{
		EnrollmentID:         item.EnrollmentID,
		PeerNodeID:           item.PeerNodeID,
		PeerName:             item.PeerName,
		PeerURL:              item.PeerURL,
		Transport:            item.Transport,
		Role:                 item.Role,
		Status:               status,
		PairingCode:          item.PairingCode,
		Groups:               append([]string(nil), item.Groups...),
		AllowedTools:         append([]string(nil), item.AllowedTools...),
		AllowedSkills:        append([]string(nil), item.AllowedSkills...),
		Scopes:               append([]string(nil), item.Scopes...),
		AllowUpstreamExecute: allowsUpstreamExecute(item),
		Capabilities:         item.Capabilities,
		CapabilityHash:       item.CapabilityHash,
		PolicyVersion:        item.PolicyVersion,
		CreatedAt:            item.CreatedAt,
		ExpiresAt:            item.ExpiresAt,
		ApprovedAt:           item.ApprovedAt,
		ClaimedAt:            item.ClaimedAt,
		LastSeen:             item.LastSeen,
		LeaseExpires:         leaseExpires,
	}
}

func (m *Manager) relationshipLocked(enrollmentID string) (*Relationship, error) {
	for index := range m.doc.Relationships {
		if m.doc.Relationships[index].EnrollmentID == enrollmentID {
			return &m.doc.Relationships[index], nil
		}
	}
	return nil, os.ErrNotExist
}

func (m *Manager) expirePendingLocked(now time.Time) {
	changed := false
	for index := range m.doc.Relationships {
		item := &m.doc.Relationships[index]
		if (item.Status == StatusPending || item.Status == StatusApproved) &&
			!item.ExpiresAt.IsZero() && now.After(item.ExpiresAt) {
			item.Status = StatusRevoked
			item.ClaimToken = ""
			item.IssuedToken = ""
			item.Token = ""
			changed = true
		}
	}
	if changed {
		_ = m.saveLocked()
	}
}

func (m *Manager) loadOrCreate() error {
	data, err := os.ReadFile(m.path)
	if err == nil {
		if err := json.Unmarshal(data, &m.doc); err != nil {
			return fmt.Errorf("decode network auth state: %w", err)
		}
		if !nodeIDPattern.MatchString(m.doc.Identity.NodeID) {
			return errors.New("network auth state contains invalid node_id")
		}
		changed := false
		for index := range m.doc.Relationships {
			item := &m.doc.Relationships[index]
			if item.AllowUpstreamExecute == nil {
				item.AllowUpstreamExecute = boolPointer(true)
				changed = true
			}
		}
		if changed {
			return m.saveLocked()
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	nodeID, err := randomID("ecs_", 16)
	if err != nil {
		return err
	}
	codeRaw, err := randomNumber(6)
	if err != nil {
		return err
	}
	m.doc = document{
		Identity:      Identity{NodeID: nodeID, PairingCode: codeRaw, CreatedAt: time.Now().UTC()},
		Relationships: []Relationship{},
	}
	return m.saveLocked()
}

func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(m.doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(m.cfg.Directory, ".network-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, m.path)
}

func randomID(prefix string, byteCount int) (string, error) {
	raw := make([]byte, byteCount)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw), nil
}

func randomNumber(length int) (string, error) {
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	result := make([]byte, length)
	for index, value := range raw {
		result[index] = '0' + value%10
	}
	return string(result), nil
}

func secureEqual(expected, given string) bool {
	if expected == "" || len(expected) != len(given) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(given)) == 1
}
