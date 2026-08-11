package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"ecs/internal/config"
	"ecs/internal/networkauth"
	"ecs/internal/protocol"
	"ecs/internal/tools"
)

type adminRuntime struct {
	StartedAt       time.Time `json:"started_at"`
	UptimeSeconds   int64     `json:"uptime_seconds"`
	Goroutines      int       `json:"goroutines"`
	MemoryAlloc     uint64    `json:"memory_alloc_bytes"`
	MemorySystem    uint64    `json:"memory_system_bytes"`
	ActiveRequests  int       `json:"active_requests"`
	RequestCapacity int       `json:"request_capacity"`
}

type adminNode struct {
	ID           string                     `json:"id"`
	Name         string                     `json:"name"`
	URL          string                     `json:"url,omitempty"`
	Source       string                     `json:"source"`
	Status       string                     `json:"status"`
	Access       string                     `json:"access"`
	CanReply     bool                       `json:"can_reply"`
	LastSeen     *time.Time                 `json:"last_seen,omitempty"`
	Capabilities *protocol.NodeCapabilities `json:"capabilities,omitempty"`
	Runtime      *adminRuntime              `json:"runtime,omitempty"`
}

type adminEdge struct {
	Parent string `json:"parent"`
	Child  string `json:"child"`
	Source string `json:"source"`
}

type adminOverview struct {
	GeneratedAt time.Time   `json:"generated_at"`
	LocalNodeID string      `json:"local_node_id"`
	Nodes       []adminNode `json:"nodes"`
	Edges       []adminEdge `json:"edges"`
}

type adminTool struct {
	Manifest     tools.Manifest `json:"manifest"`
	Enabled      bool           `json:"enabled"`
	Available    bool           `json:"available"`
	Reason       string         `json:"unavailable_reason,omitempty"`
	Dependencies []string       `json:"skill_dependencies,omitempty"`
}

func (s *Server) adminOverview(w http.ResponseWriter, _ *http.Request) {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	localCapabilities := s.discovery.Capabilities()
	nodes := []adminNode{{
		ID:           "local",
		Name:         s.cfg.NodeName,
		URL:          localCapabilities.URL,
		Source:       "local",
		Status:       "online",
		Access:       "local",
		CanReply:     true,
		Capabilities: &localCapabilities,
		Runtime: &adminRuntime{
			StartedAt:       s.startedAt,
			UptimeSeconds:   int64(time.Since(s.startedAt).Seconds()),
			Goroutines:      runtime.NumGoroutine(),
			MemoryAlloc:     memory.Alloc,
			MemorySystem:    memory.Sys,
			ActiveRequests:  len(s.slots),
			RequestCapacity: cap(s.slots),
		},
	}}
	edges := make([]adminEdge, 0)
	knownByURL := map[string]string{normalizedURL(localCapabilities.URL): "local"}
	knownByName := map[string]string{s.cfg.NodeName: "local"}
	knownByNodeID := make(map[string]int)
	for _, remote := range s.discovery.Snapshot() {
		id := "discovered:" + remote.Node
		copyOfRemote := remote
		nodes, _ = upsertAdminNode(nodes, knownByNodeID, remote.NodeID, adminNode{
			ID:           id,
			Name:         remote.Node,
			URL:          remote.URL,
			Source:       "discovered",
			Status:       "online",
			Access:       "shared-node-token",
			CanReply:     strings.TrimSpace(s.cfg.AuthToken) != "",
			LastSeen:     remote.LastSeen,
			Capabilities: &copyOfRemote,
		})
		knownByURL[normalizedURL(remote.URL)] = id
		knownByName[remote.Node] = id
	}
	if s.network != nil {
		for _, relationship := range s.network.List() {
			if relationship.Status == networkauth.StatusPending ||
				relationship.Status == networkauth.StatusApproved ||
				relationship.Status == networkauth.StatusRevoked {
				continue
			}
			id := "network:" + relationship.PeerNodeID
			copyOfCapabilities := relationship.Capabilities
			allowedTools := make(map[string]bool, len(relationship.AllowedTools))
			for _, name := range relationship.AllowedTools {
				allowedTools[name] = true
			}
			toolItems := make([]protocol.ToolCapability, 0, len(copyOfCapabilities.Tools))
			for _, item := range copyOfCapabilities.Tools {
				if allowedTools[item.Name] {
					toolItems = append(toolItems, item)
				}
			}
			copyOfCapabilities.Tools = toolItems
			allowedSkills := make(map[string]bool, len(relationship.AllowedSkills))
			for _, name := range relationship.AllowedSkills {
				allowedSkills[name] = true
			}
			skillItems := make([]protocol.SkillCapability, 0, len(copyOfCapabilities.Skills))
			for _, item := range copyOfCapabilities.Skills {
				if allowedSkills[item.Name] {
					skillItems = append(skillItems, item)
				}
			}
			copyOfCapabilities.Skills = skillItems
			lastSeen := relationship.LastSeen
			status := "unknown"
			if relationship.Status == networkauth.StatusActive {
				status = "online"
			}
			var replacedID string
			nodes, replacedID = upsertAdminNode(nodes, knownByNodeID, relationship.PeerNodeID, adminNode{
				ID:     id,
				Name:   relationship.PeerName,
				URL:    relationship.PeerURL,
				Source: "network",
				Status: status,
				Access: "relationship-token",
				CanReply: slices.Contains(relationship.Scopes, "reply:execute") &&
					(relationship.Role != networkauth.RoleController ||
						relationship.AllowUpstreamExecute),
				LastSeen:     &lastSeen,
				Capabilities: &copyOfCapabilities,
			})
			// An authenticated relationship is the authoritative view when the
			// same persistent node was also seen through multicast discovery.
			replaceKnownAdminNodeID(knownByURL, replacedID, id)
			replaceKnownAdminNodeID(knownByName, replacedID, id)
			knownByURL[normalizedURL(relationship.PeerURL)] = id
			knownByName[relationship.PeerName] = id
			if relationship.Role == networkauth.RoleController {
				edges = append(edges, adminEdge{Parent: id, Child: "local", Source: "network"})
			} else {
				edges = append(edges, adminEdge{Parent: "local", Child: id, Source: "network"})
			}
		}
	}
	for _, child := range s.cfg.Nested.Children {
		id := knownByURL[normalizedURL(child.URL)]
		if id == "" {
			id = "configured:" + child.Name
			nodes = append(nodes, adminNode{
				ID:       id,
				Name:     child.Name,
				URL:      child.URL,
				Source:   "configured",
				Status:   "unknown",
				Access:   configuredAccess(child.Token),
				CanReply: true,
			})
			knownByURL[normalizedURL(child.URL)] = id
			knownByName[child.Name] = id
		} else {
			for index := range nodes {
				if nodes[index].ID == id {
					nodes[index].CanReply = true
					break
				}
			}
		}
		edges = append(edges, adminEdge{Parent: "local", Child: id, Source: "nested"})
	}
	for _, node := range nodes {
		if node.Capabilities == nil {
			continue
		}
		for _, child := range node.Capabilities.Children {
			childID := knownByURL[normalizedURL(child.URL)]
			if childID == "" {
				childID = knownByName[child.Name]
			}
			if childID == "" {
				childID = "declared:" + child.Name
				nodes = append(nodes, adminNode{
					ID: childID, Name: child.Name, URL: child.URL,
					Source: "recursive-declaration", Status: "unknown", Access: "not-configured",
				})
				knownByURL[normalizedURL(child.URL)] = childID
				knownByName[child.Name] = childID
			}
			edges = append(edges, adminEdge{Parent: node.ID, Child: childID, Source: "declared-child"})
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Source == "local" {
			return true
		}
		if nodes[j].Source == "local" {
			return false
		}
		return nodes[i].Name < nodes[j].Name
	})
	writeJSON(w, http.StatusOK, adminOverview{
		GeneratedAt: time.Now().UTC(),
		LocalNodeID: "local",
		Nodes:       nodes,
		Edges:       uniqueEdges(edges),
	})
}

func upsertAdminNode(
	nodes []adminNode,
	knownByNodeID map[string]int,
	nodeID string,
	node adminNode,
) ([]adminNode, string) {
	if nodeID != "" {
		if index, ok := knownByNodeID[nodeID]; ok {
			replacedID := nodes[index].ID
			nodes[index] = node
			return nodes, replacedID
		}
		knownByNodeID[nodeID] = len(nodes)
	}
	return append(nodes, node), ""
}

func replaceKnownAdminNodeID(index map[string]string, oldID, newID string) {
	if oldID == "" || oldID == newID {
		return
	}
	for key, id := range index {
		if id == oldID {
			index[key] = newID
		}
	}
}

func (s *Server) adminTools(w http.ResponseWriter, _ *http.Request) {
	items, err := s.loadAdminTools()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) saveAdminTool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("tool")
	var manifest tools.Manifest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid tool manifest: "+err.Error())
		return
	}
	if manifest.Name != name {
		writeError(w, http.StatusBadRequest, "manifest name must match URL tool name")
		return
	}
	if err := tools.ValidateManifest(manifest); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data = append(data, '\n')
	if err := s.updateToolFile(name, data, false); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.logger.Info("admin tool saved", "tool", name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved", "tool": name})
}

func (s *Server) deleteAdminTool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("tool")
	if err := s.updateToolFile(name, nil, true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "tool manifest not found")
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.logger.Warn("admin tool removed", "tool", name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "tool": name})
}

func (s *Server) adminLogs(w http.ResponseWriter, r *http.Request) {
	if s.logs == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > s.cfg.Admin.LogCapacity {
		limit = min(200, s.cfg.Admin.LogCapacity)
	}
	writeJSON(w, http.StatusOK, s.logs.Recent(limit, observabilityLevel(r.URL.Query().Get("level"))))
}

func (s *Server) adminConfig(w http.ResponseWriter, _ *http.Request) {
	data, _ := json.Marshal(s.cfg)
	var value map[string]any
	_ = json.Unmarshal(data, &value)
	redactSecrets(value)
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) adminDebugNodeReply(w http.ResponseWriter, r *http.Request) {
	request, ok := s.decodeRequest(w, r)
	if !ok {
		return
	}
	peer, ok := s.resolveAdminPeer(r.PathValue("node"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote ECS node is not discovered or configured")
		return
	}
	result := s.callPeer(r.Context(), peer, request)
	if result.Error != "" {
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	s.logger.Info("admin remote ECS debug request", "node", peer.Name, "url", peer.URL)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) resolveAdminPeer(id string) (config.Peer, bool) {
	if strings.HasPrefix(id, "network:") && s.network != nil {
		nodeID := strings.TrimPrefix(id, "network:")
		var matched *networkauth.PublicRelationship
		for _, relationship := range s.network.List() {
			if relationship.PeerNodeID != nodeID ||
				(relationship.Status != networkauth.StatusActive &&
					relationship.Status != networkauth.StatusOffline) {
				continue
			}
			copyOfRelationship := relationship
			matched = &copyOfRelationship
			for _, peer := range s.network.PeersFor(
				"all", nil, nil, "reply:execute",
			) {
				if peer.URL == relationship.PeerURL {
					return peer, true
				}
			}
		}
		if matched != nil {
			for _, peer := range s.cfg.Nested.Children {
				if peer.NodeID == nodeID ||
					normalizedURL(peer.URL) == normalizedURL(matched.PeerURL) {
					return peer, true
				}
			}
			for _, peers := range s.cfg.Groups {
				for _, peer := range peers {
					if peer.NodeID == nodeID ||
						normalizedURL(peer.URL) == normalizedURL(matched.PeerURL) {
						return peer, true
					}
				}
			}
		}
		return config.Peer{}, false
	}
	if strings.HasPrefix(id, "discovered:") {
		name := strings.TrimPrefix(id, "discovered:")
		for _, peer := range s.discovery.Peers("all") {
			if peer.Name == name {
				return peer, true
			}
		}
		for _, peer := range s.cfg.Nested.Children {
			if peer.Name == name {
				return peer, true
			}
		}
		return config.Peer{}, false
	}
	if strings.HasPrefix(id, "configured:") {
		name := strings.TrimPrefix(id, "configured:")
		for _, peer := range s.cfg.Nested.Children {
			if peer.Name == name {
				return peer, true
			}
		}
	}
	return config.Peer{}, false
}

func (s *Server) loadAdminTools() ([]adminTool, error) {
	entries, err := os.ReadDir(s.cfg.Tools.Directory)
	if err != nil {
		return nil, err
	}
	dependencies := make(map[string][]string)
	if s.skills != nil {
		for _, skill := range s.skills.All() {
			for _, dependency := range skill.Dependencies.Tools {
				dependencies[dependency.Name] = append(dependencies[dependency.Name], skill.Name)
			}
		}
	}
	result := make([]adminTool, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.cfg.Tools.Directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		var manifest tools.Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("decode %s: %w", entry.Name(), err)
		}
		enabled := manifest.Enabled == nil || *manifest.Enabled
		available := false
		reason := "disabled"
		if enabled {
			status := s.registry.Availability(manifest.Name)
			available = status.Available
			reason = status.Reason
		}
		result = append(result, adminTool{
			Manifest: manifest, Enabled: enabled, Available: available,
			Reason: reason, Dependencies: dependencies[manifest.Name],
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Manifest.Name < result[j].Manifest.Name })
	return result, nil
}

func (s *Server) updateToolFile(name string, data []byte, remove bool) error {
	if !validAdminName(name) {
		return errors.New("invalid tool name")
	}
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	path := filepath.Join(s.cfg.Tools.Directory, name+".json")
	original, readErr := os.ReadFile(path)
	existed := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if remove {
		if !existed {
			return os.ErrNotExist
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if err := writeAtomic(path, data); err != nil {
		return err
	}
	if err := s.reloadToolsAndSkills(); err != nil {
		if existed {
			_ = writeAtomic(path, original)
		} else {
			_ = os.Remove(path)
		}
		_ = s.reloadToolsAndSkills()
		return fmt.Errorf("tool change rolled back: %w", err)
	}
	return nil
}

func (s *Server) reloadToolsAndSkills() error {
	if err := s.registry.Reload(); err != nil {
		return err
	}
	if s.skills != nil {
		if err := s.skills.Reload(); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".ecs-tool-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func validAdminName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for index, character := range name {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(index > 0 && character >= '0' && character <= '9') ||
			(index > 0 && strings.ContainsRune("_.-", character)) {
			continue
		}
		return false
	}
	return true
}

func redactSecrets(value map[string]any) {
	for key, item := range value {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "api_key") ||
			strings.Contains(lower, "secret") || strings.Contains(lower, "pairing_code") {
			if text, ok := item.(string); ok && text != "" {
				value[key] = "[redacted]"
			}
			continue
		}
		if child, ok := item.(map[string]any); ok {
			redactSecrets(child)
		}
		if list, ok := item.([]any); ok {
			for _, child := range list {
				if object, ok := child.(map[string]any); ok {
					redactSecrets(object)
				}
			}
		}
	}
}

func uniqueEdges(edges []adminEdge) []adminEdge {
	seen := make(map[string]bool)
	result := make([]adminEdge, 0, len(edges))
	for _, edge := range edges {
		key := edge.Parent + "\x00" + edge.Child + "\x00" + edge.Source
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, edge)
	}
	return result
}

func normalizedURL(value string) string { return strings.TrimRight(value, "/") }

func configuredAccess(token string) string {
	if token != "" {
		return "configured-token"
	}
	return "not-configured"
}

func observabilityLevel(value string) slog.Level {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
