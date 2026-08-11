package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"ecs/internal/protocol"
	"ecs/internal/roles"
	ecsschema "ecs/internal/schema"
	"ecs/internal/skills"
	"ecs/internal/tools"
	"ecs/internal/trace"
)

// tryDirectInvocation bypasses the model when an explicitly named capability
// has a single required string input and the user supplies it as `name: value`.
// The Manifest schema, policy, runner and verifier remain authoritative.
func (a *Agent) tryDirectInvocation(
	ctx context.Context,
	recorder *trace.Recorder,
	parentID string,
	plan roles.Plan,
	request protocol.ReplyRequest,
	available []tools.Manifest,
	allowedSkills map[string]skills.Manifest,
) (protocol.ReplyResponse, bool, error) {
	name, manifest, isSkill, ok := directInvocationTarget(plan, available, allowedSkills)
	if !ok {
		return protocol.ReplyResponse{}, false, nil
	}
	arguments, ok := bindManifestInput(routingSelectionPrompt(request), name, manifest.InputSchema)
	if !ok {
		return protocol.ReplyResponse{}, false, nil
	}
	response, err := a.executeResolvedCapability(
		ctx, recorder, parentID, plan, request, name, manifest, isSkill, arguments, "direct",
	)
	return response, true, err
}

type capabilityDecision struct {
	Execute   bool            `json:"execute"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Reply     string          `json:"reply,omitempty"`
}

// tryConstrainedCapabilitySequence keeps mandatory multi-Skill workflows
// small-model friendly. The model fills one compact argument object at a time;
// ECS fixes the order, validates every Schema, executes each workflow, and
// verifies the complete required-call set at the end.
func (a *Agent) tryConstrainedCapabilitySequence(
	ctx context.Context,
	recorder *trace.Recorder,
	parentID string,
	plan roles.Plan,
	request protocol.ReplyRequest,
	available []tools.Manifest,
	allowedSkills map[string]skills.Manifest,
) (protocol.ReplyResponse, bool, error) {
	targets, ok := constrainedSkillSequenceTargets(plan, available, allowedSkills)
	if !ok {
		return protocol.ReplyResponse{}, false, nil
	}
	response := protocol.ReplyResponse{}
	basePrompt := routingSelectionPrompt(request)
	var priorOutputs []string
	var priorSkillOutputs []string
	for _, target := range targets {
		stepPlan := planWithRequiredSkill(plan, target)
		stepRequest := request
		stepRequest.Prompt = basePrompt + "\n当前必须执行的单一步骤：" + target + "。"
		if len(priorOutputs) > 0 {
			stepRequest.Prompt += "\n已完成前序 Skill 的原始 JSON 结果（仅作为当前参数依据）：\n" +
				strings.Join(priorOutputs, "\n")
		}
		manifest := toolsManifestForName(available, target)
		arguments, bound := typedHandoffArguments(priorSkillOutputs, manifest.InputSchema)
		var step protocol.ReplyResponse
		var handled bool
		var err error
		if bound {
			step, err = a.executeResolvedCapability(
				ctx, recorder, parentID, stepPlan, stepRequest,
				target, manifest, true, arguments, "typed_handoff",
			)
			handled = true
		} else {
			step, handled, err = a.tryConstrainedCapabilityDecision(
				ctx, recorder, parentID, stepPlan, stepRequest, available, allowedSkills,
			)
		}
		if err != nil {
			return response, true, err
		}
		if !handled || len(step.SkillRuns) != 1 || step.SkillRuns[0].Status != "success" {
			return response, true, fmt.Errorf("required Skill %s was not executed", target)
		}
		response.SkillRuns = append(response.SkillRuns, step.SkillRuns...)
		response.ToolRuns = append(response.ToolRuns, step.ToolRuns...)
		response.Message = step.Message
		priorOutputs = append(priorOutputs, target+": "+step.SkillRuns[0].Output)
		priorSkillOutputs = append(priorSkillOutputs, step.SkillRuns[0].Output)
	}
	finalized, err := a.finalizeResponse(recorder, parentID, plan, request, response)
	return finalized, true, err
}

// typedHandoffArguments binds trusted structured output from an earlier Skill
// directly to a later Skill when it already satisfies the later input Schema.
// This avoids asking a small model to copy execution evidence into another JSON
// envelope while keeping Schema validation and the normal execution policy gate.
func typedHandoffArguments(priorOutputs []string, inputSchema map[string]any) (json.RawMessage, bool) {
	for index := len(priorOutputs) - 1; index >= 0; index-- {
		raw := json.RawMessage(strings.TrimSpace(priorOutputs[index]))
		var object map[string]any
		if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
			continue
		}
		if validateCapabilityDecisionArguments(raw, inputSchema) == nil {
			return raw, true
		}
	}
	return nil, false
}

func constrainedSkillSequenceTargets(
	plan roles.Plan,
	available []tools.Manifest,
	allowedSkills map[string]skills.Manifest,
) ([]string, bool) {
	requiredSkills := planRequiredSkills(plan)
	if len(requiredSkills) < 2 || len(planRequiredTools(plan)) != 0 {
		return nil, false
	}
	targets := make([]string, 0, len(requiredSkills))
	for _, name := range requiredSkills {
		if _, ok := allowedSkills[name]; !ok || toolsManifestForName(available, name).Name == "" {
			return nil, false
		}
		targets = append(targets, name)
	}
	return targets, true
}

func planWithRequiredSkill(plan roles.Plan, name string) roles.Plan {
	result := plan
	result.Tasks = append([]roles.Task(nil), plan.Tasks...)
	for index := range result.Tasks {
		result.Tasks[index].RequiredSkills = []string{name}
		result.Tasks[index].RequiredTools = nil
	}
	return result
}

func (a *Agent) tryConstrainedCapabilityDecision(
	ctx context.Context,
	recorder *trace.Recorder,
	parentID string,
	plan roles.Plan,
	request protocol.ReplyRequest,
	available []tools.Manifest,
	allowedSkills map[string]skills.Manifest,
) (protocol.ReplyResponse, bool, error) {
	name, manifest, isSkill, ok := directInvocationTarget(plan, available, allowedSkills)
	if !ok {
		return protocol.ReplyResponse{}, false, nil
	}
	schema, err := json.Marshal(manifest.InputSchema)
	if err != nil {
		return protocol.ReplyResponse{}, true, err
	}
	userPrompt := routingSelectionPrompt(request)
	mandatoryExecution := requiredOfficeCapability(plan, request, name)
	executionRule := "判断用户是否要求现在执行该能力，并按输入 Schema 填参数。"
	if mandatoryExecution {
		executionRule = "这是 ECS Office 已授权并要求执行的必选步骤；execute 必须为 true，只需按输入 Schema 填参数。"
	}
	messages := []chatMessage{
		{
			Role: "system",
			Content: fmt.Sprintf(
				"你是 ECS 能力调用判定器。能力已经锁定为 %s。用途：%s。\n"+
					"%s\n"+
					"语义规则：要求试用、运行、生成、计算、查询即时结果属于执行；只问用途、含义、使用方法，或明确说不要执行，才属于不执行。\n"+
					"通用示例：\"能力名 试一下\" => execute=true；\"运行一下能力名\" => execute=true；\"能力名是做什么的\" => execute=false；\"不要执行能力名\" => execute=false。\n"+
					"需要执行时只输出 {\"execute\":true,\"arguments\":{...}}。\n"+
					"用户只是在咨询、否定或要求不要执行时，只输出 {\"execute\":false}。\n"+
					"不要输出 tool_call、工具名、id、哈希、Markdown 或解释。没有必填参数且用户未提供参数时 arguments 必须是 {}。\n"+
					"输入 Schema：%s",
				name,
				manifest.Description,
				executionRule,
				schema,
			),
		},
		{Role: "user", Content: userPrompt},
	}
	model := request.Model
	if model == "" {
		model = a.llm.cfg.Model
	}
	span := recorder.StartSpan(parentID, "model", "capability-decision", map[string]any{
		"capability": name,
		"model":      model,
	})
	var decision capabilityDecision
	var answer chatMessage
	var decisionErr error
	for attempt := 0; attempt < 2; attempt++ {
		answer, decisionErr = a.llm.CompleteDeterministic(ctx, request.Model, messages)
		if decisionErr != nil {
			break
		}
		decision, decisionErr = parseCapabilityDecision(answer.Content)
		if decisionErr == nil && mandatoryExecution && !decision.Execute {
			decisionErr = errors.New("Office required capability must execute")
		}
		if decisionErr == nil && decision.Execute {
			decisionErr = validateCapabilityDecisionArguments(decision.Arguments, manifest.InputSchema)
		}
		if decisionErr == nil {
			break
		}
		messages = append(messages,
			answer,
			chatMessage{
				Role: "user",
				Content: "上一条输出无效：" + decisionErr.Error() +
					"。重新判断，只输出规定的 JSON 对象。",
			},
		)
	}
	if decisionErr != nil {
		recorder.FinishSpan(span, "error", answer.Content, decisionErr, map[string]any{
			"capability": name,
		})
		return protocol.ReplyResponse{}, true, fmt.Errorf("capability decision for %s: %w", name, decisionErr)
	}
	recorder.FinishSpan(span, "success", decision, nil, map[string]any{
		"capability": name,
		"execute":    decision.Execute,
	})
	if !decision.Execute {
		reply := renderNonExecutionCapabilityReply(name, manifest)
		response := protocol.ReplyResponse{Message: protocol.Message{Role: "assistant", Content: reply}}
		finalized, err := a.finalizeResponse(recorder, parentID, planWithoutRequiredCalls(plan), request, response)
		return finalized, true, err
	}
	response, err := a.executeResolvedCapability(
		ctx, recorder, parentID, plan, request, name, manifest, isSkill, decision.Arguments, "model_decision",
	)
	return response, true, err
}

func requiredOfficeCapability(plan roles.Plan, request protocol.ReplyRequest, name string) bool {
	return strings.TrimSpace(request.Metadata["office_task"]) != "" &&
		containsString(planRequiredSkills(plan), name)
}

func validateCapabilityDecisionArguments(arguments json.RawMessage, schema map[string]any) error {
	var object map[string]any
	if err := json.Unmarshal(arguments, &object); err != nil || object == nil {
		return errors.New("arguments must be a JSON object")
	}
	if missing := ecsschema.MissingRequired(object, schema); len(missing) > 0 {
		return fmt.Errorf("arguments missing required fields: %s", strings.Join(missing, ", "))
	}
	return tools.ValidateArguments(arguments, schema)
}

func renderNonExecutionCapabilityReply(name string, manifest tools.Manifest) string {
	required, _ := requiredFields(manifest.InputSchema)
	requiredSet := make(map[string]bool, len(required))
	for _, field := range required {
		requiredSet[field] = true
	}
	properties, _ := manifest.InputSchema["properties"].(map[string]any)
	names := make([]string, 0, len(properties))
	for field := range properties {
		names = append(names, field)
	}
	sort.Strings(names)
	parameters := make([]string, 0, len(names))
	for _, field := range names {
		property, _ := properties[field].(map[string]any)
		kind, _ := property["type"].(string)
		if kind == "" {
			kind = "any"
		}
		necessity := "可选"
		if requiredSet[field] {
			necessity = "必填"
		}
		parameters = append(parameters, fmt.Sprintf("`%s`（%s，%s）", field, kind, necessity))
	}
	parameterText := "无参数"
	if len(parameters) > 0 {
		parameterText = strings.Join(parameters, "、")
	}
	return fmt.Sprintf(
		"未执行 `%s`。用途：%s。参数：%s。",
		name,
		manifest.Description,
		parameterText,
	)
}

func parseCapabilityDecision(content string) (capabilityDecision, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return capabilityDecision{}, errors.New("decision is not a JSON object")
	}
	var decision capabilityDecision
	if err := json.Unmarshal([]byte(content[start:end+1]), &decision); err != nil {
		return capabilityDecision{}, fmt.Errorf("decode decision: %w", err)
	}
	if decision.Execute && (len(decision.Arguments) == 0 || !json.Valid(decision.Arguments)) {
		return capabilityDecision{}, errors.New("execute decision requires valid arguments")
	}
	return decision, nil
}

func planWithoutRequiredCalls(plan roles.Plan) roles.Plan {
	result := plan
	result.Tasks = append([]roles.Task(nil), plan.Tasks...)
	for index := range result.Tasks {
		result.Tasks[index].RequiredSkills = nil
		result.Tasks[index].RequiredTools = nil
	}
	return result
}

func (a *Agent) executeResolvedCapability(
	ctx context.Context,
	recorder *trace.Recorder,
	parentID string,
	plan roles.Plan,
	request protocol.ReplyRequest,
	name string,
	manifest tools.Manifest,
	isSkill bool,
	arguments json.RawMessage,
	route string,
) (protocol.ReplyResponse, error) {
	canonical, missing, err := tools.PrepareArguments(
		arguments,
		manifest,
		ecsschema.RuntimeContext{
			RequestID: request.RequestID,
			TraceID:   request.TraceID,
			SessionID: request.SessionID,
		},
	)
	if len(missing) > 0 {
		return missingInputResponse(
			request,
			name,
			arguments,
			modelFacingMissingFields(missing, manifest),
		), nil
	}
	if err != nil {
		return protocol.ReplyResponse{}, err
	}
	arguments = canonical
	approved := false
	for _, approvedTool := range request.ApprovedTools {
		if approvedTool == name {
			approved = true
			break
		}
	}
	if a.requiresApproval(manifest) && !approved {
		return protocol.ReplyResponse{}, &ApprovalError{
			Tool:        name,
			Description: manifest.Description,
			Risk:        normalizedRisk(manifest.Risk),
			Arguments:   arguments,
		}
	}

	started := time.Now()
	kind := "tool"
	if isSkill {
		kind = "skill"
	}
	span := recorder.StartSpan(parentID, kind, name, map[string]any{
		"arguments": arguments,
		"route":     route,
	})
	response := protocol.ReplyResponse{}
	if isSkill {
		run, err := a.skillRun.Execute(ctx, name, arguments)
		response.SkillRuns = []protocol.SkillRun{run}
		response.ToolRuns = append(response.ToolRuns, run.ToolRuns...)
		response.Message = protocol.Message{Role: "assistant", Content: renderCapabilityOutput(run.Output)}
		status := "success"
		if err != nil {
			status = "error"
		}
		recorder.FinishSpan(span, status, run.Output, err, map[string]any{
			"duration_ms": time.Since(started).Milliseconds(),
			"route":       route,
		})
		if err != nil {
			return response, err
		}
	} else {
		output, err := a.registry.Execute(ctx, name, arguments)
		run := protocol.ToolRun{
			Name:      name,
			Arguments: arguments,
			Output:    output,
			Duration:  time.Since(started).Milliseconds(),
		}
		if err != nil {
			run.Error = err.Error()
		}
		response.ToolRuns = []protocol.ToolRun{run}
		response.Message = protocol.Message{Role: "assistant", Content: renderCapabilityOutput(output)}
		status := "success"
		if err != nil {
			status = "error"
		}
		recorder.FinishSpan(span, status, output, err, map[string]any{
			"duration_ms": time.Since(started).Milliseconds(),
			"route":       route,
		})
		if err != nil {
			return response, err
		}
	}
	finalized, err := a.finalizeResponse(recorder, parentID, plan, request, response)
	return finalized, err
}

// renderCapabilityOutput converts any JSON Tool/Skill result into a stable,
// human-readable response without asking the model to restate it. Every scalar
// value is preserved verbatim so the verifier can match the response against
// the canonical execution evidence.
func renderCapabilityOutput(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return trimmed
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return trimmed
	}
	if decoder.More() {
		return trimmed
	}

	if object, ok := value.(map[string]any); ok {
		if result, exists := object["result"]; exists && len(object) == 1 {
			return "结果：" + renderOutputValue(result) + "。"
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, renderOutputField(displayOutputField(key), object[key])...)
		}
		if len(parts) > 0 {
			return "执行结果：" + strings.Join(parts, "；") + "。"
		}
	}
	return "执行结果：" + renderOutputValue(value) + "。"
}

// normalizeExecutedCapabilityResponse replaces a model's verbatim JSON echo
// with deterministic presentation of the matching execution output. Natural
// language synthesis is left untouched, as are JSON values that did not come
// from a Tool/Skill run.
func normalizeExecutedCapabilityResponse(
	content string,
	toolRuns []protocol.ToolRun,
	skillRuns []protocol.SkillRun,
) string {
	if !json.Valid([]byte(strings.TrimSpace(content))) {
		return content
	}
	outputs := make([]string, 0, len(skillRuns)+len(toolRuns))
	for _, run := range skillRuns {
		if run.Status == "success" {
			outputs = append(outputs, run.Output)
		}
	}
	for _, run := range toolRuns {
		if run.Error == "" {
			outputs = append(outputs, run.Output)
		}
	}
	for _, output := range outputs {
		if equivalentJSON(content, output) {
			return renderCapabilityOutput(output)
		}
	}
	return content
}

func equivalentJSON(left string, right string) bool {
	if !json.Valid([]byte(strings.TrimSpace(right))) {
		return false
	}
	canonical := func(raw string) (string, bool) {
		var value any
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return "", false
		}
		encoded, err := json.Marshal(value)
		return string(encoded), err == nil
	}
	leftCanonical, leftOK := canonical(left)
	rightCanonical, rightOK := canonical(right)
	return leftOK && rightOK && leftCanonical == rightCanonical
}

func renderOutputField(path string, value any) []string {
	switch item := value.(type) {
	case map[string]any:
		if len(item) == 0 {
			return []string{path + " {}"}
		}
		keys := make([]string, 0, len(item))
		for key := range item {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var parts []string
		for _, key := range keys {
			parts = append(parts, renderOutputField(path+"."+displayOutputField(key), item[key])...)
		}
		return parts
	case []any:
		if len(item) == 0 {
			return []string{path + " []"}
		}
		var parts []string
		for index, child := range item {
			parts = append(parts, renderOutputField(fmt.Sprintf("%s[%d]", path, index), child)...)
		}
		return parts
	default:
		return []string{path + " " + renderOutputValue(item)}
	}
}

func renderOutputValue(value any) string {
	switch item := value.(type) {
	case string:
		return item
	case json.Number:
		return item.String()
	case bool:
		return fmt.Sprint(item)
	case nil:
		return "null"
	default:
		encoded, err := json.Marshal(item)
		if err != nil {
			return fmt.Sprint(item)
		}
		compact := new(bytes.Buffer)
		if err := json.Compact(compact, encoded); err == nil {
			return compact.String()
		}
		return string(encoded)
	}
}

func displayOutputField(field string) string {
	labels := map[string]string{
		"result":        "结果",
		"id":            "ID",
		"characters":    "字符数",
		"total_chars":   "总字符数",
		"chinese_chars": "汉字数",
		"lines":         "行数",
		"words":         "单词数",
		"digits":        "数字数",
		"letters":       "字母数",
		"punctuation":   "标点数",
		"spaces":        "空白数",
		"summary":       "摘要",
	}
	if label := labels[field]; label != "" {
		return label
	}
	return field
}

func directInvocationTarget(
	plan roles.Plan,
	available []tools.Manifest,
	allowedSkills map[string]skills.Manifest,
) (string, tools.Manifest, bool, bool) {
	requiredSkills := planRequiredSkills(plan)
	requiredTools := planRequiredTools(plan)
	if len(requiredSkills) == 1 &&
		(len(requiredTools) == 0 || (len(requiredTools) == 1 && requiredTools[0] == requiredSkills[0])) {
		name := requiredSkills[0]
		if _, ok := allowedSkills[name]; ok {
			manifest := toolsManifestForName(available, name)
			if manifest.Name != "" {
				return name, manifest, true, true
			}
		}
	}
	if len(requiredSkills) == 0 && len(requiredTools) == 1 {
		name := requiredTools[0]
		manifest := toolsManifestForName(available, name)
		if manifest.Name != "" {
			return name, manifest, false, true
		}
	}
	return "", tools.Manifest{}, false, false
}

func toolsManifestForName(available []tools.Manifest, name string) tools.Manifest {
	for _, manifest := range available {
		if manifest.Name == name {
			return manifest
		}
	}
	return tools.Manifest{}
}

func routingSelectionPrompt(request protocol.ReplyRequest) string {
	if prompt := strings.TrimSpace(request.Prompt); prompt != "" {
		return prompt
	}
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == "user" {
			return strings.TrimSpace(request.Messages[index].Content)
		}
	}
	return ""
}

func bindManifestInput(prompt string, name string, schema map[string]any) (json.RawMessage, bool) {
	prompt = strings.TrimSpace(prompt)
	if len(prompt) < len(name) || !strings.EqualFold(prompt[:len(name)], name) {
		return nil, false
	}
	remainder := strings.TrimSpace(prompt[len(name):])
	if remainder == "" {
		required, ok := requiredFields(schema)
		if ok && len(required) == 0 {
			return json.RawMessage(`{}`), true
		}
		return nil, false
	}
	if strings.HasPrefix(remainder, ":") {
		remainder = strings.TrimSpace(strings.TrimPrefix(remainder, ":"))
	} else if strings.HasPrefix(remainder, "：") {
		remainder = strings.TrimSpace(strings.TrimPrefix(remainder, "："))
	} else {
		return nil, false
	}
	remainder = trimMatchingQuotes(remainder)
	if remainder == "" {
		return nil, false
	}
	if strings.HasPrefix(remainder, "{") && json.Valid([]byte(remainder)) {
		return json.RawMessage(remainder), true
	}
	field, ok := singleRequiredStringField(schema)
	if !ok {
		return nil, false
	}
	arguments, err := json.Marshal(map[string]string{field: remainder})
	if err != nil {
		return nil, false
	}
	return arguments, true
}

func singleRequiredStringField(schema map[string]any) (string, bool) {
	required, ok := requiredFields(schema)
	if !ok || len(required) != 1 {
		return "", false
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return "", false
	}
	property, ok := properties[required[0]].(map[string]any)
	if !ok || property["type"] != "string" {
		return "", false
	}
	return required[0], true
}

func requiredFields(schema map[string]any) ([]string, bool) {
	value, exists := schema["required"]
	if !exists {
		return nil, true
	}
	return stringSlice(value)
}

func stringSlice(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return values, true
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	default:
		return nil, false
	}
}

func trimMatchingQuotes(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) < 2 {
		return string(runes)
	}
	pairs := map[rune]rune{'"': '"', '\'': '\'', '“': '”', '‘': '’'}
	if closing, ok := pairs[runes[0]]; ok && runes[len(runes)-1] == closing {
		return strings.TrimSpace(string(runes[1 : len(runes)-1]))
	}
	return string(runes)
}
