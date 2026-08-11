package skills

import (
	"encoding/json"

	"ecs/internal/config"
	ecsschema "ecs/internal/schema"
)

type Manifest struct {
	SchemaVersion   string             `json:"schema_version"`
	Name            string             `json:"name"`
	Version         string             `json:"version"`
	Description     string             `json:"description"`
	Platforms       []string           `json:"platforms,omitempty"`
	When            Conditions         `json:"when"`
	NeverWhen       []string           `json:"never_when,omitempty"`
	NeverKeywords   []string           `json:"never_keywords,omitempty"`
	Agents          AgentCompatibility `json:"agents,omitempty"`
	InputSchema     map[string]any     `json:"input_schema"`
	CanonicalSchema map[string]any     `json:"canonical_input_schema,omitempty"`
	InputAdapter    ecsschema.Adapter  `json:"input_adapter,omitempty"`
	OutputSchema    map[string]any     `json:"output_schema,omitempty"`
	Dependencies    Dependencies       `json:"dependencies"`
	Workflow        []Step             `json:"workflow"`
	Failure         FailurePolicy      `json:"failure,omitempty"`
	Risk            RiskPolicy         `json:"risk,omitempty"`
	Compatibility   Compatibility      `json:"compatibility,omitempty"`
	Reuse           Reuse              `json:"reuse,omitempty"`
	Compensation    Compensation       `json:"compensation,omitempty"`
	PromptCard      PromptCard         `json:"prompt_card"`
	Documentation   Documentation      `json:"documentation,omitempty"`
	Enabled         *bool              `json:"enabled,omitempty"`
}

func (m Manifest) RuntimeInputSchema() map[string]any {
	if m.CanonicalSchema != nil {
		return m.CanonicalSchema
	}
	return m.InputSchema
}

type Documentation struct {
	Source      string `json:"source,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Content     string `json:"content,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

type GroundingReference struct {
	Kind        string `json:"kind"`
	Skill       string `json:"skill,omitempty"`
	Document    string `json:"document,omitempty"`
	Source      string `json:"source"`
	ContentHash string `json:"content_hash"`
	Score       int    `json:"score"`
	Explicit    bool   `json:"explicit,omitempty"`
}

type KnowledgeDocument struct {
	Name        string
	Title       string
	Summary     string
	Keywords    []string
	Intents     []string
	Source      string
	Content     string
	ContentHash string
}

type Conditions struct {
	Intents  []string `json:"intents,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
}

type AgentCompatibility struct {
	Names        []string `json:"names,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	ExcludeRoles []string `json:"exclude_roles,omitempty"`
}

type Dependencies struct {
	Tools        []ToolDependency `json:"tools"`
	Capabilities []string         `json:"capabilities,omitempty"`
}

type ToolDependency struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type Step struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Input     string `json:"input,omitempty"`
	OnSuccess string `json:"on_success,omitempty"`
	OnFailure string `json:"on_failure,omitempty"`
}

type FailurePolicy struct {
	MaxAttempts int             `json:"max_attempts,omitempty"`
	Timeout     config.Duration `json:"timeout,omitempty"`
	Fallback    string          `json:"fallback,omitempty"`
}

type RiskPolicy struct {
	Level      string   `json:"level,omitempty"`
	Boundaries []string `json:"boundaries,omitempty"`
	Approval   string   `json:"approval,omitempty"`
}

type Compatibility struct {
	ECS          string `json:"ecs,omitempty"`
	ToolProtocol string `json:"tool_protocol,omitempty"`
	MinModel     string `json:"min_model,omitempty"`
}

type Reuse struct {
	Value string   `json:"value,omitempty"`
	Tags  []string `json:"tags,omitempty"`
}

type Compensation struct {
	Required bool   `json:"required,omitempty"`
	Steps    []Step `json:"steps,omitempty"`
}

type PromptCard struct {
	Use   string `json:"use"`
	Avoid string `json:"avoid,omitempty"`
}

type SelectionContext struct {
	AgentName string
	AgentRole string
	Prompt    string
}

type Execution struct {
	Manifest Manifest
	Input    json.RawMessage
	Output   string
}
