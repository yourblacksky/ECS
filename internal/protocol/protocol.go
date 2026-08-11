package protocol

import (
	"encoding/json"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

type ReplyRequest struct {
	RequestID string    `json:"request_id,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Prompt    string    `json:"prompt,omitempty"`
	Messages  []Message `json:"messages,omitempty"`
	Model     string    `json:"model,omitempty"`
	Pipeline  string    `json:"pipeline,omitempty"`
	// Agent pins routing to one installed agent principal instead of letting
	// the pipeline select an executor. It is honored only when the agent is
	// registered and its allowlist covers the requested capabilities; unknown
	// or under-privileged agents fail closed.
	Agent   string   `json:"agent,omitempty"`
	Skills  []string `json:"skills,omitempty"`
	Tools   []string `json:"tools,omitempty"`
	// RequiredSkills and RequiredTools are trusted, process-local routing
	// constraints. They are intentionally excluded from the wire protocol so a
	// remote caller cannot promote an allowed capability into a mandatory one.
	RequiredSkills []string          `json:"-"`
	RequiredTools  []string          `json:"-"`
	ApprovedTools  []string          `json:"approved_tools,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	// ExpectedText runs a real-model probe without a capability catalog and is
	// verified byte-for-byte against the returned text.
	ExpectedText   string          `json:"expected_text,omitempty"`
	DirectTool     *ToolInvocation `json:"direct_tool,omitempty"`
	DispatchMode   string          `json:"dispatch_mode,omitempty"`
	DispatchFanout int             `json:"dispatch_fanout,omitempty"`
	RestrictTools  bool            `json:"restrict_tools,omitempty"`
	RestrictSkills bool            `json:"restrict_skills,omitempty"`
}

// ToolInvocation carries a fully planned tool call. Transports without a local
// model, such as browser leaves, can execute this deterministic form directly.
type ToolInvocation struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ReplyResponse struct {
	RequestID    string        `json:"request_id"`
	TraceID      string        `json:"trace_id,omitempty"`
	SessionID    string        `json:"session_id,omitempty"`
	Node         string        `json:"node"`
	Message      Message       `json:"message"`
	ToolRuns     []ToolRun     `json:"tool_runs,omitempty"`
	SkillRuns    []SkillRun    `json:"skill_runs,omitempty"`
	InputRequest *InputRequest `json:"input_request,omitempty"`
	Verification *Verification `json:"verification,omitempty"`
}

// InputRequest pauses execution when a capability still needs user-provided
// slots. PartialArguments is model-facing and contains no injected secrets.
type InputRequest struct {
	Capability       string          `json:"capability"`
	MissingFields    []string        `json:"missing_fields"`
	PartialArguments json.RawMessage `json:"partial_arguments,omitempty"`
}

type Verification struct {
	Verifier         string   `json:"verifier,omitempty"`
	Status           string   `json:"status"`
	Validation       string   `json:"validation_result"`
	Evidence         []string `json:"evidence,omitempty"`
	Errors           []string `json:"errors,omitempty"`
	RetryRecommended bool     `json:"retry_recommended,omitempty"`
}

type ToolRun struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Output    string          `json:"output,omitempty"`
	Error     string          `json:"error,omitempty"`
	Duration  int64           `json:"duration_ms"`
}

type SkillRun struct {
	Name     string          `json:"name"`
	Version  string          `json:"version"`
	Input    json.RawMessage `json:"input"`
	Output   string          `json:"output,omitempty"`
	Status   string          `json:"status"`
	Error    string          `json:"error,omitempty"`
	Duration int64           `json:"duration_ms"`
	ToolRuns []ToolRun       `json:"tool_runs,omitempty"`
}

type SkillCapability struct {
	Name                 string         `json:"name"`
	Version              string         `json:"version"`
	Description          string         `json:"description"`
	UseWhen              string         `json:"use_when"`
	AvoidWhen            string         `json:"avoid_when,omitempty"`
	InputSchema          map[string]any `json:"input_schema"`
	CanonicalInputSchema map[string]any `json:"canonical_input_schema,omitempty"`
	OutputSchema         map[string]any `json:"output_schema,omitempty"`
	Risk                 string         `json:"risk"`
	Tools                []string       `json:"tools"`
	AgentRoles           []string       `json:"agent_roles,omitempty"`
}

type AgentCapability struct {
	Name          string   `json:"name"`
	Purpose       string   `json:"purpose"`
	ExecutionMode string   `json:"execution_mode"`
	Model         string   `json:"model,omitempty"`
	AllowedSkills []string `json:"allowed_skills,omitempty"`
	AllowedTools  []string `json:"allowed_tools,omitempty"`
}

type PipelineCapability struct {
	Name      string   `json:"name"`
	Strategy  string   `json:"strategy"`
	Planner   string   `json:"planner,omitempty"`
	Executors []string `json:"executors"`
	Fallback  string   `json:"fallback_executor,omitempty"`
	Verifier  string   `json:"verifier,omitempty"`
	Default   bool     `json:"default,omitempty"`
}

type GroupResponse struct {
	RequestID    string        `json:"request_id"`
	TraceID      string        `json:"trace_id,omitempty"`
	Group        string        `json:"group"`
	DispatchMode string        `json:"dispatch_mode"`
	Candidates   []string      `json:"candidates,omitempty"`
	Selected     []string      `json:"selected,omitempty"`
	Winner       string        `json:"winner,omitempty"`
	Results      []PeerResult  `json:"results"`
	Summary      *Message      `json:"summary,omitempty"`
	Verification *Verification `json:"verification,omitempty"`
}

type PeerResult struct {
	Peer     string         `json:"peer"`
	Response *ReplyResponse `json:"response,omitempty"`
	Error    string         `json:"error,omitempty"`
}

type ToolCapability struct {
	Name                 string         `json:"name"`
	Version              string         `json:"version"`
	Description          string         `json:"description"`
	Capabilities         []string       `json:"capabilities,omitempty"`
	InputSchema          map[string]any `json:"input_schema"`
	CanonicalInputSchema map[string]any `json:"canonical_input_schema,omitempty"`
	OutputSchema         map[string]any `json:"output_schema,omitempty"`
	Risk                 string         `json:"risk"`
	RequiresApproval     bool           `json:"requires_approval"`
}

type CapabilityAvailability struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type OfficeComponentCapability struct {
	Name               string         `json:"name"`
	Version            string         `json:"version"`
	Description        string         `json:"description"`
	Intents            []string       `json:"intents,omitempty"`
	Agents             []string       `json:"agents"`
	Stages             int            `json:"stages"`
	InputSchema        map[string]any `json:"input_schema"`
	RequiredConnectors []string       `json:"required_connectors,omitempty"`
	MissingConnectors  []string       `json:"missing_connectors,omitempty"`
	Ready              bool           `json:"ready"`
}

type NodeCapabilities struct {
	Protocol         string                      `json:"protocol"`
	NodeID           string                      `json:"node_id,omitempty"`
	Node             string                      `json:"node"`
	URL              string                      `json:"url,omitempty"`
	Model            string                      `json:"model"`
	Platform         PlatformCapabilities        `json:"platform,omitempty"`
	Groups           []string                    `json:"groups,omitempty"`
	Features         []string                    `json:"features,omitempty"`
	Children         []NodeRef                   `json:"children,omitempty"`
	Tools            []ToolCapability            `json:"tools"`
	Skills           []SkillCapability           `json:"skills,omitempty"`
	Agents           []AgentCapability           `json:"agents,omitempty"`
	Pipelines        []PipelineCapability        `json:"pipelines,omitempty"`
	OfficeComponents []OfficeComponentCapability `json:"office_components,omitempty"`
	Availability     []CapabilityAvailability    `json:"availability,omitempty"`
	CapabilityHash   string                      `json:"capability_hash,omitempty"`
	LastSeen         *time.Time                  `json:"last_seen,omitempty"`
}

// PlatformCapabilities lets routing distinguish a long-running server from a
// foreground-only or client-only node without changing the ECS wire protocol.
type PlatformCapabilities struct {
	Name               string `json:"name,omitempty"`
	RuntimeMode        string `json:"runtime_mode,omitempty"`
	TopologyRole       string `json:"topology_role,omitempty"`
	AcceptsChildren    bool   `json:"accepts_children"`
	SupportsInbound    bool   `json:"supports_inbound"`
	SupportsBackground bool   `json:"supports_background"`
	SupportsSubprocess bool   `json:"supports_subprocess"`
	SupportsMulticast  bool   `json:"supports_multicast"`
	SupportsPushWakeup bool   `json:"supports_push_wakeup"`
}

type NodeRef struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type RecursiveReplyRequest struct {
	Reply    ReplyRequest `json:"reply"`
	MaxDepth *int         `json:"max_depth,omitempty"`
	Depth    int          `json:"depth,omitempty"`
	Path     []string     `json:"path,omitempty"`
}

type RecursiveReplyResponse struct {
	RequestID    string                   `json:"request_id"`
	TraceID      string                   `json:"trace_id,omitempty"`
	Node         string                   `json:"node"`
	URL          string                   `json:"url,omitempty"`
	Depth        int                      `json:"depth"`
	Reply        *ReplyResponse           `json:"reply,omitempty"`
	Summary      *Message                 `json:"summary,omitempty"`
	Verification *Verification            `json:"verification,omitempty"`
	Approval     *ApprovalRequired        `json:"approval,omitempty"`
	Children     []RecursiveReplyResponse `json:"children,omitempty"`
	Error        string                   `json:"error,omitempty"`
}

type ApprovalRequired struct {
	Error       string          `json:"error"`
	RequestID   string          `json:"request_id"`
	TraceID     string          `json:"trace_id,omitempty"`
	Tool        string          `json:"tool"`
	Description string          `json:"description"`
	Risk        string          `json:"risk"`
	Arguments   json.RawMessage `json:"arguments"`
}
