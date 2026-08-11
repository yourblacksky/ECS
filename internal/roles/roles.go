package roles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"ecs/internal/config"
	"ecs/internal/protocol"
	ecsschema "ecs/internal/schema"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type Manifest struct {
	SchemaVersion string         `json:"schema_version"`
	Name          string         `json:"name"`
	Purpose       string         `json:"purpose"`
	ExecutionMode string         `json:"execution_mode"`
	Model         string         `json:"model,omitempty"`
	AllowedSkills []string       `json:"allowed_skills,omitempty"`
	AllowedTools  []string       `json:"allowed_tools,omitempty"`
	InputSchema   map[string]any `json:"input_schema"`
	OutputSchema  map[string]any `json:"output_schema"`
	FailurePolicy FailurePolicy  `json:"failure_policy"`
	Enabled       *bool          `json:"enabled,omitempty"`
}

type FailurePolicy struct {
	MaxRetries int    `json:"max_retries"`
	OnFailure  string `json:"on_failure"`
}

type Pipeline struct {
	SchemaVersion string   `json:"schema_version"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Strategy      string   `json:"strategy"`
	Planner       string   `json:"planner,omitempty"`
	Executors     []string `json:"executors"`
	Fallback      string   `json:"fallback_executor,omitempty"`
	Verifier      string   `json:"verifier,omitempty"`
	Enabled       *bool    `json:"enabled,omitempty"`
}

type Plan struct {
	Pipeline string `json:"pipeline"`
	Planner  string `json:"planner,omitempty"`
	Strategy string `json:"strategy"`
	Tasks    []Task `json:"tasks"`
	Verifier string `json:"verifier,omitempty"`
}

type Task struct {
	ID             string   `json:"id"`
	Agent          string   `json:"agent"`
	Objective      string   `json:"objective"`
	AllowedSkills  []string `json:"allowed_skills,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	RequiredSkills []string `json:"required_skills,omitempty"`
	RequiredTools  []string `json:"required_tools,omitempty"`
}

type Verification struct {
	Verifier         string   `json:"verifier"`
	Status           string   `json:"status"`
	Validation       string   `json:"validation_result"`
	Evidence         []string `json:"evidence,omitempty"`
	Errors           []string `json:"errors,omitempty"`
	RetryRecommended bool     `json:"retry_recommended"`
}

type Registry struct {
	cfg    config.AgentRolesConfig
	logger *slog.Logger
	mu     sync.RWMutex
	items  map[string]Manifest
	pipes  map[string]Pipeline
}

func NewRegistry(cfg config.AgentRolesConfig, logger *slog.Logger) *Registry {
	return &Registry{
		cfg:    cfg,
		logger: logger,
		items:  make(map[string]Manifest),
		pipes:  make(map[string]Pipeline),
	}
}

func (r *Registry) Reload() error {
	next, err := loadRoles(r.cfg.Directory)
	if err != nil {
		return err
	}
	pipelines, err := loadPipelines(r.cfg.PipelineDirectory, next)
	if err != nil {
		return err
	}
	if _, ok := pipelines[r.cfg.DefaultPipeline]; !ok {
		return fmt.Errorf("default agent pipeline %q is missing", r.cfg.DefaultPipeline)
	}
	r.mu.Lock()
	r.items = next
	r.pipes = pipelines
	r.mu.Unlock()
	r.logger.Info(
		"agent role registry loaded",
		"roles", len(next),
		"pipelines", len(pipelines),
		"default_pipeline", r.cfg.DefaultPipeline,
	)
	return nil
}

func loadRoles(directory string) (map[string]Manifest, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				return nil, err
			}
			return map[string]Manifest{}, nil
		}
		return nil, err
	}
	next := make(map[string]Manifest)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read agent role %s: %w", path, err)
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("decode agent role %s: %w", path, err)
		}
		if err := validate(manifest); err != nil {
			return nil, fmt.Errorf("invalid agent role %s: %w", path, err)
		}
		if manifest.Enabled != nil && !*manifest.Enabled {
			continue
		}
		if _, exists := next[manifest.Name]; exists {
			return nil, fmt.Errorf("duplicate agent role %q", manifest.Name)
		}
		next[manifest.Name] = manifest
	}
	return next, nil
}

func loadPipelines(directory string, roles map[string]Manifest) (map[string]Pipeline, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				return nil, err
			}
			return map[string]Pipeline{}, nil
		}
		return nil, err
	}
	next := make(map[string]Pipeline)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read agent pipeline %s: %w", path, err)
		}
		var pipeline Pipeline
		if err := json.Unmarshal(data, &pipeline); err != nil {
			return nil, fmt.Errorf("decode agent pipeline %s: %w", path, err)
		}
		if pipeline.Enabled != nil && !*pipeline.Enabled {
			continue
		}
		if err := validatePipeline(pipeline, roles); err != nil {
			return nil, fmt.Errorf("invalid agent pipeline %s: %w", path, err)
		}
		if _, exists := next[pipeline.Name]; exists {
			return nil, fmt.Errorf("duplicate agent pipeline %q", pipeline.Name)
		}
		next[pipeline.Name] = pipeline
	}
	return next, nil
}

func validate(manifest Manifest) error {
	if manifest.SchemaVersion != "ecs.agent/v1" {
		return errors.New("schema_version must be ecs.agent/v1")
	}
	if !validName.MatchString(manifest.Name) || strings.TrimSpace(manifest.Purpose) == "" {
		return errors.New("valid name and purpose are required")
	}
	switch manifest.ExecutionMode {
	case "deterministic", "llm":
	default:
		return errors.New("execution_mode must be deterministic or llm")
	}
	if manifest.InputSchema == nil || manifest.OutputSchema == nil {
		return errors.New("input_schema and output_schema are required")
	}
	if err := ecsschema.ValidateDefinition(manifest.InputSchema, true); err != nil {
		return fmt.Errorf("invalid input_schema: %w", err)
	}
	if err := ecsschema.ValidateDefinition(manifest.OutputSchema, false); err != nil {
		return fmt.Errorf("invalid output_schema: %w", err)
	}
	if manifest.FailurePolicy.MaxRetries < 0 || manifest.FailurePolicy.MaxRetries > 2 {
		return errors.New("failure_policy.max_retries must be 0..2")
	}
	switch manifest.FailurePolicy.OnFailure {
	case "fail", "return_error", "request_retry":
	default:
		return errors.New("failure_policy.on_failure is invalid")
	}
	return nil
}

func validatePipeline(pipeline Pipeline, roles map[string]Manifest) error {
	if pipeline.SchemaVersion != "ecs.pipeline/v1" {
		return errors.New("schema_version must be ecs.pipeline/v1")
	}
	if !validName.MatchString(pipeline.Name) {
		return errors.New("valid pipeline name is required")
	}
	if pipeline.Strategy != "capability_single_task" {
		return errors.New("strategy must be capability_single_task")
	}
	if len(pipeline.Executors) == 0 {
		return errors.New("at least one executor is required")
	}
	if pipeline.Fallback != "" && !contains(pipeline.Executors, pipeline.Fallback) {
		return errors.New("fallback_executor must be one of executors")
	}
	references := append([]string(nil), pipeline.Executors...)
	if pipeline.Planner != "" {
		references = append(references, pipeline.Planner)
	}
	if pipeline.Verifier != "" {
		references = append(references, pipeline.Verifier)
	}
	for _, name := range references {
		if !validName.MatchString(name) {
			return fmt.Errorf("invalid role reference %q", name)
		}
		if _, ok := roles[name]; !ok {
			return fmt.Errorf("role %q is not installed", name)
		}
	}
	for _, name := range pipeline.Executors {
		if roles[name].ExecutionMode != "llm" {
			return fmt.Errorf("executor role %q must use llm execution_mode", name)
		}
	}
	return nil
}

func (r *Registry) Get(name string) (Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.items[name]
	return value, ok
}

func (r *Registry) All() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Manifest, 0, len(r.items))
	for _, value := range r.items {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (r *Registry) Pipelines() []Pipeline {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Pipeline, 0, len(r.pipes))
	for _, value := range r.pipes {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (r *Registry) DefaultPipeline() (Pipeline, bool) {
	return r.Pipeline(r.cfg.DefaultPipeline)
}

func (r *Registry) Pipeline(name string) (Pipeline, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.pipes[name]
	return value, ok
}

func (r *Registry) Route(request protocol.ReplyRequest) (protocol.ReplyRequest, Plan, error) {
	pipelineName := request.Pipeline
	if pipelineName == "" {
		pipelineName = r.cfg.DefaultPipeline
	}
	pipeline, ok := r.Pipeline(pipelineName)
	if !ok {
		return request, Plan{}, fmt.Errorf("pipeline %q is not loaded", pipelineName)
	}
	requestedSkills, requestedTools := expandRequested(request)
	requiredSkills := append([]string(nil), request.RequiredSkills...)
	requiredTools := append([]string(nil), request.RequiredTools...)
	if request.Agent != "" {
		return r.routeForAgent(
			request, pipeline, requestedSkills, requestedTools, requiredSkills, requiredTools,
		)
	}
	explicitPromptCapabilities := false
	if !request.RestrictSkills && !request.RestrictTools &&
		len(requestedSkills) == 0 && len(requestedTools) == 0 {
		prompt := routingPrompt(request)
		explicitSkills, explicitTools := r.explicitCapabilitiesForPrompt(pipeline, prompt)
		if len(explicitSkills) > 0 || len(explicitTools) > 0 {
			requestedSkills = explicitSkills
			requestedTools = explicitTools
			requiredSkills = append([]string(nil), explicitSkills...)
			requiredTools = append([]string(nil), explicitTools...)
			explicitPromptCapabilities = true
		} else if required := requiredSkillForPrompt(prompt); required != "" {
			requestedSkills = []string{required}
			requestedTools = []string{required}
			requiredSkills = []string{required}
			requiredTools = []string{required}
			explicitPromptCapabilities = true
		}
	}
	executor, err := r.selectExecutor(pipeline, requestedSkills, requestedTools)
	if err != nil {
		return request, Plan{}, err
	}
	skills, err := constrain(
		requestedSkills,
		executor.AllowedSkills,
		"skill",
		executor.Name,
		request.RestrictSkills || explicitPromptCapabilities,
	)
	if err != nil {
		return request, Plan{}, err
	}
	tools, err := constrain(
		requestedTools,
		executor.AllowedTools,
		"tool",
		executor.Name,
		request.RestrictTools || explicitPromptCapabilities,
	)
	if err != nil {
		return request, Plan{}, err
	}
	request.Skills = skills
	request.Tools = tools
	if denied := capabilitiesOutside(requiredSkills, skills); len(denied) > 0 {
		return request, Plan{}, fmt.Errorf("required skills are outside the executor policy: %s", strings.Join(denied, ", "))
	}
	if denied := capabilitiesOutside(requiredTools, tools); len(denied) > 0 {
		return request, Plan{}, fmt.Errorf("required tools are outside the executor policy: %s", strings.Join(denied, ", "))
	}
	if request.Model == "" {
		request.Model = executor.Model
	}
	objective := request.Prompt
	if objective == "" && len(request.Messages) > 0 {
		objective = request.Messages[len(request.Messages)-1].Content
	}
	return request, Plan{
		Pipeline: pipeline.Name,
		Planner:  pipeline.Planner,
		Strategy: "ecs-capability-single-task",
		Tasks: []Task{{
			ID:             "task-1",
			Agent:          executor.Name,
			Objective:      objective,
			AllowedSkills:  append([]string(nil), skills...),
			AllowedTools:   append([]string(nil), tools...),
			RequiredSkills: append([]string(nil), requiredSkills...),
			RequiredTools:  append([]string(nil), requiredTools...),
		}},
		Verifier: pipeline.Verifier,
	}, nil
}

func expandRequested(request protocol.ReplyRequest) ([]string, []string) {
	requestedSkills := request.Skills
	if !request.RestrictSkills && len(requestedSkills) == 0 && len(request.Tools) > 0 {
		requestedSkills = request.Tools
	}
	requestedTools := request.Tools
	if !request.RestrictTools && len(requestedTools) == 0 && len(request.Skills) > 0 {
		requestedTools = request.Skills
	}
	return requestedSkills, requestedTools
}

// routeForAgent pins routing to one installed agent principal (an Office stage
// role, an AgentTeams profile, or any explicitly addressed principal). Unlike
// pipeline selection it never inspects the prompt, so the addressed agent is
// the identity, not a guess. Unknown agents, non-llm agents, and agents whose
// allowlist does not cover the requested capabilities all fail closed.
func (r *Registry) routeForAgent(
	request protocol.ReplyRequest,
	pipeline Pipeline,
	requestedSkills []string,
	requestedTools []string,
	requiredSkills []string,
	requiredTools []string,
) (protocol.ReplyRequest, Plan, error) {
	agent, ok := r.Get(request.Agent)
	if !ok {
		return request, Plan{}, fmt.Errorf("agent %q is not installed", request.Agent)
	}
	if agent.ExecutionMode != "llm" {
		return request, Plan{}, fmt.Errorf("agent %q cannot execute llm tasks", request.Agent)
	}
	skills, err := constrain(
		requestedSkills,
		agent.AllowedSkills,
		"skill",
		agent.Name,
		request.RestrictSkills,
	)
	if err != nil {
		return request, Plan{}, err
	}
	tools, err := constrain(
		requestedTools,
		agent.AllowedTools,
		"tool",
		agent.Name,
		request.RestrictTools,
	)
	if err != nil {
		return request, Plan{}, err
	}
	request.Skills = skills
	request.Tools = tools
	if denied := capabilitiesOutside(requiredSkills, skills); len(denied) > 0 {
		return request, Plan{}, fmt.Errorf("required skills are outside the agent policy: %s", strings.Join(denied, ", "))
	}
	if denied := capabilitiesOutside(requiredTools, tools); len(denied) > 0 {
		return request, Plan{}, fmt.Errorf("required tools are outside the agent policy: %s", strings.Join(denied, ", "))
	}
	if request.Model == "" {
		request.Model = agent.Model
	}
	objective := request.Prompt
	if objective == "" && len(request.Messages) > 0 {
		objective = request.Messages[len(request.Messages)-1].Content
	}
	return request, Plan{
		Pipeline: pipeline.Name,
		Planner:  pipeline.Planner,
		Strategy: "ecs-agent-direct",
		Tasks: []Task{{
			ID:             "task-1",
			Agent:          agent.Name,
			Objective:      objective,
			AllowedSkills:  append([]string(nil), skills...),
			AllowedTools:   append([]string(nil), tools...),
			RequiredSkills: append([]string(nil), requiredSkills...),
			RequiredTools:  append([]string(nil), requiredTools...),
		}},
		Verifier: pipeline.Verifier,
	}, nil
}

func routingPrompt(request protocol.ReplyRequest) string {
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

func capabilitiesOutside(required, allowed []string) []string {
	var outside []string
	for _, name := range required {
		if !contains(allowed, name) {
			outside = append(outside, name)
		}
	}
	return outside
}

// explicitCapabilitiesForPrompt resolves exact Manifest identifiers before the
// request reaches the model. New Skill and Tool names are covered automatically
// through the executor allowlists, so routing does not grow a keyword table.
func (r *Registry) explicitCapabilitiesForPrompt(
	pipeline Pipeline,
	prompt string,
) ([]string, []string) {
	identifiers := make(map[string]bool)
	for _, identifier := range allCapabilityIdentifiers(strings.ToLower(prompt)) {
		identifiers[identifier] = true
	}
	if len(identifiers) == 0 {
		return nil, nil
	}
	skillSet := make(map[string]bool)
	toolSet := make(map[string]bool)
	for _, executorName := range pipeline.Executors {
		executor, ok := r.Get(executorName)
		if !ok {
			continue
		}
		for _, name := range executor.AllowedSkills {
			if identifiers[strings.ToLower(name)] {
				skillSet[name] = true
			}
		}
		for _, name := range executor.AllowedTools {
			if identifiers[strings.ToLower(name)] {
				toolSet[name] = true
			}
		}
	}
	return sortedSet(skillSet), sortedSet(toolSet)
}

func sortedSet(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func requiredSkillForPrompt(prompt string) string {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return ""
	}
	capabilityPhrases := []string{
		"有什么工具",
		"有哪些工具",
		"工具可以用",
		"可用的工具",
		"有什么能力",
		"有哪些能力",
		"能力可以用",
		"可用的能力",
		"有什么skill",
		"有哪些skill",
		"可用的skill",
		"能做什么",
		"会做什么",
		"what tools",
		"available tools",
		"what skills",
		"available skills",
		"capabilities",
	}
	for _, phrase := range capabilityPhrases {
		if strings.Contains(normalized, phrase) {
			return "capabilities"
		}
	}
	return ""
}

func (r *Registry) selectExecutor(
	pipeline Pipeline,
	requestedSkills []string,
	requestedTools []string,
) (Manifest, error) {
	if len(requestedSkills) == 0 && len(requestedTools) == 0 {
		name := pipeline.Fallback
		if name == "" {
			name = pipeline.Executors[0]
		}
		candidate, ok := r.Get(name)
		if !ok {
			return Manifest{}, fmt.Errorf("fallback executor %q is not loaded", name)
		}
		return candidate, nil
	}
	var selected Manifest
	bestScore := int(^uint(0) >> 1)
	for _, name := range pipeline.Executors {
		candidate, ok := r.Get(name)
		if !ok {
			continue
		}
		if !containsAll(candidate.AllowedSkills, requestedSkills) ||
			!containsAll(candidate.AllowedTools, requestedTools) {
			continue
		}
		score := len(candidate.AllowedSkills) + len(candidate.AllowedTools)
		if selected.Name == "" || score < bestScore {
			selected = candidate
			bestScore = score
		}
	}
	if selected.Name == "" {
		return Manifest{}, fmt.Errorf(
			"pipeline %q has no executor covering requested skills %v and tools %v",
			pipeline.Name,
			requestedSkills,
			requestedTools,
		)
	}
	return selected, nil
}

func (r *Registry) Verify(plan Plan, response protocol.ReplyResponse) Verification {
	return r.VerifyWithRequest(plan, protocol.ReplyRequest{}, response)
}

func (r *Registry) VerifyWithRequest(
	plan Plan,
	request protocol.ReplyRequest,
	response protocol.ReplyResponse,
) Verification {
	verifier, ok := r.Get(plan.Verifier)
	result := Verification{
		Status:     "accepted",
		Validation: "accepted",
	}
	if !ok || plan.Verifier == "" {
		result.Validation = "skipped"
		return result
	}
	result.Verifier = verifier.Name
	if strings.TrimSpace(response.Message.Content) == "" {
		result.Errors = append(result.Errors, "assistant result is empty")
	}
	if request.ExpectedText != "" {
		if response.Message.Content != request.ExpectedText {
			result.Errors = append(result.Errors, fmt.Sprintf(
				"literal probe mismatch: expected %q, got %q",
				request.ExpectedText,
				response.Message.Content,
			))
		} else {
			result.Evidence = append(result.Evidence, "literal_probe:exact_match")
		}
	}
	if strings.Contains(response.Message.Content, "<tool_call") ||
		strings.Contains(response.Message.Content, "</tool_call>") {
		result.Errors = append(result.Errors, "final answer leaks a raw tool_call")
	}
	for _, run := range response.SkillRuns {
		if run.Error != "" || (run.Status != "success" && run.Status != "degraded") {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %s failed: %s", run.Name, run.Error))
		} else {
			result.Evidence = append(result.Evidence, "skill:"+run.Name+"="+run.Status)
		}
	}
	for _, run := range response.ToolRuns {
		if run.Error != "" {
			result.Errors = append(result.Errors, fmt.Sprintf("tool %s failed: %s", run.Name, run.Error))
		} else {
			result.Evidence = append(result.Evidence, "tool:"+run.Name+"=success")
			if contains(verifier.AllowedTools, run.Name) &&
				!outputEvidenceAppears(response.Message.Content, run.Output) {
				result.Errors = append(
					result.Errors,
					fmt.Sprintf("tool %s result is not reflected in the final answer", run.Name),
				)
			} else if contains(verifier.AllowedTools, run.Name) {
				result.Evidence = append(result.Evidence, "tool:"+run.Name+"=verified")
			}
		}
	}
	for _, task := range plan.Tasks {
		for _, required := range task.RequiredSkills {
			if !successfulSkillRun(response.SkillRuns, required) {
				result.Errors = append(result.Errors, fmt.Sprintf("required skill %s was not executed successfully", required))
			}
		}
		for _, required := range task.RequiredTools {
			if !successfulToolRun(response.ToolRuns, required) {
				result.Errors = append(result.Errors, fmt.Sprintf("required tool %s was not executed successfully", required))
			}
		}
	}
	for _, contradiction := range capabilityContradictions(plan, request.Messages, response.Message.Content) {
		result.Errors = append(result.Errors, contradiction)
	}
	if len(result.Errors) > 0 {
		result.Status = "rejected"
		result.Validation = "rejected"
		result.RetryRecommended = verifier.FailurePolicy.OnFailure == "request_retry" &&
			verifier.FailurePolicy.MaxRetries > 0
	}
	return result
}

func capabilityContradictions(
	plan Plan,
	history []protocol.Message,
	content string,
) []string {
	lowerContent := strings.ToLower(content)
	known := make(map[string]bool)
	for _, task := range plan.Tasks {
		for _, name := range task.AllowedTools {
			known[name] = true
		}
		for _, name := range task.AllowedSkills {
			known[name] = true
		}
		if isCapabilityDescriptionQuestion(task.Objective) {
			for _, name := range allCapabilityIdentifiers(task.Objective) {
				known[name] = true
			}
		}
	}
	for _, message := range history {
		if message.Role != "assistant" {
			continue
		}
		lowerHistory := strings.ToLower(message.Content)
		for _, name := range capabilityIdentifiers(message.Content) {
			if strings.Contains(lowerHistory, strings.ToLower(name)) {
				known[name] = true
			}
		}
	}
	var errorsFound []string
	for name := range known {
		if deniesCapability(lowerContent, strings.ToLower(name)) {
			errorsFound = append(
				errorsFound,
				fmt.Sprintf("assistant denies capability %s that the current policy or conversation already established", name),
			)
		}
	}
	sort.Strings(errorsFound)
	return errorsFound
}

func deniesCapability(content string, name string) bool {
	quoted := "`" + name + "`"
	for _, pattern := range []string{
		"没有 " + name,
		"没有" + name,
		"没有 " + quoted,
		"没有" + quoted,
		"不存在 " + name,
		"不存在" + name,
		"不存在 " + quoted,
		name + " 不存在",
		quoted + " 不存在",
		"未注册 " + name,
		"未注册" + name,
		"未注册 " + quoted,
		name + " 未注册",
		quoted + " 未注册",
		name + " is not registered",
		quoted + " is not registered",
		name + " does not exist",
		quoted + " does not exist",
	} {
		if strings.Contains(content, pattern) {
			return true
		}
	}
	return false
}

func capabilityIdentifiers(value string) []string {
	var result []string
	for _, field := range allCapabilityIdentifiers(value) {
		if strings.ContainsAny(field, "_.-") {
			result = append(result, field)
		}
	}
	return result
}

func allCapabilityIdentifiers(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			r != '_' && r != '.' && r != '-'
	})
	var result []string
	for _, field := range fields {
		field = strings.Trim(field, "`")
		if len(field) >= 2 && validCapabilityIdentifier(field) {
			result = append(result, field)
		}
	}
	return result
}

func isCapabilityDescriptionQuestion(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"有什么用", "做什么", "作用", "介绍一下", "是什么",
		"what does", "what is", "describe",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func validCapabilityIdentifier(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func successfulSkillRun(runs []protocol.SkillRun, name string) bool {
	for _, run := range runs {
		if run.Name == name && (run.Status == "success" || run.Status == "degraded") && run.Error == "" {
			return true
		}
	}
	return false
}

func successfulToolRun(runs []protocol.ToolRun, name string) bool {
	for _, run := range runs {
		if run.Name == name && run.Error == "" {
			return true
		}
	}
	return false
}

func constrain(
	requested []string,
	allowed []string,
	kind string,
	agent string,
	restricted bool,
) ([]string, error) {
	if len(requested) == 0 {
		if restricted {
			return []string{}, nil
		}
		return append([]string(nil), allowed...), nil
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	result := make([]string, 0, len(requested))
	for _, name := range requested {
		if !allowedSet[name] {
			return nil, fmt.Errorf("requested %s %q is not allowed for agent %q", kind, name, agent)
		}
		result = append(result, name)
	}
	return result, nil
}

func containsAll(available []string, requested []string) bool {
	for _, name := range requested {
		if !contains(available, name) {
			return false
		}
	}
	return true
}

func outputEvidenceAppears(content string, output string) bool {
	if strings.TrimSpace(output) == "" {
		return false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader([]byte(output)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return strings.Contains(content, strings.TrimSpace(output))
	}
	values := scalarValues(value)
	if len(values) == 0 {
		compact := new(bytes.Buffer)
		if err := json.Compact(compact, []byte(output)); err != nil {
			return false
		}
		return strings.Contains(content, compact.String())
	}
	for _, item := range values {
		if item != "" && !strings.Contains(content, item) {
			return false
		}
	}
	return true
}

func scalarValues(value any) []string {
	switch item := value.(type) {
	case map[string]any:
		if len(item) == 0 {
			return []string{"{}"}
		}
		var result []string
		keys := make([]string, 0, len(item))
		for key := range item {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			result = append(result, scalarValues(item[key])...)
		}
		return result
	case []any:
		if len(item) == 0 {
			return []string{"[]"}
		}
		var result []string
		for _, child := range item {
			result = append(result, scalarValues(child)...)
		}
		return result
	case string:
		return []string{item}
	case json.Number:
		return []string{item.String()}
	case float64:
		return []string{strconv.FormatFloat(item, 'f', -1, 64)}
	case bool:
		return []string{fmt.Sprint(item)}
	case nil:
		return []string{"null"}
	default:
		return nil
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
