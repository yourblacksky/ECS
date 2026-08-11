package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"ecs/internal/config"
	"ecs/internal/platform"
	ecsschema "ecs/internal/schema"
)

var validName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,63}$`)

type Manifest struct {
	Name             string            `json:"name"`
	Version          string            `json:"version,omitempty"`
	Description      string            `json:"description"`
	Capabilities     []string          `json:"capabilities,omitempty"`
	Platforms        []string          `json:"platforms,omitempty"`
	Command          string            `json:"command"`
	Args             []string          `json:"args,omitempty"`
	Timeout          config.Duration   `json:"timeout,omitempty"`
	Enabled          *bool             `json:"enabled,omitempty"`
	Risk             string            `json:"risk,omitempty"`
	RequiresApproval bool              `json:"requires_approval,omitempty"`
	InputSchema      map[string]any    `json:"input_schema"`
	CanonicalSchema  map[string]any    `json:"canonical_input_schema,omitempty"`
	InputAdapter     ecsschema.Adapter `json:"input_adapter,omitempty"`
	OutputSchema     map[string]any    `json:"output_schema,omitempty"`
}

func (m Manifest) RuntimeInputSchema() map[string]any {
	if m.CanonicalSchema != nil {
		return m.CanonicalSchema
	}
	return m.InputSchema
}

type BuiltinHandler func(context.Context, json.RawMessage) (string, error)
type BuiltinAvailability func() platform.ToolAvailability

type Registry struct {
	cfg                 config.ToolsConfig
	logger              *slog.Logger
	mu                  sync.RWMutex
	tools               map[string]Manifest
	builtins            map[string]BuiltinHandler
	builtinAvailability map[string]BuiltinAvailability
	executor            platform.ToolExecutor
	platform            string
}

type ArgumentValidationError struct {
	Err error
}

func (e *ArgumentValidationError) Error() string { return e.Err.Error() }
func (e *ArgumentValidationError) Unwrap() error { return e.Err }

func IsArgumentValidationError(err error) bool {
	var target *ArgumentValidationError
	return errors.As(err, &target)
}

func NewRegistry(cfg config.ToolsConfig, logger *slog.Logger) *Registry {
	return NewRegistryWithExecutor(cfg, logger, platform.ProcessTools{Enabled: true})
}

func NewRegistryWithExecutor(
	cfg config.ToolsConfig,
	logger *slog.Logger,
	executor platform.ToolExecutor,
) *Registry {
	return &Registry{
		cfg:                 cfg,
		logger:              logger,
		tools:               make(map[string]Manifest),
		builtins:            make(map[string]BuiltinHandler),
		builtinAvailability: make(map[string]BuiltinAvailability),
		executor:            executor,
	}
}

// SetPlatform limits manifests with an explicit platforms list to this host.
// Manifests without a platform restriction remain portable.
func (r *Registry) SetPlatform(name string) {
	r.mu.Lock()
	r.platform = strings.ToLower(strings.TrimSpace(name))
	r.mu.Unlock()
}

func (r *Registry) Reload() error {
	r.mu.RLock()
	currentPlatform := r.platform
	r.mu.RUnlock()
	entries, err := os.ReadDir(r.cfg.Directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.MkdirAll(r.cfg.Directory, 0o755)
		}
		return err
	}
	next := make(map[string]Manifest)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(r.cfg.Directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read tool %s: %w", path, err)
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return fmt.Errorf("decode tool %s: %w", path, err)
		}
		if err := validate(manifest); err != nil {
			return fmt.Errorf("invalid tool %s: %w", path, err)
		}
		if manifest.Version == "" {
			manifest.Version = "1.0.0"
		}
		if manifest.Enabled != nil && !*manifest.Enabled {
			continue
		}
		if !supportsPlatform(manifest.Platforms, currentPlatform) {
			continue
		}
		if !filepath.IsAbs(manifest.Command) && strings.ContainsRune(manifest.Command, filepath.Separator) {
			manifest.Command = filepath.Clean(filepath.Join(filepath.Dir(path), manifest.Command))
		}
		if manifest.Command == "@ecs" {
			executable, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve ECS executable for tool %q: %w", manifest.Name, err)
			}
			manifest.Command = executable
		}
		if _, exists := next[manifest.Name]; exists {
			return fmt.Errorf("duplicate tool name %q", manifest.Name)
		}
		next[manifest.Name] = manifest
	}
	r.mu.Lock()
	r.tools = next
	r.mu.Unlock()
	r.logger.Info("tool registry loaded", "count", len(next), "directory", r.cfg.Directory)
	return nil
}

func supportsPlatform(platforms []string, current string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, name := range platforms {
		if strings.EqualFold(strings.TrimSpace(name), current) {
			return true
		}
	}
	return false
}

func validate(m Manifest) error {
	if !validName.MatchString(m.Name) {
		return errors.New("name must match [a-zA-Z][a-zA-Z0-9_.-]{0,63}")
	}
	if strings.TrimSpace(m.Description) == "" || strings.TrimSpace(m.Command) == "" {
		return errors.New("description and command are required")
	}
	if m.InputSchema == nil {
		return errors.New("input_schema is required")
	}
	if err := ecsschema.ValidateDefinition(m.InputSchema, true); err != nil {
		return fmt.Errorf("invalid input_schema: %w", err)
	}
	if m.CanonicalSchema != nil {
		if err := ecsschema.ValidateDefinition(m.CanonicalSchema, true); err != nil {
			return fmt.Errorf("invalid canonical_input_schema: %w", err)
		}
	}
	if m.OutputSchema != nil {
		if err := ecsschema.ValidateDefinition(m.OutputSchema, false); err != nil {
			return fmt.Errorf("invalid output_schema: %w", err)
		}
	}
	if err := ecsschema.ValidateAdapter(m.InputAdapter, m.InputSchema, m.CanonicalSchema); err != nil {
		return err
	}
	seenCapabilities := make(map[string]bool, len(m.Capabilities))
	for _, capability := range m.Capabilities {
		if !validName.MatchString(capability) || seenCapabilities[capability] {
			return fmt.Errorf("invalid or duplicate capability %q", capability)
		}
		seenCapabilities[capability] = true
	}
	switch m.Risk {
	case "", "low", "medium", "high":
	default:
		return errors.New("risk must be low, medium, or high")
	}
	return nil
}

func ValidateManifest(manifest Manifest) error {
	return validate(manifest)
}

func (r *Registry) List(allow []string, limit int) []Manifest {
	allowed := make(map[string]bool, len(allow))
	for _, name := range allow {
		allowed[name] = true
	}
	installed := r.All()
	result := make([]Manifest, 0, len(installed))
	for _, tool := range installed {
		name := tool.Name
		if len(allowed) > 0 && !allowed[name] {
			continue
		}
		if !r.Availability(name).Available {
			continue
		}
		result = append(result, tool)
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func (r *Registry) All() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Manifest, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (r *Registry) Available() []Manifest {
	installed := r.All()
	result := make([]Manifest, 0, len(installed))
	for _, manifest := range installed {
		if r.Availability(manifest.Name).Available {
			result = append(result, manifest)
		}
	}
	return result
}

// ProvidedCapabilities returns business-facing capabilities exposed by tools
// that are currently executable on this node.
func (r *Registry) ProvidedCapabilities() []string {
	seen := make(map[string]struct{})
	for _, manifest := range r.Available() {
		for _, capability := range manifest.Capabilities {
			capability = strings.TrimSpace(capability)
			if capability != "" {
				seen[capability] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for capability := range seen {
		result = append(result, capability)
	}
	sort.Strings(result)
	return result
}

func (r *Registry) RegisterBuiltin(name string, handler BuiltinHandler) error {
	return r.RegisterBuiltinWithAvailability(name, handler, nil)
}

// RegisterBuiltinWithAvailability lets configured connectors distinguish an
// installed manifest from a handler that is ready with usable profiles and
// credentials.
func (r *Registry) RegisterBuiltinWithAvailability(
	name string,
	handler BuiltinHandler,
	availability BuiltinAvailability,
) error {
	if handler == nil {
		return errors.New("built-in tool handler is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, ok := r.tools[name]
	if !ok {
		return fmt.Errorf("built-in tool %q is not installed", name)
	}
	if manifest.Command != "@builtin" {
		return fmt.Errorf("tool %q does not declare command @builtin", name)
	}
	r.builtins[name] = handler
	if availability == nil {
		delete(r.builtinAvailability, name)
	} else {
		r.builtinAvailability[name] = availability
	}
	return nil
}

func (r *Registry) Execute(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	builtin := r.builtins[name]
	builtinAvailability := r.builtinAvailability[name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("tool %q is not installed or not allowed", name)
	}
	if availability := r.availability(tool, builtin, builtinAvailability); !availability.Available {
		return "", fmt.Errorf("tool %q is unavailable: %s", name, availability.Reason)
	}
	if err := ValidateArguments(arguments, tool.RuntimeInputSchema()); err != nil {
		return "", fmt.Errorf("invalid arguments for tool %q: %w", name, err)
	}
	timeout := tool.Timeout.Duration()
	if timeout <= 0 {
		timeout = r.cfg.DefaultTimeout.Duration()
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if tool.Command == "@builtin" {
		if builtin == nil {
			return "", fmt.Errorf("built-in tool %q has no registered handler", name)
		}
		result, err := builtin(runCtx, arguments)
		if runCtx.Err() != nil {
			return result, fmt.Errorf("tool timed out after %s", timeout)
		}
		if err != nil {
			return result, err
		}
		return validateOutput(result, tool.OutputSchema)
	}

	if r.executor == nil {
		return "", errors.New("tool subprocess executor is not configured")
	}
	result, err := r.executor.Execute(runCtx, platform.ToolRequest{
		Command:        tool.Command,
		Args:           tool.Args,
		Input:          arguments,
		MaxOutputBytes: r.cfg.MaxOutputBytes,
	})
	if runCtx.Err() != nil {
		return result.Stdout, fmt.Errorf("tool timed out after %s", timeout)
	}
	if err != nil {
		return result.Stdout, fmt.Errorf("%w: %s", err, strings.TrimSpace(result.Stderr))
	}
	return validateOutput(strings.TrimSpace(result.Stdout), tool.OutputSchema)
}

func (r *Registry) Get(name string) (Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	manifest, ok := r.tools[name]
	return manifest, ok
}

// Availability evaluates the current executor state rather than only the
// manifest. This is used by routing and is rechecked by Execute.
func (r *Registry) Availability(name string) platform.ToolAvailability {
	r.mu.RLock()
	tool, ok := r.tools[name]
	builtin := r.builtins[name]
	builtinAvailability := r.builtinAvailability[name]
	r.mu.RUnlock()
	if !ok {
		return platform.ToolAvailability{Reason: "not_installed"}
	}
	return r.availability(tool, builtin, builtinAvailability)
}

func (r *Registry) availability(
	tool Manifest,
	builtin BuiltinHandler,
	builtinAvailability BuiltinAvailability,
) platform.ToolAvailability {
	if tool.Command == "@builtin" {
		if builtin == nil {
			return platform.ToolAvailability{Reason: "handler_unavailable"}
		}
		if builtinAvailability != nil {
			availability := builtinAvailability()
			if !availability.Available && strings.TrimSpace(availability.Reason) == "" {
				availability.Reason = "connector_unconfigured"
			}
			return availability
		}
		return platform.ToolAvailability{Available: true}
	}
	if r.executor == nil {
		return platform.ToolAvailability{Reason: "executor_unavailable"}
	}
	if provider, ok := r.executor.(platform.ToolAvailabilityProvider); ok {
		availability := provider.ToolAvailability(platform.ToolRequest{
			Command: tool.Command,
			Args:    append([]string(nil), tool.Args...),
		})
		if !availability.Available && strings.TrimSpace(availability.Reason) == "" {
			availability.Reason = "executor_unavailable"
		}
		return availability
	}
	return platform.ToolAvailability{Available: true}
}

func validateOutput(result string, schema map[string]any) (string, error) {
	result = strings.TrimSpace(result)
	if schema == nil {
		return result, nil
	}
	if !json.Valid([]byte(result)) {
		return result, errors.New("tool output must be valid JSON")
	}
	if err := validateArguments(json.RawMessage(result), schema); err != nil {
		return result, fmt.Errorf("invalid tool output: %w", err)
	}
	return result, nil
}

func validateArguments(raw json.RawMessage, schema map[string]any) error {
	if err := ecsschema.Validate(raw, schema); err != nil {
		return fmt.Errorf("arguments do not match schema: %w", err)
	}
	return nil
}

// ValidateArguments validates JSON data against JSON Schema Draft 2020-12.
func ValidateArguments(raw json.RawMessage, schema map[string]any) error {
	if err := validateArguments(raw, schema); err != nil {
		return &ArgumentValidationError{Err: err}
	}
	return nil
}
