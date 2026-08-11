package meetingdemo

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ecs/internal/config"
	"ecs/internal/office"
	"ecs/internal/protocol"
	"ecs/internal/skills"
	"ecs/internal/toolbox"
	"ecs/internal/tools"
)

//go:embed fixtures/*.json
var fixtureFiles embed.FS

type Evidence struct {
	SchemaVersion          string                      `json:"schema_version"`
	Scenario               string                      `json:"scenario"`
	Iteration              int                         `json:"iteration"`
	Status                 string                      `json:"status"`
	SourceSHA256           string                      `json:"source_sha256"`
	Agents                 []Agent                     `json:"agents"`
	Stages                 []Stage                     `json:"stages"`
	ExecutionChain         []ExecutionStep             `json:"execution_chain"`
	SkillRuns              []protocol.SkillRun         `json:"skill_runs"`
	Approval               *office.ApprovalRecord      `json:"approval,omitempty"`
	Receipts               []office.ExecutionReceipt   `json:"receipts"`
	ReceiptVerification    *office.ReceiptVerification `json:"receipt_verification,omitempty"`
	Blockers               []Blocker                   `json:"blockers"`
	ExternalActionExecuted bool                        `json:"external_action_executed"`
	FailureReason          string                      `json:"failure_reason,omitempty"`
	Timeline               []office.AuditEvent         `json:"timeline"`
	StableSignature        string                      `json:"stable_signature"`
}

type Agent struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type Stage struct {
	ID         string   `json:"id"`
	Agent      string   `json:"agent"`
	Status     string   `json:"status"`
	Skills     []string `json:"skills,omitempty"`
	Connectors []string `json:"connectors,omitempty"`
}

type ExecutionStep struct {
	Sequence  int    `json:"sequence"`
	Stage     string `json:"stage"`
	Agent     string `json:"agent"`
	Skill     string `json:"skill,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Connector string `json:"connector,omitempty"`
	Status    string `json:"status"`
}

type Blocker struct {
	Index   int      `json:"index"`
	Missing []string `json:"missing"`
}

type Runner struct {
	office      *office.Registry
	skillRunner *skills.Runner
}

func New(repositoryRoot string, orderDirectory string) (*Runner, error) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolRegistry := tools.NewRegistry(config.ToolsConfig{
		Directory:      filepath.Join(repositoryRoot, "tools.d"),
		DefaultTimeout: config.Duration(10 * time.Second),
		MaxOutputBytes: 64 << 10,
	}, logger)
	if err := toolRegistry.Reload(); err != nil {
		return nil, err
	}
	for _, manifest := range toolRegistry.All() {
		handler := toolbox.Handler(manifest.Name)
		if manifest.Command == "@builtin" && handler != nil {
			if err := toolRegistry.RegisterBuiltin(manifest.Name, handler); err != nil {
				return nil, err
			}
		}
	}
	skillRegistry := skills.NewRegistry(config.SkillsConfig{
		Directory:             filepath.Join(repositoryRoot, "skills.d"),
		KnowledgeDirectory:    filepath.Join(repositoryRoot, "skills.d", "_knowledge"),
		MaxPromptSkills:       5,
		MaxSteps:              4,
		DocumentationRequired: true,
		GroundingEnabled:      true,
		MaxDocumentChars:      12000,
		MaxGroundingChars:     1800,
		MaxKnowledgeDocuments: 3,
	}, toolRegistry, logger)
	if err := skillRegistry.Reload(); err != nil {
		return nil, err
	}
	officeRegistry := office.NewRegistry(config.OfficeConfig{
		Directory:         filepath.Join(repositoryRoot, "office.d"),
		OrderDirectory:    orderDirectory,
		MaxStages:         16,
		MaxContextEntries: 64,
		ApprovalTTL:       config.Duration(15 * time.Minute),
		AttemptLease:      config.Duration(2 * time.Minute),
	}, toolRegistry, logger)
	officeRegistry.SetSkillRegistry(skillRegistry)
	if err := officeRegistry.Reload(); err != nil {
		return nil, err
	}
	return &Runner{
		office:      officeRegistry,
		skillRunner: skills.NewRunner(skillRegistry, toolRegistry),
	}, nil
}

func (r *Runner) Run(ctx context.Context, scenario string, iteration int) (Evidence, error) {
	if scenario != "success" && scenario != "failure" {
		return Evidence{}, errors.New("scenario must be success or failure")
	}
	fixture, err := fixtureFiles.ReadFile("fixtures/" + scenario + ".json")
	if err != nil {
		return Evidence{}, err
	}
	var input struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(fixture, &input); err != nil {
		return Evidence{}, err
	}
	tracePrefix := fmt.Sprintf("demo-%s-%d", scenario, iteration)
	order, err := r.office.Compile("meeting_followup", office.CompileRequest{
		Input: fixture, RequestedBy: "ecs-meeting-demo", TraceID: tracePrefix,
	})
	if err != nil {
		return Evidence{}, err
	}
	if err := r.completeStage(order.ID, "scope", tracePrefix, json.RawMessage(`{"scope":"meeting_followup"}`)); err != nil {
		return Evidence{}, err
	}
	extractInput, _ := json.Marshal(map[string]string{"source": input.Source})
	extractRun, err := r.skillRunner.Execute(ctx, "meeting.fact.extract", extractInput)
	if err != nil {
		return Evidence{}, err
	}
	if err := r.completeStage(order.ID, "extract", tracePrefix, json.RawMessage(extractRun.Output)); err != nil {
		return Evidence{}, err
	}
	var extracted struct {
		SourceSHA256 string          `json:"source_sha256"`
		ActionItems  json.RawMessage `json:"action_items"`
	}
	if err := json.Unmarshal([]byte(extractRun.Output), &extracted); err != nil {
		return Evidence{}, err
	}
	verifyInput := json.RawMessage(`{"action_items":` + string(extracted.ActionItems) + `}`)
	verifyRun, err := r.skillRunner.Execute(ctx, "meeting.evidence.verify", verifyInput)
	if err != nil {
		return Evidence{}, err
	}
	var verification struct {
		Valid    bool      `json:"valid"`
		Blockers []Blocker `json:"blockers"`
	}
	if err := json.Unmarshal([]byte(verifyRun.Output), &verification); err != nil {
		return Evidence{}, err
	}
	if !verification.Valid {
		reason := "责任人、截止时间或原文证据不完整"
		if err := r.failStage(
			order.ID, "verify", tracePrefix, json.RawMessage(verifyRun.Output), errors.New(reason),
		); err != nil {
			return Evidence{}, err
		}
		return r.evidence(
			order.ID, scenario, iteration, extracted.SourceSHA256,
			[]protocol.SkillRun{extractRun, verifyRun}, verification.Blockers, reason,
		)
	}
	if err := r.completeStage(order.ID, "verify", tracePrefix, json.RawMessage(verifyRun.Output)); err != nil {
		return Evidence{}, err
	}

	dispatchSkills := []string{"execution.receipt.verify", "office.action.dispatch"}
	dispatchTools := []string{"execution_receipt_verify", "office_action_dispatch"}
	dispatchConnectors := []string{"office.task.write"}
	now := time.Now().UTC()
	_, approval, approved, err := r.office.EnsureTaskApproval(
		order.ID, "dispatch", "ecs-meeting-demo",
		dispatchSkills, dispatchTools, dispatchConnectors, now,
	)
	if err != nil || approved {
		return Evidence{}, fmt.Errorf("create approval: approved=%v: %w", approved, err)
	}
	if _, err := r.office.DecideTaskApproval(
		order.ID, "dispatch", approval.ID, "approve", "demo-reviewer",
		"固定成功路径批准", now.Add(time.Second),
	); err != nil {
		return Evidence{}, err
	}
	if _, err := r.office.StartAgentTeamsStage(
		order.ID, "dispatch", tracePrefix+"-dispatch", tracePrefix,
		tracePrefix+"-dispatch", "",
	); err != nil {
		return Evidence{}, err
	}
	idempotencyKey := "office:" + order.ID + ":" + order.ID + ".dispatch"
	dispatchInput := json.RawMessage(
		`{"action_items":` + string(extracted.ActionItems) +
			`,"idempotency_key":` + quoted(idempotencyKey) + `}`,
	)
	dispatchRun, err := r.skillRunner.Execute(ctx, "office.action.dispatch", dispatchInput)
	if err != nil {
		return Evidence{}, err
	}
	var dispatched struct {
		Receipts []rawReceipt `json:"receipts"`
	}
	if err := json.Unmarshal([]byte(dispatchRun.Output), &dispatched); err != nil {
		return Evidence{}, err
	}
	rawReceipts, _ := json.Marshal(dispatched.Receipts)
	receiptRun, err := r.skillRunner.Execute(
		ctx,
		"execution.receipt.verify",
		json.RawMessage(`{"receipts":`+string(rawReceipts)+`}`),
	)
	if err != nil {
		return Evidence{}, err
	}
	var receiptCheck struct {
		Valid bool `json:"valid"`
		Count int  `json:"count"`
	}
	if err := json.Unmarshal([]byte(receiptRun.Output), &receiptCheck); err != nil {
		return Evidence{}, err
	}
	if !receiptCheck.Valid || receiptCheck.Count != len(dispatched.Receipts) {
		return Evidence{}, errors.New("receipt verification failed")
	}
	receipts, err := bindReceipts(dispatched.Receipts, approval.PolicyVersion, now.Add(2*time.Second))
	if err != nil {
		return Evidence{}, err
	}
	if _, err := r.office.SetTaskReceipts(
		order.ID,
		"dispatch",
		"",
		receipts,
		&office.ReceiptVerification{
			Status: "verified", ExpectedPrefix: idempotencyKey + ":",
			Count: len(receipts), VerifiedAt: now.Add(2 * time.Second),
		},
	); err != nil {
		return Evidence{}, err
	}
	result, _ := json.Marshal(map[string]any{
		"dispatch": dispatchRun.Output, "receipt_verification": json.RawMessage(receiptRun.Output),
	})
	if _, err := r.office.FinishAgentTeamsStage(
		order.ID, "dispatch", tracePrefix+"-dispatch", "", result, nil,
	); err != nil {
		return Evidence{}, err
	}
	return r.evidence(
		order.ID, scenario, iteration, extracted.SourceSHA256,
		[]protocol.SkillRun{extractRun, verifyRun, dispatchRun, receiptRun}, nil, "",
	)
}

func (r *Runner) completeStage(orderID, stageID, tracePrefix string, result json.RawMessage) error {
	taskID := tracePrefix + "-" + stageID
	if _, err := r.office.StartAgentTeamsStage(
		orderID, stageID, taskID, tracePrefix, tracePrefix+"-"+stageID, "",
	); err != nil {
		return err
	}
	_, err := r.office.FinishAgentTeamsStage(orderID, stageID, taskID, "", result, nil)
	return err
}

func (r *Runner) failStage(
	orderID, stageID, tracePrefix string,
	result json.RawMessage,
	taskError error,
) error {
	taskID := tracePrefix + "-" + stageID
	if _, err := r.office.StartAgentTeamsStage(
		orderID, stageID, taskID, tracePrefix, tracePrefix+"-"+stageID, "",
	); err != nil {
		return err
	}
	_, err := r.office.FinishAgentTeamsStage(orderID, stageID, taskID, "", result, taskError)
	return err
}

func (r *Runner) evidence(
	orderID, scenario string,
	iteration int,
	sourceHash string,
	skillRuns []protocol.SkillRun,
	blockers []Blocker,
	failureReason string,
) (Evidence, error) {
	order, err := r.office.LoadOrder(orderID)
	if err != nil {
		return Evidence{}, err
	}
	manifest, _ := r.office.Get("meeting_followup")
	agents := make([]Agent, 0, len(manifest.Team))
	for _, member := range manifest.Team {
		agents = append(agents, Agent{Name: member.Name, Role: member.Role})
	}
	stages := make([]Stage, 0, len(order.Tasks))
	var approval *office.ApprovalRecord
	var receipts []office.ExecutionReceipt
	var receiptVerification *office.ReceiptVerification
	externalAction := false
	stageAgents := make(map[string]string, len(order.Tasks))
	for _, task := range order.Tasks {
		stageAgents[task.StageID] = task.Agent
		stages = append(stages, Stage{
			ID: task.StageID, Agent: task.Agent, Status: task.Status,
			Skills:     append([]string(nil), task.Skills...),
			Connectors: append([]string(nil), task.Connectors...),
		})
		if task.StageID == "dispatch" {
			approval = task.ApprovalRecord
			receipts = append([]office.ExecutionReceipt(nil), task.Receipts...)
			receiptVerification = task.ReceiptVerification
			externalAction = len(receipts) > 0
		}
	}
	if blockers == nil {
		blockers = []Blocker{}
	}
	if receipts == nil {
		receipts = []office.ExecutionReceipt{}
	}
	executionChain := buildExecutionChain(stageAgents, skillRuns)
	evidence := Evidence{
		SchemaVersion:          "ecs.meeting-demo/v1",
		Scenario:               scenario,
		Iteration:              iteration,
		Status:                 order.Status,
		SourceSHA256:           sourceHash,
		Agents:                 agents,
		Stages:                 stages,
		ExecutionChain:         executionChain,
		SkillRuns:              skillRuns,
		Approval:               approval,
		Receipts:               receipts,
		ReceiptVerification:    receiptVerification,
		Blockers:               blockers,
		ExternalActionExecuted: externalAction,
		FailureReason:          failureReason,
		Timeline:               order.Audit,
	}
	evidence.StableSignature = stableSignature(evidence)
	return evidence, nil
}

func buildExecutionChain(
	stageAgents map[string]string,
	skillRuns []protocol.SkillRun,
) []ExecutionStep {
	result := []ExecutionStep{{
		Sequence: 1, Stage: "scope", Agent: stageAgents["scope"], Status: "completed",
	}}
	for _, run := range skillRuns {
		stage := "dispatch"
		switch run.Name {
		case "meeting.fact.extract":
			stage = "extract"
		case "meeting.evidence.verify":
			stage = "verify"
		}
		if len(run.ToolRuns) == 0 {
			result = append(result, ExecutionStep{
				Sequence: len(result) + 1, Stage: stage, Agent: stageAgents[stage],
				Skill: run.Name, Status: run.Status,
			})
			continue
		}
		for _, toolRun := range run.ToolRuns {
			connector := ""
			switch toolRun.Name {
			case "meeting_fact_extract":
				connector = "office.document.read"
			case "office_action_dispatch":
				connector = "office.task.write"
			}
			status := "success"
			if toolRun.Error != "" {
				status = "failed"
			}
			result = append(result, ExecutionStep{
				Sequence: len(result) + 1, Stage: stage, Agent: stageAgents[stage],
				Skill: run.Name, Tool: toolRun.Name, Connector: connector, Status: status,
			})
		}
	}
	return result
}

func stableSignature(evidence Evidence) string {
	parts := []string{evidence.Scenario, evidence.Status, fmt.Sprint(evidence.ExternalActionExecuted)}
	for _, agent := range evidence.Agents {
		parts = append(parts, agent.Name+":"+agent.Role)
	}
	for _, stage := range evidence.Stages {
		parts = append(parts, stage.ID+":"+stage.Agent+":"+stage.Status)
	}
	for _, run := range evidence.SkillRuns {
		parts = append(parts, run.Name+":"+run.Status)
		for _, toolRun := range run.ToolRuns {
			parts = append(parts, toolRun.Name+":"+fmt.Sprint(toolRun.Error == ""))
		}
	}
	for _, blocker := range evidence.Blockers {
		missing := append([]string(nil), blocker.Missing...)
		sort.Strings(missing)
		parts = append(parts, fmt.Sprintf("%d:%s", blocker.Index, strings.Join(missing, ",")))
	}
	return strings.Join(parts, "|")
}

type rawReceipt struct {
	ReceiptID         string `json:"receipt_id"`
	Action            string `json:"action"`
	Target            string `json:"target"`
	Result            string `json:"result"`
	ExternalReference string `json:"external_reference"`
	IdempotencyKey    string `json:"idempotency_key"`
	CreatedAt         string `json:"created_at"`
}

func bindReceipts(
	values []rawReceipt,
	policyVersion string,
	verifiedAt time.Time,
) ([]office.ExecutionReceipt, error) {
	result := make([]office.ExecutionReceipt, 0, len(values))
	for _, value := range values {
		createdAt, err := time.Parse(time.RFC3339Nano, value.CreatedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, office.ExecutionReceipt{
			ReceiptID: value.ReceiptID, Action: value.Action, Target: value.Target,
			Result: value.Result, ExternalReference: value.ExternalReference,
			IdempotencyKey: value.IdempotencyKey, CreatedAt: createdAt,
			Connector: "office.task.write", Tool: "office_action_dispatch",
			VerifiedAt: verifiedAt, ApprovalPolicyVersion: policyVersion,
		})
	}
	return result, nil
}

func quoted(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
