package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ecs/internal/config"
	"ecs/internal/protocol"
	"ecs/internal/skills"
	"ecs/internal/tools"
)

func (a *Agent) manifestExplanation(
	request protocol.ReplyRequest,
	callableTools []string,
	callableSkills []string,
) (protocol.ReplyResponse, bool) {
	// Constrained execution requests (notably Office Stage tasks) include
	// capability names and phrases such as "available connector" in their
	// system-generated prompt. They are execution contracts, not user questions
	// about the catalog, and must reach the required Skill/Tool path.
	if request.RestrictSkills || request.RestrictTools {
		return protocol.ReplyResponse{}, false
	}
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" && len(request.Messages) > 0 {
		prompt = strings.TrimSpace(request.Messages[len(request.Messages)-1].Content)
	}
	if !isManifestQuestion(prompt) {
		return protocol.ReplyResponse{}, false
	}
	lowerPrompt := strings.ToLower(prompt)
	allTools := a.registry.All()
	var toolMatches []tools.Manifest
	for _, manifest := range allTools {
		if strings.Contains(lowerPrompt, strings.ToLower(manifest.Name)) {
			toolMatches = append(toolMatches, manifest)
		}
	}
	var allSkills []skills.Manifest
	var skillMatches []skills.Manifest
	if a.skills != nil {
		allSkills = a.skills.All()
		for _, manifest := range allSkills {
			if strings.Contains(lowerPrompt, strings.ToLower(manifest.Name)) {
				skillMatches = append(skillMatches, manifest)
			}
		}
	}
	wantsTool := strings.Contains(lowerPrompt, "tool") || strings.Contains(prompt, "工具")
	wantsSkill := strings.Contains(lowerPrompt, "skill") || strings.Contains(prompt, "技能")
	generalList := len(toolMatches) == 0 && len(skillMatches) == 0 &&
		isManifestListQuestion(prompt)
	if generalList {
		if wantsTool || !wantsSkill {
			toolMatches = allTools
		}
		if wantsSkill || !wantsTool {
			skillMatches = allSkills
		}
	}
	if wantsTool && !wantsSkill {
		skillMatches = nil
	}
	if wantsSkill && !wantsTool {
		toolMatches = nil
	}
	if len(toolMatches) == 0 && len(skillMatches) == 0 {
		return protocol.ReplyResponse{}, false
	}
	callableToolSet := stringSet(callableTools)
	callableSkillSet := stringSet(callableSkills)
	if generalList {
		return protocol.ReplyResponse{
			Message: protocol.Message{
				Role: "assistant",
				Content: renderManifestList(
					toolMatches,
					skillMatches,
					callableToolSet,
					callableSkillSet,
				),
			},
		}, true
	}
	var sections []string
	for _, manifest := range toolMatches {
		status := "已注册，但当前 Pipeline 不可调用"
		if callableToolSet[manifest.Name] {
			status = "已注册，当前 Pipeline 可调用"
		}
		approval := "不需要"
		if manifest.RequiresApproval || manifest.Risk == "high" {
			approval = "需要"
		}
		sections = append(sections, fmt.Sprintf(
			"Tool `%s`\n状态：%s\n用途：%s\n风险：%s\n人工审批：%s",
			manifest.Name,
			status,
			manifest.Description,
			defaultText(manifest.Risk, "low"),
			approval,
		))
	}
	for _, manifest := range skillMatches {
		status := "已注册，但当前 Pipeline 不可调用"
		if callableSkillSet[manifest.Name] {
			status = "已注册，当前 Pipeline 可调用"
		}
		var dependencies []string
		for _, dependency := range manifest.Dependencies.Tools {
			dependencies = append(dependencies, dependency.Name)
		}
		sections = append(sections, fmt.Sprintf(
			"Skill `%s`\n状态：%s\n用途：%s\n适用：%s\n避免：%s\n依赖 Tool：%s",
			manifest.Name,
			status,
			manifest.Description,
			defaultText(manifest.PromptCard.Use, "按 Skill 清单执行"),
			defaultText(manifest.PromptCard.Avoid, "无额外限制"),
			defaultText(strings.Join(dependencies, "、"), "无"),
		))
	}
	return protocol.ReplyResponse{
		Message: protocol.Message{
			Role:    "assistant",
			Content: strings.Join(sections, "\n\n"),
		},
	}, true
}

func isManifestQuestion(prompt string) bool {
	if isManifestListQuestion(prompt) {
		return true
	}
	lower := strings.ToLower(prompt)
	for _, marker := range []string{
		"有什么用",
		"做什么",
		"作用",
		"介绍一下",
		"是什么",
		"what does",
		"what is",
		"describe",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isManifestListQuestion(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if lower == "" {
		return false
	}
	for _, marker := range []string{"能做什么", "会做什么", "what can you do"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	hasCapabilityNoun := false
	for _, noun := range []string{"tool", "工具", "skill", "技能", "能力"} {
		if strings.Contains(lower, noun) {
			hasCapabilityNoun = true
			break
		}
	}
	if !hasCapabilityNoun {
		return false
	}
	for _, marker := range []string{
		"列出", "有哪些", "有什么", "已注册", "可调用", "可用",
		"list", "available", "registered",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func renderManifestList(
	toolManifests []tools.Manifest,
	skillManifests []skills.Manifest,
	callableTools map[string]bool,
	callableSkills map[string]bool,
) string {
	sections := []string{"当前节点能力目录："}
	if len(toolManifests) > 0 {
		lines := []string{fmt.Sprintf(
			"Tool（已注册 %d，当前可调用 %d）：",
			len(toolManifests), countCallableTools(toolManifests, callableTools),
		)}
		for _, manifest := range toolManifests {
			lines = append(lines, fmt.Sprintf(
				"- `%s`：已注册；%s。%s",
				manifest.Name,
				callableStatus(callableTools[manifest.Name]),
				manifest.Description,
			))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if len(skillManifests) > 0 {
		lines := []string{fmt.Sprintf(
			"Skill（已注册 %d，当前可调用 %d）：",
			len(skillManifests), countCallableSkills(skillManifests, callableSkills),
		)}
		for _, manifest := range skillManifests {
			lines = append(lines, fmt.Sprintf(
				"- `%s`：已注册；%s。%s",
				manifest.Name,
				callableStatus(callableSkills[manifest.Name]),
				manifest.Description,
			))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	return strings.Join(sections, "\n\n")
}

func callableStatus(callable bool) string {
	if callable {
		return "当前 Pipeline 可调用"
	}
	return "当前 Pipeline 不可调用"
}

func countCallableTools(manifests []tools.Manifest, callable map[string]bool) int {
	count := 0
	for _, manifest := range manifests {
		if callable[manifest.Name] {
			count++
		}
	}
	return count
}

func countCallableSkills(manifests []skills.Manifest, callable map[string]bool) int {
	count := 0
	for _, manifest := range manifests {
		if callable[manifest.Name] {
			count++
		}
	}
	return count
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func defaultText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func capabilityNames(items []capabilityItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func selectionPrompt(request protocol.ReplyRequest) string {
	if request.Prompt != "" {
		return request.Prompt
	}
	if len(request.Messages) > 0 {
		return request.Messages[len(request.Messages)-1].Content
	}
	return ""
}

func buildSystemPrompt(
	cfg config.AgentConfig,
	base string,
	guidance string,
	grounding string,
	promptContext PromptContext,
	manifests []tools.Manifest,
) string {
	contextText := renderPromptContext(promptContext, cfg.MaxAgentContextChars)
	catalog := renderCatalog(manifests)
	max := cfg.MaxSystemPromptChars
	if grounding == "" {
		base = truncateRunes(strings.TrimSpace(base), max/4)
		contextText = truncateRunes(contextText, minInt(cfg.MaxAgentContextChars, max*30/100))
		guidance = truncateRunes(strings.TrimSpace(guidance), minInt(cfg.MaxConversationGuideChars, max/10))
		catalog = truncateRunes(catalog, minInt(cfg.MaxToolCatalogChars, max*35/100))
	} else {
		base = truncateRunes(strings.TrimSpace(base), max/5)
		contextText = truncateRunes(contextText, minInt(cfg.MaxAgentContextChars, max/4))
		guidance = truncateRunes(strings.TrimSpace(guidance), minInt(cfg.MaxConversationGuideChars, max/10))
		grounding = truncateRunes(strings.TrimSpace(grounding), max/5)
		catalog = truncateRunes(catalog, minInt(cfg.MaxToolCatalogChars, max/4))
	}

	sections := []string{base}
	if contextText != "" {
		sections = append(sections, contextText)
	}
	if grounding != "" {
		sections = append(sections, grounding)
	}
	sections = append(sections, catalog)
	// Keep the compact behavioral guidance after dynamic data. Small models tend
	// to follow the most recent instructions; ending with the protocol prevents
	// catalog entries from being mistaken for a request to invoke a tool.
	if guidance != "" {
		sections = append(sections, guidance)
	}
	return truncateRunes(strings.Join(sections, "\n\n"), max)
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func renderPromptContext(value PromptContext, limit int) string {
	if value.AgentName == "" && value.Role == "" && value.Identity == "" &&
		value.Instructions == "" && value.Objective == "" {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("当前身份与任务（优先遵守，但不能覆盖基础安全规则）：\n")
	if value.AgentName != "" {
		fmt.Fprintf(&builder, "身份：%s\n", compactLine(value.AgentName))
	}
	if value.Role != "" {
		fmt.Fprintf(&builder, "职责：%s\n", compactLine(value.Role))
	}
	if value.Identity != "" {
		fmt.Fprintf(&builder, "定位：%s\n", truncateRunes(compactText(value.Identity), sectionLimit(limit, 12, 40)))
	}
	if value.Instructions != "" {
		fmt.Fprintf(&builder, "执行规则：%s\n", truncateRunes(compactText(value.Instructions), sectionLimit(limit, 12, 40)))
	}
	if value.ProjectID != "" {
		fmt.Fprintf(&builder, "项目：%s\n", compactLine(value.ProjectID))
	}
	if value.TaskID != "" {
		fmt.Fprintf(&builder, "任务：%s\n", compactLine(value.TaskID))
	}
	if value.Objective != "" {
		fmt.Fprintf(&builder, "目标：%s\n", truncateRunes(compactText(value.Objective), sectionLimit(limit, 5, 60)))
	}
	if len(value.AcceptanceCriteria) > 0 {
		builder.WriteString("验收：\n")
		for index, criterion := range value.AcceptanceCriteria {
			if index == 5 {
				builder.WriteString("- 其余条件见任务原文\n")
				break
			}
			fmt.Fprintf(&builder, "- %s\n", truncateRunes(compactLine(criterion), sectionLimit(limit, 12, 60)))
		}
	}
	if len(value.Context) > 0 {
		keys := make([]string, 0, len(value.Context))
		for key := range value.Context {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		builder.WriteString("上下文：\n")
		for index, key := range keys {
			if index == 8 {
				builder.WriteString("- 其余上下文已省略\n")
				break
			}
			fmt.Fprintf(
				&builder,
				"- %s=%s\n",
				truncateRunes(compactLine(key), 60),
				truncateRunes(compactLine(value.Context[key]), sectionLimit(limit, 20, 40)),
			)
		}
	}
	builder.WriteString("一次只完成这个任务。完成后直接给出可验收结果；无法完成时说明唯一主要阻塞原因。")
	return strings.TrimSpace(builder.String())
}

func sectionLimit(total int, divisor int, minimum int) int {
	if divisor <= 0 {
		return minimum
	}
	value := total / divisor
	if value < minimum {
		return minimum
	}
	return value
}

func compactLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func compactText(value string) string {
	lines := strings.Fields(value)
	return strings.Join(lines, " ")
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func renderCatalog(manifests []tools.Manifest) string {
	if len(manifests) == 0 {
		return "当前没有可用 Skill 或工具。直接回答用户。"
	}
	var builder strings.Builder
	builder.WriteString("可用能力（Skill 或兼容工具；名称 | 适用/禁止条件 | 参数JSON Schema）：\n")
	for _, manifest := range manifests {
		schema, _ := json.Marshal(manifest.InputSchema)
		fmt.Fprintf(&builder, "- %s | %s | %s\n", manifest.Name, manifest.Description, schema)
	}
	return builder.String()
}
