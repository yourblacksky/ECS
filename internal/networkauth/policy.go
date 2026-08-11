package networkauth

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	defaultScopes = []string{"capabilities:read", "heartbeat:write", "reply:execute"}
	allowedScopes = []string{
		"capabilities:read",
		"agentteams:execute",
		"agentteams:read",
		"group:execute",
		"heartbeat:write",
		"recursive:execute",
		"reply:execute",
		"session:read",
		"topology:read",
		"trace:read",
	}
)

// Relationship policy is derived from declared peer capabilities. The node,
// not the model, remains responsible for granting and narrowing permissions.
func normalizePolicy(item Relationship, policy Approval, defaults bool) (Approval, error) {
	declaredTools := declaredCapabilityNames(item.Capabilities, "tool")
	defaultTools := make([]string, 0, len(item.Capabilities.Tools))
	for _, capability := range item.Capabilities.Tools {
		if capability.Risk != "high" && !capability.RequiresApproval {
			defaultTools = append(defaultTools, capability.Name)
		}
	}
	declaredSkills := declaredCapabilityNames(item.Capabilities, "skill")
	defaultSkills := make([]string, 0, len(item.Capabilities.Skills))
	for _, capability := range item.Capabilities.Skills {
		if capability.Risk != "high" {
			defaultSkills = append(defaultSkills, capability.Name)
		}
	}
	if defaults {
		if policy.Scopes == nil {
			policy.Scopes = append([]string(nil), defaultScopes...)
		}
		if policy.AllowedTools == nil {
			policy.AllowedTools = defaultTools
		}
		if policy.AllowedSkills == nil {
			policy.AllowedSkills = defaultSkills
		}
	}
	if policy.AllowUpstreamExecute == nil {
		policy.AllowUpstreamExecute = boolPointer(
			defaults || allowsUpstreamExecute(item),
		)
	}
	policy.Groups = uniqueStrings(policy.Groups)
	policy.AllowedTools = uniqueStrings(policy.AllowedTools)
	policy.AllowedSkills = uniqueStrings(policy.AllowedSkills)
	policy.Scopes = uniqueStrings(policy.Scopes)
	if unknown := outsideSet(policy.AllowedTools, declaredTools); len(unknown) > 0 {
		return Approval{}, fmt.Errorf("allowed_tools contains undeclared capabilities: %s", strings.Join(unknown, ", "))
	}
	if unknown := outsideSet(policy.AllowedSkills, declaredSkills); len(unknown) > 0 {
		return Approval{}, fmt.Errorf("allowed_skills contains undeclared capabilities: %s", strings.Join(unknown, ", "))
	}
	if unknown := outsideSet(policy.Scopes, allowedScopes); len(unknown) > 0 {
		return Approval{}, fmt.Errorf("scopes contains unsupported values: %s", strings.Join(unknown, ", "))
	}
	if !slices.Contains(policy.Scopes, "heartbeat:write") {
		return Approval{}, errors.New("heartbeat:write scope is required for an active relationship")
	}
	return policy, nil
}

func allowsUpstreamExecute(item Relationship) bool {
	return item.AllowUpstreamExecute == nil || *item.AllowUpstreamExecute
}

func boolPointer(value bool) *bool {
	return &value
}

func isExecuteScope(scope string) bool {
	switch scope {
	case "reply:execute", "group:execute", "recursive:execute", "agentteams:execute":
		return true
	default:
		return false
	}
}

func policyNarrows(item Relationship, policy Approval) bool {
	return hasRemovedValues(item.AllowedTools, policy.AllowedTools) ||
		hasRemovedValues(item.AllowedSkills, policy.AllowedSkills) ||
		hasRemovedValues(item.Scopes, policy.Scopes) ||
		(allowsUpstreamExecute(item) && !*policy.AllowUpstreamExecute)
}

func hasRemovedValues(before, after []string) bool {
	for _, value := range before {
		if !slices.Contains(after, value) {
			return true
		}
	}
	return false
}

func uniqueStrings(items []string) []string {
	result := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	slices.Sort(result)
	return result
}

func outsideSet(items, allowed []string) []string {
	var result []string
	for _, item := range uniqueStrings(items) {
		if !slices.Contains(allowed, item) {
			result = append(result, item)
		}
	}
	return result
}

func keepAllowed(items, available []string) []string {
	var result []string
	for _, item := range items {
		if slices.Contains(available, item) {
			result = append(result, item)
		}
	}
	return result
}
