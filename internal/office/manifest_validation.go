package office

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	ecsschema "ecs/internal/schema"
)

func validateManifest(manifest Manifest, maxStages int) error {
	if manifest.SchemaVersion != ManifestSchema {
		return fmt.Errorf("schema_version must be %q", ManifestSchema)
	}
	if !validID.MatchString(manifest.Name) || strings.TrimSpace(manifest.Version) == "" ||
		strings.TrimSpace(manifest.Description) == "" {
		return errors.New("name, version, and description are required")
	}
	if len(manifest.Team) < 3 {
		return errors.New("team must contain at least three agent identities")
	}
	if len(manifest.Stages) == 0 || len(manifest.Stages) > maxStages {
		return fmt.Errorf("stages must contain 1..%d items", maxStages)
	}
	if manifest.InputSchema == nil {
		return errors.New("input_schema is required")
	}
	if err := ecsschema.ValidateDefinition(manifest.InputSchema, true); err != nil {
		return fmt.Errorf("invalid input_schema: %w", err)
	}
	if manifest.OutputSchema != nil {
		if err := ecsschema.ValidateDefinition(manifest.OutputSchema, false); err != nil {
			return fmt.Errorf("invalid output_schema: %w", err)
		}
	}
	agents := make(map[string]bool)
	for _, identity := range manifest.Team {
		if !validID.MatchString(identity.Name) || strings.TrimSpace(identity.Role) == "" ||
			strings.TrimSpace(identity.Purpose) == "" || agents[identity.Name] {
			return fmt.Errorf("invalid or duplicate agent identity %q", identity.Name)
		}
		agents[identity.Name] = true
	}
	connectors := make(map[string]bool)
	for _, connector := range manifest.Connectors {
		if !validID.MatchString(connector.Capability) || connectors[connector.Capability] {
			return fmt.Errorf("invalid or duplicate connector capability %q", connector.Capability)
		}
		connectors[connector.Capability] = true
	}
	stages := make(map[string]bool)
	for _, stage := range manifest.Stages {
		if !validID.MatchString(stage.ID) || stages[stage.ID] {
			return fmt.Errorf("invalid or duplicate stage %q", stage.ID)
		}
		if strings.TrimSpace(stage.Name) == "" || strings.TrimSpace(stage.Purpose) == "" {
			return fmt.Errorf("stage %q requires name and purpose", stage.ID)
		}
		if !agents[stage.Agent] {
			return fmt.Errorf("stage %q references unknown agent %q", stage.ID, stage.Agent)
		}
		switch normalizedFailure(stage.OnFailure) {
		case "fail", "hold", "retry", "compensate":
		default:
			return fmt.Errorf("stage %q has invalid on_failure", stage.ID)
		}
		switch stage.Approval.Mode {
		case "", "none", "policy", "human":
		default:
			return fmt.Errorf("stage %q has invalid approval mode", stage.ID)
		}
		switch stage.Approval.Risk {
		case "", "low", "medium", "high":
		default:
			return fmt.Errorf("stage %q has invalid approval risk", stage.ID)
		}
		for _, connector := range stage.Connectors {
			if !connectors[connector] {
				return fmt.Errorf("stage %q references undeclared connector %q", stage.ID, connector)
			}
		}
		stageSkills := make(map[string]bool, len(stage.Skills))
		for _, skill := range stage.Skills {
			if !validID.MatchString(skill) || stageSkills[skill] {
				return fmt.Errorf("stage %q has invalid or duplicate skill %q", stage.ID, skill)
			}
			stageSkills[skill] = true
		}
		if normalizedFailure(stage.OnFailure) == "compensate" && stage.Compensation == nil {
			return fmt.Errorf("stage %q requires compensation when on_failure is compensate", stage.ID)
		}
		if stage.Compensation != nil {
			if strings.TrimSpace(stage.Compensation.Action) == "" {
				return fmt.Errorf("stage %q compensation requires an action", stage.ID)
			}
			if stage.Compensation.Connector != "" && !connectors[stage.Compensation.Connector] {
				return fmt.Errorf("stage %q compensation references undeclared connector %q", stage.ID, stage.Compensation.Connector)
			}
		}
		artifacts := make(map[string]bool, len(stage.Produces))
		for _, artifact := range stage.Produces {
			if !validID.MatchString(artifact.Name) || artifacts[artifact.Name] {
				return fmt.Errorf("stage %q has invalid or duplicate artifact %q", stage.ID, artifact.Name)
			}
			artifacts[artifact.Name] = true
		}
		stages[stage.ID] = true
	}
	for _, stage := range manifest.Stages {
		for _, dependency := range stage.DependsOn {
			if dependency == stage.ID || !stages[dependency] {
				return fmt.Errorf("stage %q has invalid dependency %q", stage.ID, dependency)
			}
		}
	}
	_, err := topologicalStages(manifest.Stages)
	return err
}

func (r *Registry) validateExecutionContracts(manifest Manifest) error {
	if r.agents != nil {
		for _, identity := range manifest.Team {
			agent, ok := r.agents.Get(identity.Name)
			if !ok {
				return fmt.Errorf("team agent %q is not installed", identity.Name)
			}
			if agent.ExecutionMode != "llm" {
				return fmt.Errorf("team agent %q must use llm execution_mode", identity.Name)
			}
		}
	}
	for _, stage := range manifest.Stages {
		if len(stage.Skills) == 0 {
			continue
		}
		if r.skills == nil {
			return fmt.Errorf("stage %q declares skills but no skill registry is configured", stage.ID)
		}
		connectorCoverage := make(map[string]bool, len(stage.Connectors))
		var stageAgentSkills map[string]bool
		var stageAgentTools map[string]bool
		if r.agents != nil {
			agent, _ := r.agents.Get(stage.Agent)
			stageAgentSkills = stringSet(agent.AllowedSkills)
			stageAgentTools = stringSet(agent.AllowedTools)
		}
		for _, skillName := range stage.Skills {
			if stageAgentSkills != nil && !stageAgentSkills[skillName] {
				return fmt.Errorf("stage %q agent %q does not allow skill %q", stage.ID, stage.Agent, skillName)
			}
			skill, ok := r.skills.Get(skillName)
			if !ok {
				return fmt.Errorf("stage %q requires unavailable skill %q", stage.ID, skillName)
			}
			if availability := r.skills.Availability(skillName); !availability.Available {
				return fmt.Errorf("stage %q requires unavailable skill %q: %s", stage.ID, skillName, availability.Reason)
			}
			for _, dependency := range skill.Dependencies.Tools {
				if stageAgentTools != nil && !stageAgentTools[dependency.Name] {
					return fmt.Errorf("stage %q agent %q does not allow tool %q required by skill %q", stage.ID, stage.Agent, dependency.Name, skillName)
				}
				tool, ok := r.tools.Get(dependency.Name)
				if !ok {
					return fmt.Errorf("stage %q skill %q requires unavailable tool %q", stage.ID, skillName, dependency.Name)
				}
				for _, capability := range tool.Capabilities {
					for _, connector := range stage.Connectors {
						if capability == connector {
							connectorCoverage[connector] = true
						}
					}
				}
			}
		}
		for _, connector := range stage.Connectors {
			if !connectorCoverage[connector] {
				return fmt.Errorf("stage %q connector %q is not implemented by a declared skill dependency", stage.ID, connector)
			}
		}
	}
	return nil
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func topologicalStages(stages []Stage) ([]Stage, error) {
	byID := make(map[string]Stage, len(stages))
	indegree := make(map[string]int, len(stages))
	children := make(map[string][]string, len(stages))
	for _, stage := range stages {
		byID[stage.ID] = stage
		indegree[stage.ID] = len(stage.DependsOn)
		for _, dependency := range stage.DependsOn {
			children[dependency] = append(children[dependency], stage.ID)
		}
	}
	ready := make([]string, 0)
	for id, count := range indegree {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	result := make([]Stage, 0, len(stages))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		result = append(result, byID[id])
		for _, child := range children[id] {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sort.Strings(ready)
			}
		}
	}
	if len(result) != len(stages) {
		return nil, errors.New("stage dependency graph contains a cycle")
	}
	return result, nil
}

func connectorState(manifest Manifest, provided map[string]bool) ([]string, []string) {
	var required, missing []string
	for _, connector := range manifest.Connectors {
		if !connector.Required {
			continue
		}
		required = append(required, connector.Capability)
		if !provided[connector.Capability] {
			missing = append(missing, connector.Capability)
		}
	}
	sort.Strings(required)
	sort.Strings(missing)
	return required, missing
}

func buildHandoffs(stages []Stage, taskIDs map[string]string, context map[string]string) []Handoff {
	byID := make(map[string]Stage, len(stages))
	for _, stage := range stages {
		byID[stage.ID] = stage
	}
	contextKeys := make([]string, 0, len(context))
	for key := range context {
		contextKeys = append(contextKeys, key)
	}
	sort.Strings(contextKeys)
	var result []Handoff
	for _, stage := range stages {
		for _, dependency := range stage.DependsOn {
			artifacts := make([]string, 0)
			for _, artifact := range byID[dependency].Produces {
				if len(stage.Consumes) == 0 || contains(stage.Consumes, artifact.Name) {
					artifacts = append(artifacts, artifact.Name)
				}
			}
			result = append(result, Handoff{
				SchemaVersion: HandoffSchema, ID: taskIDs[dependency] + "_to_" + stage.ID,
				FromTask: taskIDs[dependency], ToTask: taskIDs[stage.ID], Artifacts: artifacts,
				ContextKeys: append([]string(nil), contextKeys...), Integrity: "pending",
			})
		}
	}
	return result
}

func normalizedFailure(value string) string {
	if strings.TrimSpace(value) == "" {
		return "fail"
	}
	return value
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneContext(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func randomID(prefix string) (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}
