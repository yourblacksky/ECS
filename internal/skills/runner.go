package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"ecs/internal/protocol"
	"ecs/internal/tools"
)

type Runner struct {
	registry *Registry
	tools    *tools.Registry
}

func NewRunner(registry *Registry, toolRegistry *tools.Registry) *Runner {
	return &Runner{registry: registry, tools: toolRegistry}
}

func (r *Runner) Execute(ctx context.Context, name string, input json.RawMessage) (protocol.SkillRun, error) {
	started := time.Now()
	run := protocol.SkillRun{Name: name, Input: append(json.RawMessage(nil), input...), Status: "failed"}
	manifest, ok := r.registry.Get(name)
	if !ok {
		err := fmt.Errorf("skill %q is not installed or not allowed", name)
		run.Error = err.Error()
		run.Duration = time.Since(started).Milliseconds()
		return run, err
	}
	run.Version = manifest.Version
	if err := tools.ValidateArguments(input, manifest.RuntimeInputSchema()); err != nil {
		err = fmt.Errorf("invalid input for skill %q: %w", name, err)
		run.Error = err.Error()
		run.Duration = time.Since(started).Milliseconds()
		return run, err
	}
	if manifest.Failure.Timeout.Duration() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, manifest.Failure.Timeout.Duration())
		defer cancel()
	}
	current := append(json.RawMessage(nil), input...)
	var output string
	for _, step := range manifest.Workflow {
		stepInput := current
		if step.Input == "$input" || step.Input == "" {
			stepInput = input
		}
		stepOutput, err := r.executeStep(ctx, manifest, step, stepInput, &run)
		if err != nil {
			switch step.OnFailure {
			case "continue":
				continue
			case "return_input":
				output = string(input)
				run.Output = output
				run.Status = "degraded"
				run.Duration = time.Since(started).Milliseconds()
				return run, nil
			default:
				if manifest.Compensation.Required {
					r.compensate(ctx, manifest, input, current, &run)
				}
				run.Error = err.Error()
				run.Duration = time.Since(started).Milliseconds()
				return run, errors.New(err.Error())
			}
		}
		output = stepOutput
		if json.Valid([]byte(stepOutput)) {
			current = json.RawMessage(stepOutput)
		} else {
			encoded, _ := json.Marshal(map[string]string{"output": stepOutput})
			current = encoded
		}
		if step.OnSuccess == "return" {
			break
		}
	}
	if manifest.OutputSchema != nil && json.Valid([]byte(output)) {
		if err := tools.ValidateArguments(json.RawMessage(output), manifest.OutputSchema); err != nil {
			run.Error = "skill output validation failed: " + err.Error()
			run.Duration = time.Since(started).Milliseconds()
			return run, errors.New(run.Error)
		}
	}
	run.Output = output
	run.Status = "success"
	run.Duration = time.Since(started).Milliseconds()
	return run, nil
}

func (r *Runner) executeStep(
	ctx context.Context,
	manifest Manifest,
	step Step,
	input json.RawMessage,
	run *protocol.SkillRun,
) (string, error) {
	attempts := manifest.Failure.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var output string
	var finalErr error
	for attempt := 0; attempt < attempts; attempt++ {
		started := time.Now()
		output, finalErr = r.tools.Execute(ctx, step.Tool, input)
		toolRun := protocol.ToolRun{
			Name:      step.Tool,
			Arguments: append(json.RawMessage(nil), input...),
			Output:    output,
			Duration:  time.Since(started).Milliseconds(),
		}
		if finalErr != nil {
			toolRun.Error = finalErr.Error()
		}
		run.ToolRuns = append(run.ToolRuns, toolRun)
		if finalErr == nil || ctx.Err() != nil {
			break
		}
	}
	return output, finalErr
}

func (r *Runner) compensate(
	ctx context.Context,
	manifest Manifest,
	input json.RawMessage,
	previous json.RawMessage,
	run *protocol.SkillRun,
) {
	for index := len(manifest.Compensation.Steps) - 1; index >= 0; index-- {
		step := manifest.Compensation.Steps[index]
		stepInput := previous
		if step.Input == "" || step.Input == "$input" {
			stepInput = input
		}
		started := time.Now()
		output, err := r.tools.Execute(ctx, step.Tool, stepInput)
		toolRun := protocol.ToolRun{
			Name:      step.Tool,
			Arguments: append(json.RawMessage(nil), stepInput...),
			Output:    output,
			Duration:  time.Since(started).Milliseconds(),
		}
		if err != nil {
			toolRun.Error = "compensation: " + err.Error()
		}
		run.ToolRuns = append(run.ToolRuns, toolRun)
	}
}
