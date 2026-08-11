package server

import (
	"ecs/internal/networkauth"
	"ecs/internal/protocol"
	"ecs/internal/skills"
	"errors"
	"net/http"
	"os"
	"strings"
)

func (s *Server) nodeIdentity() string {
	if s.network != nil {
		return s.network.NodeID()
	}
	identity := strings.TrimRight(s.discovery.Capabilities().URL, "/")
	if identity == "" {
		return s.cfg.NodeName
	}
	return identity
}

func pathContains(path []string, identity string) bool {
	for _, item := range path {
		if strings.TrimRight(item, "/") == strings.TrimRight(identity, "/") {
			return true
		}
	}
	return false
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	result := map[string]string{"status": "ok", "node": s.cfg.NodeName}
	if s.network != nil {
		result["node_id"] = s.network.NodeID()
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listTools(w http.ResponseWriter, r *http.Request) {
	capabilities := s.authorizedCapabilities(r)
	writeJSON(w, http.StatusOK, capabilities.Tools)
}

func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	if s.skills == nil {
		writeJSON(w, http.StatusOK, []skills.Manifest{})
		return
	}
	items := s.skills.Available()
	if relationship, ok := r.Context().Value(networkRelationshipKey{}).(networkauth.PublicRelationship); ok {
		allowed := make(map[string]bool, len(relationship.AllowedSkills))
		for _, name := range relationship.AllowedSkills {
			allowed[name] = true
		}
		filtered := items[:0]
		for _, item := range items {
			if allowed[item.Name] {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.authorizedCapabilities(r))
}

func (s *Server) authorizedCapabilities(r *http.Request) protocol.NodeCapabilities {
	capabilities := s.discovery.Capabilities()
	relationship, ok := r.Context().Value(networkRelationshipKey{}).(networkauth.PublicRelationship)
	if !ok {
		return capabilities
	}
	allowedTools := make(map[string]bool, len(relationship.AllowedTools))
	for _, name := range relationship.AllowedTools {
		allowedTools[name] = true
	}
	tools := capabilities.Tools[:0]
	for _, item := range capabilities.Tools {
		if allowedTools[item.Name] {
			tools = append(tools, item)
		}
	}
	capabilities.Tools = tools
	allowedSkills := make(map[string]bool, len(relationship.AllowedSkills))
	for _, name := range relationship.AllowedSkills {
		allowedSkills[name] = true
	}
	skillItems := capabilities.Skills[:0]
	for _, item := range capabilities.Skills {
		if allowedSkills[item.Name] {
			skillItems = append(skillItems, item)
		}
	}
	capabilities.Skills = skillItems
	statusItems := capabilities.Availability[:0]
	for _, item := range capabilities.Availability {
		if (item.Kind == "tool" && allowedTools[item.Name]) ||
			(item.Kind == "skill" && allowedSkills[item.Name]) {
			statusItems = append(statusItems, item)
		}
	}
	capabilities.Availability = statusItems
	if hash, err := networkauth.CapabilityHash(capabilities); err == nil {
		capabilities.CapabilityHash = hash
	}
	return capabilities
}

func (s *Server) nodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.discovery.Snapshot())
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	conversation, err := s.sessions.Load(requestOwner(r.Context()), r.PathValue("session"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, conversation)
}
