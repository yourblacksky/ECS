package server

import (
	"bytes"
	"context"
	"ecs/internal/office"
	"ecs/internal/protocol"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

func (s *Server) maintainOfficeAttemptLease(
	ctx context.Context,
	orderID string,
	taskID string,
	attemptID string,
) func() {
	lease := s.cfg.Office.AttemptLease.Duration()
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	interval := lease / 3
	if interval < time.Second {
		interval = time.Second
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case now := <-ticker.C:
				if _, err := s.office.RenewTaskAttempt(
					orderID, taskID, attemptID, now.UTC(),
				); err != nil {
					if !errors.Is(err, office.ErrAttemptNotActive) {
						s.logger.Error(
							"Office attempt lease renewal failed",
							"work_order", orderID, "task", taskID,
							"attempt_id", attemptID, "error", err,
						)
					}
					return
				}
			}
		}
	}()
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

func connectorTools(capabilities protocol.NodeCapabilities, required []string) (map[string][]string, []string) {
	result := make(map[string][]string)
	for _, tool := range capabilities.Tools {
		for _, capability := range tool.Capabilities {
			result[capability] = append(result[capability], tool.Name)
		}
	}
	var missing []string
	for _, capability := range required {
		if len(result[capability]) == 0 {
			missing = append(missing, capability)
		}
	}
	return result, missing
}

func toolsForConnectors(toolMap map[string][]string, connectors []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, connector := range connectors {
		for _, tool := range toolMap[connector] {
			if strings.Contains(strings.ToLower(tool), "compensat") {
				continue
			}
			if !seen[tool] {
				seen[tool] = true
				result = append(result, tool)
			}
		}
	}
	slices.Sort(result)
	return result
}

func officeWorkOrderSkills(order office.WorkOrder) []string {
	var result []string
	for _, task := range order.Tasks {
		result = appendUniqueSorted(result, task.Skills...)
	}
	return result
}

func officeNodeHasSkills(capabilities protocol.NodeCapabilities, required []string) bool {
	available := make(map[string]bool, len(capabilities.Skills))
	for _, skill := range capabilities.Skills {
		available[skill.Name] = true
	}
	for _, name := range required {
		if !available[name] {
			return false
		}
	}
	return true
}

func officeSkillTools(capabilities protocol.NodeCapabilities, required []string) []string {
	requiredSet := make(map[string]bool, len(required))
	for _, name := range required {
		requiredSet[name] = true
	}
	var result []string
	for _, skill := range capabilities.Skills {
		if requiredSet[skill.Name] {
			result = appendUniqueSorted(result, skill.Tools...)
		}
	}
	return result
}

func appendUniqueSorted(values []string, additional ...string) []string {
	seen := make(map[string]bool, len(values)+len(additional))
	result := make([]string, 0, len(values)+len(additional))
	for _, value := range append(append([]string(nil), values...), additional...) {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}

func (s *Server) localOfficeStageTools(task office.Task) []string {
	var result []string
	requiredSkills := make(map[string]bool, len(task.Skills))
	for _, name := range task.Skills {
		requiredSkills[name] = true
	}
	if s.skills != nil {
		for _, skill := range s.skills.Capabilities() {
			if requiredSkills[skill.Name] {
				result = appendUniqueSorted(result, skill.Tools...)
			}
		}
	}
	requiredConnectors := make(map[string]bool, len(task.Connectors))
	for _, capability := range task.Connectors {
		requiredConnectors[capability] = true
	}
	if s.registry != nil {
		for _, manifest := range s.registry.Available() {
			if strings.Contains(strings.ToLower(manifest.Name), "compensat") {
				continue
			}
			for _, capability := range manifest.Capabilities {
				if requiredConnectors[capability] {
					result = appendUniqueSorted(result, manifest.Name)
					break
				}
			}
		}
	}
	return result
}

func (s *Server) validateOfficeExecution(
	request officeTaskExecutionRequest,
	response protocol.ReplyResponse,
) error {
	if len(request.Skills) == 0 {
		return nil
	}
	requiredSkills := make(map[string]bool, len(request.Skills))
	for _, name := range request.Skills {
		requiredSkills[name] = true
	}
	successfulSkills := make(map[string]bool, len(request.Skills))
	executedConnectors := make(map[string]bool, len(request.Connectors))
	for _, run := range response.SkillRuns {
		if !requiredSkills[run.Name] || run.Status != "success" {
			continue
		}
		successfulSkills[run.Name] = true
		for _, toolRun := range run.ToolRuns {
			if toolRun.Error != "" {
				continue
			}
			manifest, ok := s.registry.Get(toolRun.Name)
			if !ok {
				continue
			}
			for _, capability := range manifest.Capabilities {
				executedConnectors[capability] = true
			}
		}
	}
	for _, name := range request.Skills {
		if !successfulSkills[name] {
			return fmt.Errorf("Office stage requires successful SkillRun %q", name)
		}
	}
	for _, connector := range request.Connectors {
		if !executedConnectors[connector] {
			return fmt.Errorf("Office stage connector %q was not executed through a declared skill", connector)
		}
	}
	return nil
}

func officeTaskIdempotencyKey(workOrderID, taskID string) string {
	return "office:" + workOrderID + ":" + taskID
}

func officeTaskRequiresReceipts(task office.Task) bool {
	for _, artifact := range task.Produces {
		if artifact.Required && strings.Contains(strings.ToLower(artifact.Name), "receipt") {
			return true
		}
	}
	return false
}

func (s *Server) validateOfficeReceipts(
	request officeTaskExecutionRequest,
	task office.Task,
	response protocol.ReplyResponse,
	now time.Time,
) ([]office.ExecutionReceipt, *office.ReceiptVerification, error) {
	if !officeTaskRequiresReceipts(task) {
		return nil, nil, nil
	}
	expectedPrefix := request.IdempotencyKey + ":"
	var receipts []office.ExecutionReceipt
	verifierPassed := false
	seenReceiptIDs := make(map[string]bool)
	seenIdempotencyKeys := make(map[string]bool)
	for _, skillRun := range response.SkillRuns {
		if skillRun.Status != "success" {
			continue
		}
		for _, toolRun := range skillRun.ToolRuns {
			if toolRun.Error != "" {
				continue
			}
			if toolRun.Name == "execution_receipt_verify" {
				var verification struct {
					Valid bool `json:"valid"`
				}
				if json.Unmarshal([]byte(toolRun.Output), &verification) == nil && verification.Valid {
					verifierPassed = true
				}
				continue
			}
			manifest, ok := s.registry.Get(toolRun.Name)
			if !ok {
				continue
			}
			connector := ""
			for _, capability := range manifest.Capabilities {
				if slices.Contains(task.Connectors, capability) {
					connector = capability
					break
				}
			}
			if connector == "" {
				continue
			}
			var output struct {
				Receipts []struct {
					ReceiptID         string `json:"receipt_id"`
					Action            string `json:"action"`
					Target            string `json:"target"`
					Result            string `json:"result"`
					ExternalReference string `json:"external_reference"`
					IdempotencyKey    string `json:"idempotency_key"`
					CreatedAt         string `json:"created_at"`
				} `json:"receipts"`
			}
			if json.Unmarshal([]byte(toolRun.Output), &output) != nil {
				continue
			}
			for _, value := range output.Receipts {
				createdAt, err := time.Parse(time.RFC3339Nano, value.CreatedAt)
				if err != nil {
					return nil, nil, fmt.Errorf("receipt %q has invalid created_at", value.ReceiptID)
				}
				if value.ReceiptID == "" || value.Action == "" || value.Target == "" ||
					value.Result != "created" || value.ExternalReference == "" {
					return nil, nil, fmt.Errorf("connector %q returned an incomplete receipt", connector)
				}
				if !strings.HasPrefix(value.IdempotencyKey, expectedPrefix) {
					return nil, nil, fmt.Errorf(
						"receipt %q is not bound to idempotency key %q",
						value.ReceiptID, request.IdempotencyKey,
					)
				}
				if seenReceiptIDs[value.ReceiptID] || seenIdempotencyKeys[value.IdempotencyKey] {
					return nil, nil, fmt.Errorf("duplicate receipt or idempotency key detected")
				}
				seenReceiptIDs[value.ReceiptID] = true
				seenIdempotencyKeys[value.IdempotencyKey] = true
				policyVersion := ""
				if request.Approval != nil {
					policyVersion = request.Approval.PolicyVersion
				}
				receipts = append(receipts, office.ExecutionReceipt{
					ReceiptID: value.ReceiptID, Action: value.Action, Target: value.Target,
					Result: value.Result, ExternalReference: value.ExternalReference,
					IdempotencyKey: value.IdempotencyKey, CreatedAt: createdAt.UTC(),
					Connector: connector, Tool: toolRun.Name, VerifiedAt: now.UTC(),
					ApprovalPolicyVersion: policyVersion,
				})
			}
		}
	}
	if len(receipts) == 0 {
		return nil, nil, errors.New("required connector receipt is missing")
	}
	if !verifierPassed {
		return nil, nil, errors.New("execution.receipt.verify did not confirm the receipts")
	}
	return receipts, &office.ReceiptVerification{
		Status: "verified", ExpectedPrefix: expectedPrefix,
		Count: len(receipts), VerifiedAt: now.UTC(),
	}, nil
}

func officeTaskPrompt(order office.WorkOrder, task office.Task) string {
	var upstream []string
	for _, dependency := range task.DependsOn {
		for _, candidate := range order.Tasks {
			if candidate.ID != dependency || len(candidate.Result) == 0 {
				continue
			}
			var response protocol.ReplyResponse
			if json.Unmarshal(candidate.Result, &response) != nil {
				continue
			}
			for _, run := range response.SkillRuns {
				if run.Status == "success" && json.Valid([]byte(run.Output)) {
					upstream = append(upstream, candidate.Name+" / "+run.Name+" 原始 JSON: "+run.Output)
				}
			}
			if response.Message.Content != "" {
				upstream = append(upstream, candidate.Name+" / 展示结果: "+response.Message.Content)
			}
		}
	}
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, "办公工单单步任务。只完成当前步骤，不执行后续步骤。\n组件：%s\n任务：%s\n目标：%s\n", order.Component, task.Name, task.Purpose)
	if len(task.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&buffer, "验收：%s\n", strings.Join(task.AcceptanceCriteria, "；"))
	}
	if len(task.Skills) > 0 {
		fmt.Fprintf(&buffer, "必须依次调用并成功完成这些 Skill：%s。\n", strings.Join(task.Skills, "、"))
	}
	if len(task.Connectors) > 0 {
		fmt.Fprintf(&buffer, "可用连接器：%s。需要真实动作时必须调用对应工具。\n", strings.Join(task.Connectors, "、"))
	}
	if officeTaskRequiresReceipts(task) {
		fmt.Fprintf(
			&buffer,
			"执行动作必须使用精确幂等键 %q，并调用 execution.receipt.verify 校验连接器回执。\n",
			officeTaskIdempotencyKey(order.ID, task.ID),
		)
	}
	fmt.Fprintf(&buffer, "原始输入：%s\n", strings.TrimSpace(string(order.Input)))
	if len(upstream) > 0 {
		fmt.Fprintf(&buffer, "上游结果：%s\n", strings.Join(upstream, "\n"))
	}
	buffer.WriteString("请给出本步骤的结构化、简短结果。")
	return buffer.String()
}

func officeFailureStatus(policy string) string {
	switch policy {
	case "hold":
		return "blocked"
	case "compensate":
		return "compensation_required"
	default:
		return "failed"
	}
}
