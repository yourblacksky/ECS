package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"ecs/internal/adminauth"
	"ecs/internal/agent"
	"ecs/internal/agentteams"
	"ecs/internal/config"
	"ecs/internal/discovery"
	"ecs/internal/iot"
	"ecs/internal/networkauth"
	"ecs/internal/observability"
	"ecs/internal/office"
	"ecs/internal/platform"
	"ecs/internal/protocol"
	"ecs/internal/roles"
	"ecs/internal/server"
	"ecs/internal/session"
	"ecs/internal/skills"
	"ecs/internal/tools"
	"ecs/version"
)

type Status struct {
	Version          string                        `json:"version"`
	Node             string                        `json:"node"`
	Address          string                        `json:"address"`
	Running          bool                          `json:"running"`
	NodeID           string                        `json:"node_id,omitempty"`
	Platform         protocol.PlatformCapabilities `json:"platform"`
	Tools            int                           `json:"tools"`
	Skills           int                           `json:"skills"`
	Agents           int                           `json:"agents"`
	Pipelines        int                           `json:"pipelines"`
	OfficeComponents int                           `json:"office_components"`
	ModelReady       bool                          `json:"model_ready"`
}

// Runtime owns one ECS node independently of process signals or a particular
// application lifecycle. CLI, Android, iOS, and service wrappers can all drive
// the same Start/Stop/Reload contract.
type Runtime struct {
	cfg          config.Config
	host         platform.Host
	logger       *slog.Logger
	tools        *tools.Registry
	skills       *skills.Registry
	roles        *roles.Registry
	office       *office.Registry
	iot          *iot.Connector
	agent        *agent.Agent
	discovery    *discovery.Manager
	network      *networkauth.Manager
	matrixWorker *agentteams.MatrixWorker
	httpServer   *http.Server
	handler      http.Handler

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	listener net.Listener
	done     chan error
}

func New(cfg config.Config, host platform.Host) (*Runtime, error) {
	if host == nil {
		host = platform.NewDefaultHost(platform.RuntimePaths{})
	}
	logStore := observability.NewLogStore(cfg.Admin.LogCapacity)
	logger := slog.New(observability.MultiHandler(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel()}),
		logStore,
	))
	toolRegistry := tools.NewRegistryWithExecutor(cfg.Tools, logger, host.Tools())
	toolRegistry.SetPlatform(host.Capabilities().Name)
	if err := toolRegistry.Reload(); err != nil {
		return nil, fmt.Errorf("load tools: %w", err)
	}
	skillRegistry := skills.NewRegistry(cfg.Skills, toolRegistry, logger)
	skillRegistry.SetPlatform(host.Capabilities().Name)
	if err := skillRegistry.Reload(); err != nil {
		return nil, fmt.Errorf("load skills: %w", err)
	}
	iotConnector := iot.New(cfg.IoT, host.Network().HTTPTransport())
	if err := registerBuiltinTools(
		toolRegistry, skillRegistry, iotBuiltinRegistrations(iotConnector),
	); err != nil {
		return nil, fmt.Errorf("register built-in tools: %w", err)
	}
	roleRegistry := roles.NewRegistry(cfg.AgentRoles, logger)
	if err := roleRegistry.Reload(); err != nil {
		return nil, fmt.Errorf("load agent roles and pipelines: %w", err)
	}
	officeRegistry := office.NewRegistry(cfg.Office, toolRegistry, logger)
	officeRegistry.SetSkillRegistry(skillRegistry)
	officeRegistry.SetAgentRegistry(roleRegistry)
	if err := officeRegistry.Reload(); err != nil {
		return nil, fmt.Errorf("load office components: %w", err)
	}
	recovered, err := officeRegistry.ReapExpiredAttempts(time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("recover expired office attempts: %w", err)
	}
	if len(recovered) > 0 {
		logger.Warn("expired Office attempts require recovery", "attempts", len(recovered))
	}
	runner := agent.NewWithRuntimeAndTransport(
		cfg.Agent,
		cfg.LLM,
		toolRegistry,
		skillRegistry,
		roleRegistry,
		logger,
		host.Network().HTTPTransport(),
	)
	sessionStore, err := session.New(cfg.Sessions)
	if err != nil {
		return nil, fmt.Errorf("initialize sessions: %w", err)
	}
	discoveryManager := discovery.NewWithProvider(
		cfg.Discovery,
		cfg.Nested,
		cfg.NodeName,
		cfg.LLM.Model,
		cfg.AuthToken,
		cfg.Agent.ApprovalMode,
		toolRegistry,
		logger,
		host.Discovery(),
	)
	discoveryManager.SetSkillRegistry(skillRegistry)
	discoveryManager.SetAgentRegistry(roleRegistry)
	discoveryManager.SetOfficeRegistry(officeRegistry)
	discoveryManager.SetPlatformCapabilities(host.Capabilities())
	discoveryManager.SetHTTPTransport(host.Network().HTTPTransport())
	networkManager, err := networkauth.New(
		cfg.Network,
		cfg.NodeName,
		cfg.Discovery.AdvertiseURL,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize network authentication: %w", err)
	}
	if networkManager != nil {
		discoveryManager.SetNodeID(networkManager.NodeID())
		networkManager.SetDiscoverySource(discoveryManager.Snapshot)
		logger.Info("network identity loaded", "node_id", networkManager.NodeID())
	}
	handler := server.NewWithOfficeRuntime(
		cfg,
		runner,
		toolRegistry,
		skillRegistry,
		roleRegistry,
		officeRegistry,
		sessionStore,
		discoveryManager,
		logger,
		logStore,
		networkManager,
	)
	matrixWorker, err := agentteams.NewMatrixWorker(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize AgentTeams Matrix worker: %w", err)
	}
	return &Runtime{
		cfg:          cfg,
		host:         host,
		logger:       logger,
		tools:        toolRegistry,
		skills:       skillRegistry,
		roles:        roleRegistry,
		office:       officeRegistry,
		iot:          iotConnector,
		agent:        runner,
		discovery:    discoveryManager,
		network:      networkManager,
		matrixWorker: matrixWorker,
		handler:      handler,
		done:         make(chan error, 1),
	}, nil
}

func (r *Runtime) Start(parent context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return errors.New("ECS runtime is already running")
	}
	if parent == nil {
		parent = context.Background()
	}
	listener, err := net.Listen("tcp", r.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", r.cfg.Listen, err)
	}
	runCtx, cancel := context.WithCancel(parent)
	if err := r.discovery.Start(runCtx); err != nil {
		cancel()
		_ = listener.Close()
		return fmt.Errorf("start discovery: %w", err)
	}
	r.cancel = cancel
	r.listener = listener
	r.httpServer = r.newHTTPServer()
	r.running = true
	r.done = make(chan error, 1)
	done := r.done
	logResolvedLLM(r.logger, r.cfg.LLM)
	logResolvedAuthentication(r.logger, r.cfg)
	r.logger.Info("ECS node listening", "version", version.Current(), "address", r.cfg.Listen, "node", r.cfg.NodeName)
	httpServer := r.httpServer
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	if r.network != nil {
		go r.network.Run(runCtx, r.discovery.Capabilities, r.logger)
	}
	if r.cfg.AgentTeams.Enabled && r.cfg.AgentTeams.ControllerURL != "" {
		go agentteams.NewReporter(r.cfg.AgentTeams, r.logger).Run(runCtx)
	}
	if r.matrixWorker != nil {
		go r.matrixWorker.Run(runCtx)
	}
	go r.runOfficeReaper(runCtx)
	return nil
}

func logResolvedAuthentication(logger *slog.Logger, cfg config.Config) {
	adminFingerprint := ""
	if cfg.AdminToken != "" {
		adminFingerprint = adminauth.Fingerprint(cfg.AdminToken)
	}
	logger.Info(
		"authentication resolved",
		"node_auth_enabled", cfg.AuthToken != "" || cfg.Network.Enabled,
		"admin_enabled", cfg.Admin.Enabled,
		"admin_auth_enabled", cfg.AdminToken != "",
		"admin_token_fingerprint", adminFingerprint,
	)
}

func logResolvedLLM(logger *slog.Logger, cfg config.LLMConfig) {
	logger.Info(
		"LLM runtime resolved",
		"base_url", cfg.BaseURL,
		"model", cfg.Model,
		"native_tools", cfg.NativeTools,
		"reasoning_effort", cfg.ReasoningEffort,
	)
}

func (r *Runtime) runOfficeReaper(ctx context.Context) {
	interval := r.cfg.Office.ReaperInterval.Duration()
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			recovered, err := r.office.ReapExpiredAttempts(now.UTC())
			if err != nil {
				r.logger.Error("Office attempt reaper failed", "error", err)
				continue
			}
			if len(recovered) > 0 {
				r.logger.Warn(
					"Office attempts moved to recovery_required",
					"attempts", len(recovered),
				)
			}
		}
	}
}

func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return nil
	}
	cancel := r.cancel
	httpServer := r.httpServer
	r.running = false
	r.cancel = nil
	r.listener = nil
	r.httpServer = nil
	r.mu.Unlock()
	cancel()
	err := httpServer.Shutdown(ctx)
	r.logger.Info("ECS node stopped")
	return err
}

func (r *Runtime) newHTTPServer() *http.Server {
	return &http.Server{
		Addr:              r.cfg.Listen,
		Handler:           r.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       r.cfg.HTTP.ReadTimeout.Duration(),
		WriteTimeout:      r.cfg.HTTP.WriteTimeout.Duration(),
		IdleTimeout:       60 * time.Second,
	}
}

func (r *Runtime) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.tools.Reload(); err != nil {
		return fmt.Errorf("reload tools: %w", err)
	}
	if err := r.skills.Reload(); err != nil {
		return fmt.Errorf("reload skills: %w", err)
	}
	if err := registerBuiltinTools(
		r.tools, r.skills, iotBuiltinRegistrations(r.iot),
	); err != nil {
		return fmt.Errorf("reload built-in tools: %w", err)
	}
	if err := r.roles.Reload(); err != nil {
		return fmt.Errorf("reload agent roles and pipelines: %w", err)
	}
	if err := r.office.Reload(); err != nil {
		return fmt.Errorf("reload office components: %w", err)
	}
	r.logger.Info("tools, skills, agent roles, pipelines, and office components reloaded")
	return nil
}

func (r *Runtime) Wait() error {
	return <-r.done
}

func (r *Runtime) Handler() http.Handler {
	return r.handler
}

// SetLLMAPIKey updates the provider credential in memory without rebuilding
// the node. Platform hosts remain responsible for storing the secret.
func (r *Runtime) SetLLMAPIKey(value string) {
	r.agent.SetLLMAPIKey(value)
}

func (r *Runtime) Status() Status {
	r.mu.Lock()
	running := r.running
	r.mu.Unlock()
	nodeID := ""
	if r.network != nil {
		nodeID = r.network.NodeID()
	}
	return Status{
		Version:          version.Current(),
		Node:             r.cfg.NodeName,
		Address:          r.cfg.Listen,
		Running:          running,
		NodeID:           nodeID,
		Platform:         r.host.Capabilities(),
		Tools:            len(r.tools.Available()),
		Skills:           len(r.skills.Available()),
		Agents:           len(r.roles.All()),
		Pipelines:        len(r.roles.Pipelines()),
		OfficeComponents: len(r.office.All()),
		ModelReady:       r.agent.LLMReady(),
	}
}
