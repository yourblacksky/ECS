package agentteams

import (
	"fmt"
	"strings"

	"ecs/internal/agent"
	"ecs/internal/protocol"
)

func MapTask(task TaskRequest, fallback AgentProfile, maxTaskPromptChars int) (protocol.ReplyRequest, agent.PromptContext) {
	profile := fallback
	model := task.Model
	if model == "" {
		model = profile.Model
	}
	tools := allowedTaskTools(task.Tools, profile.AllowedTools)
	skills := allowedTaskTools(task.Skills, profile.AllowedSkills)
	traceID := EffectiveTraceID(task)
	metadata := map[string]string{
		"agentteams.task_id":  task.TaskID,
		"agentteams.trace_id": traceID,
	}
	if task.ProjectID != "" {
		metadata["agentteams.project_id"] = task.ProjectID
	}
	if task.ParentTaskID != "" {
		metadata["agentteams.parent_task_id"] = task.ParentTaskID
	}
	return protocol.ReplyRequest{
			RequestID:     "agentteams-" + task.TaskID,
			Prompt:        truncateText(renderUserTask(task), maxTaskPromptChars),
			Model:         model,
			Skills:        append([]string(nil), skills...),
			Tools:         append([]string(nil), tools...),
			ApprovedTools: append([]string(nil), task.ApprovedTools...),
			Metadata:      metadata,
		}, agent.PromptContext{
			AgentName:          profile.Name,
			Role:               profile.Role,
			Identity:           profile.Identity,
			Instructions:       profile.Instructions,
			TaskID:             task.TaskID,
			ProjectID:          task.ProjectID,
			Objective:          truncateText(strings.Join(strings.Fields(task.Objective), " "), 320),
			AcceptanceCriteria: append([]string(nil), task.AcceptanceCriteria...),
			Context:            cloneMap(task.Context),
		}
}

func truncateText(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func allowedTaskTools(requested []string, allowed []string) []string {
	if len(allowed) == 0 {
		return append([]string(nil), requested...)
	}
	if len(requested) == 0 {
		return append([]string(nil), allowed...)
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	result := make([]string, 0, len(requested))
	for _, name := range requested {
		if allowedSet[name] {
			result = append(result, name)
		}
	}
	return result
}

func renderUserTask(task TaskRequest) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "任务：%s", strings.TrimSpace(task.Objective))
	if len(task.AcceptanceCriteria) > 0 {
		builder.WriteString("\n验收条件：")
		for index, item := range task.AcceptanceCriteria {
			if index == 5 {
				break
			}
			fmt.Fprintf(&builder, "\n%d. %s", index+1, strings.Join(strings.Fields(item), " "))
		}
	}
	builder.WriteString("\n只处理本任务，直接返回结果。")
	return builder.String()
}

func cloneMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
