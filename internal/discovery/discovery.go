package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"ecs/internal/config"
	"ecs/internal/office"
	"ecs/internal/platform"
	"ecs/internal/protocol"
	"ecs/internal/roles"
	"ecs/internal/skills"
	"ecs/internal/tools"
)

const protocolVersion = "ecs/1"

type Manager struct {
	cfg                config.DiscoveryConfig
	nested             config.NestedConfig
	nodeName           string
	nodeID             string
	model              string
	authToken          string
	approvalMode       string
	registry           *tools.Registry
	skills             *skills.Registry
	roles              *roles.Registry
	office             *office.Registry
	provider           platform.DiscoveryProvider
	platform           protocol.PlatformCapabilities
	logger             *slog.Logger
	client             *http.Client
	registryClient     *http.Client
	registryController *RegistryController
	mu                 sync.Mutex
	nodes              map[string]protocol.NodeCapabilities
	hydrated           map[string]string
	unavailable        map[string]string
}

func (m *Manager) SetSkillRegistry(registry *skills.Registry) {
	m.skills = registry
}

func (m *Manager) SetAgentRegistry(registry *roles.Registry) {
	m.roles = registry
}

func (m *Manager) SetOfficeRegistry(registry *office.Registry) {
	m.office = registry
}

func (m *Manager) SetNodeID(nodeID string) {
	m.nodeID = nodeID
}

func (m *Manager) SetPlatformCapabilities(capabilities protocol.PlatformCapabilities) {
	m.platform = capabilities
}

// SetHTTPTransport keeps capability hydration and registry access on the
// platform-owned network boundary used by the rest of ECS.
func (m *Manager) SetHTTPTransport(transport http.RoundTripper) {
	if transport == nil {
		return
	}
	m.client.Transport = transport
	m.registryClient.Transport = transport
}

func New(
	cfg config.DiscoveryConfig,
	nested config.NestedConfig,
	nodeName string,
	model string,
	authToken string,
	approvalMode string,
	registry *tools.Registry,
	logger *slog.Logger,
) *Manager {
	return NewWithProvider(
		cfg,
		nested,
		nodeName,
		model,
		authToken,
		approvalMode,
		registry,
		logger,
		&platform.MDNSDiscovery{},
	)
}

func NewWithProvider(
	cfg config.DiscoveryConfig,
	nested config.NestedConfig,
	nodeName string,
	model string,
	authToken string,
	approvalMode string,
	registry *tools.Registry,
	logger *slog.Logger,
	provider platform.DiscoveryProvider,
) *Manager {
	manager := &Manager{
		cfg:            cfg,
		nested:         nested,
		nodeName:       nodeName,
		model:          model,
		authToken:      authToken,
		approvalMode:   approvalMode,
		registry:       registry,
		provider:       provider,
		logger:         logger,
		client:         &http.Client{Timeout: 3 * time.Second},
		registryClient: &http.Client{Timeout: cfg.Registry.RequestTimeout.Duration()},
		nodes:          make(map[string]protocol.NodeCapabilities),
		hydrated:       make(map[string]string),
		unavailable:    make(map[string]string),
	}
	if cfg.Registry.Serve {
		manager.registryController = newRegistryController(cfg.Registry)
	}
	return manager
}

func (m *Manager) Start(ctx context.Context) error {
	if !m.cfg.Enabled && !m.cfg.Registry.Enabled {
		return nil
	}
	if m.cfg.Enabled {
		if m.provider == nil {
			return errors.New("discovery provider is not configured")
		}
		if err := m.provider.Start(ctx, m.cfg.ServiceType, m.acceptAnnouncement); err != nil {
			return err
		}
		go m.announceLoop(ctx)
		m.logger.Info(
			"automatic local discovery enabled",
			"provider", m.provider.Name(),
			"service_type", m.cfg.ServiceType,
			"advertise_url", m.cfg.AdvertiseURL,
		)
	}
	if m.cfg.Registry.Enabled {
		if !registryNodeIDPattern.MatchString(m.nodeID) {
			return errors.New("registry discovery requires a persistent network node_id")
		}
		go m.registryLoop(ctx)
		m.logger.Info(
			"controller registry discovery enabled",
			"controllers", len(m.cfg.Registry.ControllerURLs),
			"advertise_url", m.cfg.AdvertiseURL,
		)
	}
	return nil
}

func (m *Manager) Capabilities() protocol.NodeCapabilities {
	manifests := m.registry.All()
	capabilities := make([]protocol.ToolCapability, 0, len(manifests))
	availability := make([]protocol.CapabilityAvailability, 0)
	for _, manifest := range manifests {
		status := m.registry.Availability(manifest.Name)
		if !status.Available {
			availability = append(availability, protocol.CapabilityAvailability{
				Kind:      "tool",
				Name:      manifest.Name,
				Available: false,
				Reason:    status.Reason,
			})
			continue
		}
		risk := manifest.Risk
		if risk == "" {
			risk = "low"
		}
		capabilities = append(capabilities, protocol.ToolCapability{
			Name:                 manifest.Name,
			Version:              manifest.Version,
			Description:          manifest.Description,
			Capabilities:         append([]string(nil), manifest.Capabilities...),
			InputSchema:          manifest.InputSchema,
			CanonicalInputSchema: manifest.CanonicalSchema,
			OutputSchema:         manifest.OutputSchema,
			Risk:                 risk,
			RequiresApproval: m.approvalMode == "all" ||
				(m.approvalMode == "high_risk" && (risk == "high" || manifest.RequiresApproval)),
		})
	}
	children := make([]protocol.NodeRef, 0, len(m.nested.Children))
	if m.nested.Enabled {
		for _, child := range m.nested.Children {
			children = append(children, protocol.NodeRef{Name: child.Name, URL: child.URL})
		}
	}
	result := protocol.NodeCapabilities{
		Protocol: protocolVersion,
		NodeID:   m.nodeID,
		Node:     m.nodeName,
		URL:      m.cfg.AdvertiseURL,
		Model:    m.model,
		Platform: m.platform,
		Groups:   append([]string(nil), m.cfg.Groups...),
		Features: []string{
			"recursive_reply",
			"sessions",
			"tool_approval",
			"skills",
			"persistent_trace",
			"formal_agent_roles",
		},
		Children:     children,
		Tools:        capabilities,
		Availability: availability,
	}
	if m.nodeID != "" {
		result.Features = append(
			result.Features,
			"network_auth",
			"relationship_token_rotation",
		)
	}
	if m.cfg.Registry.Enabled {
		result.Features = append(result.Features, "registry_discovery")
	}
	if m.cfg.Registry.Serve {
		result.Features = append(result.Features, "registry_controller")
	}
	if m.skills != nil {
		result.Skills = m.skills.Capabilities()
		availableSkills := make(map[string]bool, len(result.Skills))
		for _, capability := range result.Skills {
			availableSkills[capability.Name] = true
		}
		for _, manifest := range m.skills.All() {
			if availableSkills[manifest.Name] {
				continue
			}
			status := m.skills.Availability(manifest.Name)
			result.Availability = append(result.Availability, protocol.CapabilityAvailability{
				Kind:      "skill",
				Name:      manifest.Name,
				Available: false,
				Reason:    status.Reason,
			})
		}
	}
	if m.roles != nil {
		for _, role := range m.roles.All() {
			result.Agents = append(result.Agents, protocol.AgentCapability{
				Name:          role.Name,
				Purpose:       role.Purpose,
				ExecutionMode: role.ExecutionMode,
				Model:         role.Model,
				AllowedSkills: append([]string(nil), role.AllowedSkills...),
				AllowedTools:  append([]string(nil), role.AllowedTools...),
			})
		}
		defaultPipeline, _ := m.roles.DefaultPipeline()
		for _, pipeline := range m.roles.Pipelines() {
			result.Pipelines = append(result.Pipelines, protocol.PipelineCapability{
				Name:      pipeline.Name,
				Strategy:  pipeline.Strategy,
				Planner:   pipeline.Planner,
				Executors: append([]string(nil), pipeline.Executors...),
				Fallback:  pipeline.Fallback,
				Verifier:  pipeline.Verifier,
				Default:   pipeline.Name == defaultPipeline.Name,
			})
		}
	}
	if m.office != nil {
		for _, component := range m.office.Capabilities() {
			result.OfficeComponents = append(result.OfficeComponents, protocol.OfficeComponentCapability{
				Name: component.Name, Version: component.Version, Description: component.Description,
				Intents: component.Intents, Agents: component.Agents, Stages: component.Stages,
				InputSchema:        component.InputSchema,
				RequiredConnectors: component.RequiredConnectors,
				MissingConnectors:  component.MissingConnectors, Ready: component.Ready,
			})
		}
		if len(result.OfficeComponents) > 0 {
			result.Features = append(result.Features, "office_components")
		}
	}
	encoded, err := json.Marshal(result)
	if err == nil {
		sum := sha256.Sum256(encoded)
		result.CapabilityHash = fmt.Sprintf("%x", sum[:8])
	}
	return result
}

func (m *Manager) Snapshot() []protocol.NodeCapabilities {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireLocked(time.Now())
	result := make([]protocol.NodeCapabilities, 0, len(m.nodes))
	for _, node := range m.nodes {
		result = append(result, node)
	}
	slices.SortFunc(result, func(a, b protocol.NodeCapabilities) int {
		if a.Node < b.Node {
			return -1
		}
		if a.Node > b.Node {
			return 1
		}
		return 0
	})
	return result
}

func (m *Manager) Peers(group string) []config.Peer {
	return m.PeersFor(group, nil, nil)
}

func (m *Manager) PeersFor(group string, requiredTools, requiredSkills []string) []config.Peer {
	// Discovery is identity-free. Without an explicitly configured shared
	// token it remains visible in topology but cannot enter an execution pool.
	if strings.TrimSpace(m.authToken) == "" {
		return nil
	}
	nodes := m.Snapshot()
	peers := make([]config.Peer, 0, len(nodes))
	for _, node := range nodes {
		if group != "all" && !slices.Contains(node.Groups, group) {
			continue
		}
		if !coversCapabilities(node, requiredTools, requiredSkills) {
			continue
		}
		peers = append(peers, config.Peer{
			NodeID: node.NodeID,
			Name:   node.Node,
			URL:    node.URL,
			Token:  m.authToken,
		})
	}
	return peers
}

func coversCapabilities(node protocol.NodeCapabilities, requiredTools, requiredSkills []string) bool {
	for _, required := range requiredTools {
		found := false
		for _, capability := range node.Tools {
			if capability.Name == required {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, required := range requiredSkills {
		found := false
		for _, capability := range node.Skills {
			if capability.Name == required {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (m *Manager) announceLoop(ctx context.Context) {
	send := func() {
		payload, err := json.Marshal(compactAnnouncement(m.Capabilities()))
		if err != nil {
			return
		}
		if err := m.provider.Announce(ctx, m.cfg.ServiceType, payload); err != nil &&
			ctx.Err() == nil {
			m.logger.Warn("discovery announce failed", "error", err)
		}
	}
	send()
	ticker := time.NewTicker(m.cfg.Interval.Duration())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}

func compactAnnouncement(full protocol.NodeCapabilities) protocol.NodeCapabilities {
	return protocol.NodeCapabilities{
		Protocol:       full.Protocol,
		NodeID:         full.NodeID,
		Node:           full.Node,
		URL:            full.URL,
		Model:          full.Model,
		Platform:       full.Platform,
		Groups:         append([]string(nil), full.Groups...),
		Features:       append([]string(nil), full.Features...),
		Children:       append([]protocol.NodeRef(nil), full.Children...),
		CapabilityHash: full.CapabilityHash,
		Tools:          []protocol.ToolCapability{},
		Availability:   append([]protocol.CapabilityAvailability(nil), full.Availability...),
	}
}

func (m *Manager) acceptAnnouncement(ctx context.Context, payload []byte) {
	var announced protocol.NodeCapabilities
	if err := json.Unmarshal(payload, &announced); err != nil {
		return
	}
	if announced.Protocol != protocolVersion || announced.Node == "" || announced.URL == "" {
		return
	}
	if err := validateAnnouncementURL(announced.URL); err != nil {
		return
	}
	if announced.NodeID != "" && announced.NodeID == m.nodeID {
		return
	}
	if announced.NodeID == "" && announced.Node == m.nodeName && announced.URL == m.cfg.AdvertiseURL {
		return
	}

	key := capabilityKey(announced)
	lastSeen := time.Now().UTC()
	announced.LastSeen = &lastSeen
	m.mu.Lock()
	previous, known := m.nodes[key]
	alreadyHydrated := announced.CapabilityHash != "" &&
		m.hydrated[key] == announced.CapabilityHash
	hydrationUnavailable := announced.CapabilityHash != "" &&
		m.unavailable[key] == announced.CapabilityHash
	if alreadyHydrated {
		previous.Protocol = announced.Protocol
		previous.NodeID = announced.NodeID
		previous.Node = announced.Node
		previous.URL = announced.URL
		previous.Model = announced.Model
		previous.Platform = announced.Platform
		previous.Groups = append([]string(nil), announced.Groups...)
		previous.Features = append([]string(nil), announced.Features...)
		previous.Children = append([]protocol.NodeRef(nil), announced.Children...)
		previous.LastSeen = &lastSeen
		m.nodes[key] = previous
		m.expireLocked(time.Now())
		m.mu.Unlock()
		return
	}
	m.nodes[key] = announced
	m.expireLocked(time.Now())
	m.mu.Unlock()

	// Older ECS nodes broadcast their complete capabilities and have no hash.
	if announced.CapabilityHash == "" && capabilitiesPresent(announced) {
		if !known {
			m.logger.Info("node discovered", "node", announced.Node, "url", announced.URL, "tools", len(announced.Tools))
		}
		return
	}

	// Authenticated nodes deliberately keep full capabilities behind HTTP
	// authorization. Their compact announcement is still enough for enrollment.
	if slices.Contains(announced.Features, "network_auth") && m.authToken == "" {
		if !known {
			m.logger.Info("authenticated node discovered", "node", announced.Node, "node_id", announced.NodeID, "url", announced.URL)
		}
		return
	}
	if hydrationUnavailable {
		return
	}
	hydrated, err := m.fetchCapabilities(ctx, announced)
	if err != nil {
		m.mu.Lock()
		current, currentExists := m.nodes[key]
		if currentExists && current.CapabilityHash == announced.CapabilityHash {
			if m.unavailable == nil {
				m.unavailable = make(map[string]string)
			}
			m.unavailable[key] = announced.CapabilityHash
		}
		m.mu.Unlock()
		m.logger.Warn("discovered node capabilities unavailable", "node", announced.Node, "url", announced.URL, "error", err)
		return
	}
	hydrated.LastSeen = &lastSeen
	m.mu.Lock()
	current, currentExists := m.nodes[key]
	if !currentExists || current.CapabilityHash != announced.CapabilityHash {
		m.mu.Unlock()
		return
	}
	m.nodes[key] = hydrated
	m.hydrated[key] = announced.CapabilityHash
	delete(m.unavailable, key)
	m.expireLocked(time.Now())
	m.mu.Unlock()
	if !known {
		m.logger.Info("node discovered", "node", hydrated.Node, "url", hydrated.URL, "tools", len(hydrated.Tools))
	}
}

func validateAnnouncementURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("registry node URL must be an http(s) URL")
	}
	return nil
}

// Stable node IDs keep discovery identity intact when an address changes.
// URL remains the compatibility key for older ECS announcements.
func capabilityKey(node protocol.NodeCapabilities) string {
	if node.NodeID != "" {
		return "node:" + node.NodeID
	}
	return "url:" + strings.TrimRight(node.URL, "/")
}

func capabilitiesPresent(node protocol.NodeCapabilities) bool {
	return len(node.Tools) > 0 || len(node.Skills) > 0 || len(node.Agents) > 0 ||
		len(node.Pipelines) > 0 || len(node.OfficeComponents) > 0
}

func (m *Manager) fetchCapabilities(ctx context.Context, announced protocol.NodeCapabilities) (protocol.NodeCapabilities, error) {
	endpoint := strings.TrimRight(announced.URL, "/") + "/v1/capabilities"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return protocol.NodeCapabilities{}, err
	}
	if m.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+m.authToken)
	}
	response, err := m.client.Do(request)
	if err != nil {
		return protocol.NodeCapabilities{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		diagnostic, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return protocol.NodeCapabilities{}, fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(diagnostic)))
	}
	var full protocol.NodeCapabilities
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&full); err != nil {
		return protocol.NodeCapabilities{}, err
	}
	if full.Protocol != protocolVersion || full.Node != announced.Node ||
		(announced.NodeID != "" && full.NodeID != announced.NodeID) ||
		strings.TrimRight(full.URL, "/") != strings.TrimRight(announced.URL, "/") {
		return protocol.NodeCapabilities{}, errors.New("capability identity does not match announcement")
	}
	if announced.CapabilityHash != "" && full.CapabilityHash != announced.CapabilityHash {
		return protocol.NodeCapabilities{}, errors.New("capability hash does not match announcement")
	}
	return full, nil
}

func (m *Manager) expireLocked(now time.Time) {
	for key, node := range m.nodes {
		if node.LastSeen == nil || now.Sub(*node.LastSeen) > m.cfg.Expiry.Duration() {
			delete(m.nodes, key)
			delete(m.hydrated, key)
			delete(m.unavailable, key)
			m.logger.Info("node expired", "node", node.Node, "url", node.URL)
		}
	}
}
