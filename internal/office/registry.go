package office

import (
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
	"time"

	"ecs/internal/config"
	"ecs/internal/roles"
	"ecs/internal/skills"
	"ecs/internal/tools"
)

var validID = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,63}$`)

var (
	ErrApprovalRejected       = errors.New("Office task approval was rejected")
	ErrApprovalExpired        = errors.New("Office task approval expired")
	ErrApprovalAlreadyDecided = errors.New("Office task approval was already decided")
)

type Registry struct {
	cfg     config.OfficeConfig
	tools   *tools.Registry
	skills  *skills.Registry
	agents  *roles.Registry
	log     *slog.Logger
	mu      sync.RWMutex
	orderMu sync.Mutex
	items   map[string]Manifest
}

func NewRegistry(cfg config.OfficeConfig, toolRegistry *tools.Registry, logger *slog.Logger) *Registry {
	return &Registry{cfg: cfg, tools: toolRegistry, log: logger, items: make(map[string]Manifest)}
}

func (r *Registry) SetSkillRegistry(skillRegistry *skills.Registry) {
	r.mu.Lock()
	r.skills = skillRegistry
	r.mu.Unlock()
}

// SetAgentRegistry binds Office team identities to executable Agent
// principals. Keeping this dependency explicit lets lightweight registry tests
// omit role validation while every real Runtime validates it at load time.
func (r *Registry) SetAgentRegistry(agentRegistry *roles.Registry) {
	r.mu.Lock()
	r.agents = agentRegistry
	r.mu.Unlock()
}

func (r *Registry) Reload() error {
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
			return fmt.Errorf("read office component %s: %w", path, err)
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return fmt.Errorf("decode office component %s: %w", path, err)
		}
		if err := validateManifest(manifest, r.cfg.MaxStages); err != nil {
			return fmt.Errorf("invalid office component %s: %w", path, err)
		}
		if err := r.validateExecutionContracts(manifest); err != nil {
			return fmt.Errorf("invalid office component %s: %w", path, err)
		}
		if _, exists := next[manifest.Name]; exists {
			return fmt.Errorf("duplicate office component %q", manifest.Name)
		}
		next[manifest.Name] = manifest
	}
	r.mu.Lock()
	r.items = next
	r.mu.Unlock()
	if err := os.MkdirAll(r.cfg.OrderDirectory, 0o700); err != nil {
		return err
	}
	r.log.Info("office component registry loaded", "count", len(next), "directory", r.cfg.Directory)
	return nil
}

func (r *Registry) All() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Manifest, 0, len(r.items))
	for _, item := range r.items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (r *Registry) Get(name string) (Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[name]
	return item, ok
}

func (r *Registry) Capabilities() []Capability {
	provided := make(map[string]bool)
	if r.tools != nil {
		for _, capability := range r.tools.ProvidedCapabilities() {
			provided[capability] = true
		}
	}
	manifests := r.All()
	result := make([]Capability, 0, len(manifests))
	for _, manifest := range manifests {
		required, missing := connectorState(manifest, provided)
		agents := make([]string, 0, len(manifest.Team))
		for _, identity := range manifest.Team {
			agents = append(agents, identity.Name)
		}
		result = append(result, Capability{
			Name: manifest.Name, Version: manifest.Version, Description: manifest.Description,
			Intents: append([]string(nil), manifest.Intents...), Agents: agents, Stages: len(manifest.Stages),
			InputSchema:        manifest.InputSchema,
			RequiredConnectors: required, MissingConnectors: missing, Ready: len(missing) == 0,
		})
	}
	return result
}

func (r *Registry) Compile(name string, request CompileRequest) (WorkOrder, error) {
	manifest, ok := r.Get(name)
	if !ok {
		return WorkOrder{}, os.ErrNotExist
	}
	if len(request.Context) > r.cfg.MaxContextEntries {
		return WorkOrder{}, fmt.Errorf("context has %d entries; maximum is %d", len(request.Context), r.cfg.MaxContextEntries)
	}
	if len(request.Input) == 0 {
		request.Input = json.RawMessage(`{}`)
	}
	if err := tools.ValidateArguments(request.Input, manifest.InputSchema); err != nil {
		return WorkOrder{}, err
	}
	orderID, err := randomID("office_")
	if err != nil {
		return WorkOrder{}, err
	}
	agentRoles := make(map[string]string, len(manifest.Team))
	for _, identity := range manifest.Team {
		agentRoles[identity.Name] = identity.Role
	}
	sortedStages, err := topologicalStages(manifest.Stages)
	if err != nil {
		return WorkOrder{}, err
	}
	tasks := make([]Task, 0, len(sortedStages))
	taskIDs := make(map[string]string, len(sortedStages))
	for _, stage := range sortedStages {
		taskIDs[stage.ID] = orderID + "." + stage.ID
	}
	for _, stage := range sortedStages {
		dependencies := make([]string, 0, len(stage.DependsOn))
		for _, dependency := range stage.DependsOn {
			dependencies = append(dependencies, taskIDs[dependency])
		}
		tasks = append(tasks, Task{
			ID: taskIDs[stage.ID], StageID: stage.ID, Name: stage.Name, Agent: stage.Agent, AgentRole: agentRoles[stage.Agent],
			Purpose: stage.Purpose, Status: "pending", DependsOn: dependencies,
			Skills:     append([]string(nil), stage.Skills...),
			Connectors: append([]string(nil), stage.Connectors...), Consumes: append([]string(nil), stage.Consumes...),
			Produces: stage.Produces, AcceptanceCriteria: append([]string(nil), stage.AcceptanceCriteria...),
			Approval: stage.Approval, OnFailure: normalizedFailure(stage.OnFailure), Compensation: stage.Compensation,
		})
	}
	handoffs := buildHandoffs(sortedStages, taskIDs, request.Context)
	provided := make(map[string]bool)
	if r.tools != nil {
		for _, capability := range r.tools.ProvidedCapabilities() {
			provided[capability] = true
		}
	}
	required, missing := connectorState(manifest, provided)
	now := time.Now().UTC()
	order := WorkOrder{
		SchemaVersion: OrderSchema, ID: orderID, Component: manifest.Name, ComponentVersion: manifest.Version,
		Status: "planned", TraceID: strings.TrimSpace(request.TraceID), CreatedAt: now,
		RequestedBy: strings.TrimSpace(request.RequestedBy), Input: append(json.RawMessage(nil), request.Input...),
		OwnerPeerNodeID: normalizedOwner(request.OwnerPeerNodeID),
		Context:         cloneContext(request.Context), Tasks: tasks, Handoffs: handoffs,
		RequiredConnectors: required, MissingConnectors: missing, Ready: len(missing) == 0,
		Governance: manifest.Governance,
		Audit: []AuditEvent{{At: now, Action: "work_order.planned", Actor: strings.TrimSpace(request.RequestedBy),
			Details: map[string]any{"tasks": len(tasks), "handoffs": len(handoffs), "ready": len(missing) == 0}}},
	}
	if err := r.saveOrder(order); err != nil {
		return WorkOrder{}, err
	}
	return order, nil
}

func (r *Registry) LoadOrder(id string) (WorkOrder, error) {
	r.orderMu.Lock()
	defer r.orderMu.Unlock()
	return r.loadOrder(id)
}

func (r *Registry) loadOrder(id string) (WorkOrder, error) {
	if !validID.MatchString(id) {
		return WorkOrder{}, errors.New("invalid work order id")
	}
	data, err := os.ReadFile(filepath.Join(r.cfg.OrderDirectory, id+".json"))
	if err != nil {
		return WorkOrder{}, err
	}
	var order WorkOrder
	if err := json.Unmarshal(data, &order); err != nil {
		return WorkOrder{}, err
	}
	return order, nil
}

// UpdateOrder serializes state transitions and persists each transition
// atomically so the admin view never observes a partial write.
func (r *Registry) UpdateOrder(id string, update func(*WorkOrder) error) (WorkOrder, error) {
	r.orderMu.Lock()
	defer r.orderMu.Unlock()
	order, err := r.loadOrder(id)
	if err != nil {
		return WorkOrder{}, err
	}
	if err := update(&order); err != nil {
		return WorkOrder{}, err
	}
	if err := r.saveOrder(order); err != nil {
		return WorkOrder{}, err
	}
	return order, nil
}
