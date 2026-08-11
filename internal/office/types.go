package office

import (
	"encoding/json"
	"time"
)

const (
	ManifestSchema = "ecs.office/v1"
	OrderSchema    = "ecs.office.work-order/v1"
	HandoffSchema  = "ecs.handoff/v1"
)

// Manifest describes a reusable office workflow. It contains orchestration
// metadata only; Tools and Skills remain responsible for actual execution.
type Manifest struct {
	SchemaVersion string                 `json:"schema_version"`
	Name          string                 `json:"name"`
	Version       string                 `json:"version"`
	Description   string                 `json:"description"`
	Intents       []string               `json:"intents,omitempty"`
	Team          []AgentIdentity        `json:"team"`
	InputSchema   map[string]any         `json:"input_schema"`
	OutputSchema  map[string]any         `json:"output_schema,omitempty"`
	Connectors    []ConnectorRequirement `json:"connectors,omitempty"`
	Stages        []Stage                `json:"stages"`
	Governance    Governance             `json:"governance,omitempty"`
}

type AgentIdentity struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	Purpose string `json:"purpose"`
}

type ConnectorRequirement struct {
	Capability  string   `json:"capability"`
	Operations  []string `json:"operations,omitempty"`
	Required    bool     `json:"required"`
	Description string   `json:"description,omitempty"`
}

type Stage struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Agent              string         `json:"agent"`
	Purpose            string         `json:"purpose"`
	DependsOn          []string       `json:"depends_on,omitempty"`
	Skills             []string       `json:"skills,omitempty"`
	Connectors         []string       `json:"connectors,omitempty"`
	Consumes           []string       `json:"consumes,omitempty"`
	Produces           []ArtifactSpec `json:"produces,omitempty"`
	AcceptanceCriteria []string       `json:"acceptance_criteria,omitempty"`
	Approval           ApprovalPolicy `json:"approval,omitempty"`
	OnFailure          string         `json:"on_failure,omitempty"`
	Compensation       *Compensation  `json:"compensation,omitempty"`
}

type ArtifactSpec struct {
	Name      string         `json:"name"`
	MediaType string         `json:"media_type,omitempty"`
	Schema    map[string]any `json:"schema,omitempty"`
	Required  bool           `json:"required"`
}

type ApprovalPolicy struct {
	Mode           string   `json:"mode,omitempty"`
	Risk           string   `json:"risk,omitempty"`
	RequiredScopes []string `json:"required_scopes,omitempty"`
}

type ApprovalRecord struct {
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	Mode           string     `json:"mode"`
	Risk           string     `json:"risk,omitempty"`
	RequiredScopes []string   `json:"required_scopes,omitempty"`
	Skills         []string   `json:"skills,omitempty"`
	Tools          []string   `json:"tools,omitempty"`
	Connectors     []string   `json:"connectors,omitempty"`
	PolicyVersion  string     `json:"policy_version"`
	RequestedAt    time.Time  `json:"requested_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	RequestedBy    string     `json:"requested_by"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	Opinion        string     `json:"opinion,omitempty"`
}

type Compensation struct {
	Action    string `json:"action"`
	Connector string `json:"connector,omitempty"`
}

type ExecutionReceipt struct {
	ReceiptID             string    `json:"receipt_id"`
	Action                string    `json:"action"`
	Target                string    `json:"target"`
	Result                string    `json:"result"`
	ExternalReference     string    `json:"external_reference"`
	IdempotencyKey        string    `json:"idempotency_key"`
	CreatedAt             time.Time `json:"created_at"`
	Connector             string    `json:"connector"`
	Tool                  string    `json:"tool"`
	VerifiedAt            time.Time `json:"verified_at"`
	ApprovalPolicyVersion string    `json:"approval_policy_version,omitempty"`
}

type ReceiptVerification struct {
	Status         string    `json:"status"`
	ExpectedPrefix string    `json:"expected_prefix"`
	Count          int       `json:"count"`
	VerifiedAt     time.Time `json:"verified_at"`
}

type CompensationRecord struct {
	Status      string             `json:"status"`
	Action      string             `json:"action"`
	Connector   string             `json:"connector"`
	Tool        string             `json:"tool,omitempty"`
	RequestedAt time.Time          `json:"requested_at"`
	CompletedAt *time.Time         `json:"completed_at,omitempty"`
	Receipts    []ExecutionReceipt `json:"receipts,omitempty"`
	Error       string             `json:"error,omitempty"`
}

type Governance struct {
	DataClassification  string   `json:"data_classification,omitempty"`
	Retention           string   `json:"retention,omitempty"`
	Audit               bool     `json:"audit"`
	AllowedDestinations []string `json:"allowed_destinations,omitempty"`
}

type CompileRequest struct {
	Input           json.RawMessage   `json:"input"`
	Context         map[string]string `json:"context,omitempty"`
	RequestedBy     string            `json:"requested_by,omitempty"`
	TraceID         string            `json:"trace_id,omitempty"`
	OwnerPeerNodeID string            `json:"-"`
}

type WorkOrder struct {
	SchemaVersion       string            `json:"schema_version"`
	ID                  string            `json:"id"`
	Component           string            `json:"component"`
	ComponentVersion    string            `json:"component_version"`
	Status              string            `json:"status"`
	TraceID             string            `json:"trace_id,omitempty"`
	ExecutionTraceID    string            `json:"execution_trace_id,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	RequestedBy         string            `json:"requested_by,omitempty"`
	OwnerPeerNodeID     string            `json:"owner_peer_node_id,omitempty"`
	Input               json.RawMessage   `json:"input"`
	Context             map[string]string `json:"context,omitempty"`
	Tasks               []Task            `json:"tasks"`
	Handoffs            []Handoff         `json:"handoffs,omitempty"`
	RequiredConnectors  []string          `json:"required_connectors,omitempty"`
	MissingConnectors   []string          `json:"missing_connectors,omitempty"`
	Ready               bool              `json:"ready"`
	Governance          Governance        `json:"governance,omitempty"`
	Audit               []AuditEvent      `json:"audit"`
	Outbox              []OutboxEvent     `json:"outbox,omitempty"`
	AgentTeamsProjectID string            `json:"agentteams_project_id,omitempty"`
}

type WorkOrderSummary struct {
	ID                string    `json:"id"`
	Component         string    `json:"component"`
	ComponentVersion  string    `json:"component_version"`
	Status            string    `json:"status"`
	TraceID           string    `json:"trace_id,omitempty"`
	ExecutionTraceID  string    `json:"execution_trace_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	RequestedBy       string    `json:"requested_by,omitempty"`
	OwnerPeerNodeID   string    `json:"owner_peer_node_id,omitempty"`
	Ready             bool      `json:"ready"`
	MissingConnectors []string  `json:"missing_connectors,omitempty"`
	Tasks             int       `json:"tasks"`
	PendingTasks      int       `json:"pending_tasks"`
	ApprovalTasks     int       `json:"approval_tasks"`
	PendingApprovals  int       `json:"pending_approvals"`
	ActiveAttempts    int       `json:"active_attempts"`
	PendingOutbox     int       `json:"pending_outbox"`
	RecoveryTasks     int       `json:"recovery_tasks"`
	Handoffs          int       `json:"handoffs"`
}

type Task struct {
	ID                  string               `json:"id"`
	StageID             string               `json:"stage_id,omitempty"`
	Name                string               `json:"name"`
	Agent               string               `json:"agent"`
	AgentRole           string               `json:"agent_role"`
	Purpose             string               `json:"purpose"`
	Status              string               `json:"status"`
	AssignedNode        string               `json:"assigned_node,omitempty"`
	TraceID             string               `json:"trace_id,omitempty"`
	StartedAt           *time.Time           `json:"started_at,omitempty"`
	CompletedAt         *time.Time           `json:"completed_at,omitempty"`
	Result              json.RawMessage      `json:"result,omitempty"`
	Error               string               `json:"error,omitempty"`
	DependsOn           []string             `json:"depends_on,omitempty"`
	Skills              []string             `json:"skills,omitempty"`
	Connectors          []string             `json:"connectors,omitempty"`
	Consumes            []string             `json:"consumes,omitempty"`
	Produces            []ArtifactSpec       `json:"produces,omitempty"`
	AcceptanceCriteria  []string             `json:"acceptance_criteria,omitempty"`
	Approval            ApprovalPolicy       `json:"approval,omitempty"`
	ApprovalRecord      *ApprovalRecord      `json:"approval_record,omitempty"`
	Receipts            []ExecutionReceipt   `json:"receipts,omitempty"`
	ReceiptVerification *ReceiptVerification `json:"receipt_verification,omitempty"`
	OnFailure           string               `json:"on_failure,omitempty"`
	Compensation        *Compensation        `json:"compensation,omitempty"`
	CompensationRecord  *CompensationRecord  `json:"compensation_record,omitempty"`
	AgentTeamsTaskID    string               `json:"agentteams_task_id,omitempty"`
	AttemptCount        int                  `json:"attempt_count,omitempty"`
	ActiveAttemptID     string               `json:"active_attempt_id,omitempty"`
	Attempts            []TaskAttempt        `json:"attempts,omitempty"`
}

type TaskAttempt struct {
	ID             string     `json:"id"`
	Number         int        `json:"number"`
	Status         string     `json:"status"`
	Node           string     `json:"node"`
	TraceID        string     `json:"trace_id"`
	IdempotencyKey string     `json:"idempotency_key"`
	OutboxEventID  string     `json:"outbox_event_id"`
	ClaimedAt      time.Time  `json:"claimed_at"`
	DispatchedAt   *time.Time `json:"dispatched_at,omitempty"`
	LeaseExpiresAt time.Time  `json:"lease_expires_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	Error          string     `json:"error,omitempty"`
}

type OutboxEvent struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	Status         string     `json:"status"`
	WorkOrderID    string     `json:"work_order_id"`
	TaskID         string     `json:"task_id"`
	AttemptID      string     `json:"attempt_id"`
	Destination    string     `json:"destination"`
	IdempotencyKey string     `json:"idempotency_key"`
	Skills         []string   `json:"skills,omitempty"`
	Tools          []string   `json:"tools,omitempty"`
	Connectors     []string   `json:"connectors,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DispatchedAt   *time.Time `json:"dispatched_at,omitempty"`
	ConfirmedAt    *time.Time `json:"confirmed_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
}

type RecoveryRecord struct {
	WorkOrderID string `json:"work_order_id"`
	TaskID      string `json:"task_id"`
	AttemptID   string `json:"attempt_id"`
	Reason      string `json:"reason"`
}

type AgentTeamsStageMapping struct {
	WorkOrderID  string `json:"work_order_id"`
	Component    string `json:"component"`
	StageID      string `json:"stage_id"`
	OfficeTaskID string `json:"office_task_id"`
	Status       string `json:"status"`
}

type Handoff struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	FromTask      string   `json:"from_task"`
	ToTask        string   `json:"to_task"`
	Artifacts     []string `json:"artifacts,omitempty"`
	ContextKeys   []string `json:"context_keys,omitempty"`
	Integrity     string   `json:"integrity"`
}

type AuditEvent struct {
	At      time.Time      `json:"at"`
	Action  string         `json:"action"`
	Actor   string         `json:"actor,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

type Capability struct {
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
