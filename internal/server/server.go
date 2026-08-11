package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"ecs/internal/adminui"
	"ecs/internal/agent"
	"ecs/internal/agentteams"
	"ecs/internal/browsernode"
	"ecs/internal/browserui"
	"ecs/internal/config"
	"ecs/internal/discovery"
	"ecs/internal/networkauth"
	"ecs/internal/observability"
	"ecs/internal/office"
	"ecs/internal/protocol"
	"ecs/internal/roles"
	"ecs/internal/session"
	"ecs/internal/skills"
	"ecs/internal/tools"
	"ecs/internal/trace"
)

type Server struct {
	cfg               config.Config
	agent             *agent.Agent
	registry          *tools.Registry
	skills            *skills.Registry
	roles             *roles.Registry
	office            *office.Registry
	traces            *trace.Store
	sessions          *session.Store
	discovery         *discovery.Manager
	logger            *slog.Logger
	logs              *observability.LogStore
	metrics           *observability.Metrics
	network           *networkauth.Manager
	browserHub        browserRuntime
	startedAt         time.Time
	adminMu           sync.Mutex
	client            *http.Client
	slots             chan struct{}
	requestMu         sync.Mutex
	requests          map[string]*requestEntry
	recursiveMu       sync.Mutex
	recursiveRequests map[string]*recursiveRequestEntry
	peerScheduler     *peerScheduler
	agentTeamsStore   *agentteams.Store
	agentTeamsProfile agentteams.AgentProfile
}

type browserRuntime interface {
	http.Handler
	Dispatch(context.Context, string, browsernode.TaskRequest) (browsernode.TaskResponse, error)
	Connected(string) bool
}

type requestEntry struct {
	fingerprint [32]byte
	done        chan struct{}
	response    *protocol.ReplyResponse
	completedAt time.Time
}

type recursiveRequestEntry struct {
	fingerprint [32]byte
	done        chan struct{}
	response    *protocol.RecursiveReplyResponse
	completedAt time.Time
}

func New(
	cfg config.Config,
	runner *agent.Agent,
	registry *tools.Registry,
	sessions *session.Store,
	discoveryManager *discovery.Manager,
	logger *slog.Logger,
) http.Handler {
	return NewWithRuntime(cfg, runner, registry, nil, nil, sessions, discoveryManager, logger)
}

func NewWithSkills(
	cfg config.Config,
	runner *agent.Agent,
	registry *tools.Registry,
	skillRegistry *skills.Registry,
	sessions *session.Store,
	discoveryManager *discovery.Manager,
	logger *slog.Logger,
) http.Handler {
	return NewWithRuntime(cfg, runner, registry, skillRegistry, nil, sessions, discoveryManager, logger)
}

func NewWithRuntime(
	cfg config.Config,
	runner *agent.Agent,
	registry *tools.Registry,
	skillRegistry *skills.Registry,
	roleRegistry *roles.Registry,
	sessions *session.Store,
	discoveryManager *discovery.Manager,
	logger *slog.Logger,
) http.Handler {
	return NewWithAdminRuntime(
		cfg,
		runner,
		registry,
		skillRegistry,
		roleRegistry,
		sessions,
		discoveryManager,
		logger,
		nil,
	)
}

func NewWithAdminRuntime(
	cfg config.Config,
	runner *agent.Agent,
	registry *tools.Registry,
	skillRegistry *skills.Registry,
	roleRegistry *roles.Registry,
	sessions *session.Store,
	discoveryManager *discovery.Manager,
	logger *slog.Logger,
	logStore *observability.LogStore,
) http.Handler {
	return NewWithNetworkRuntime(
		cfg,
		runner,
		registry,
		skillRegistry,
		roleRegistry,
		sessions,
		discoveryManager,
		logger,
		logStore,
		nil,
	)
}

func NewWithNetworkRuntime(
	cfg config.Config,
	runner *agent.Agent,
	registry *tools.Registry,
	skillRegistry *skills.Registry,
	roleRegistry *roles.Registry,
	sessions *session.Store,
	discoveryManager *discovery.Manager,
	logger *slog.Logger,
	logStore *observability.LogStore,
	networkManager *networkauth.Manager,
) http.Handler {
	return NewWithOfficeRuntime(
		cfg, runner, registry, skillRegistry, roleRegistry, nil, sessions,
		discoveryManager, logger, logStore, networkManager,
	)
}

func NewWithOfficeRuntime(
	cfg config.Config,
	runner *agent.Agent,
	registry *tools.Registry,
	skillRegistry *skills.Registry,
	roleRegistry *roles.Registry,
	officeRegistry *office.Registry,
	sessions *session.Store,
	discoveryManager *discovery.Manager,
	logger *slog.Logger,
	logStore *observability.LogStore,
	networkManager *networkauth.Manager,
) http.Handler {
	traceStore, err := trace.NewStore(cfg.Trace)
	if err != nil {
		logger.Warn("trace store disabled", "error", err)
	}
	s := &Server{
		cfg:               cfg,
		agent:             runner,
		registry:          registry,
		skills:            skillRegistry,
		roles:             roleRegistry,
		office:            officeRegistry,
		traces:            traceStore,
		sessions:          sessions,
		discovery:         discoveryManager,
		logger:            logger,
		logs:              logStore,
		network:           networkManager,
		startedAt:         time.Now().UTC(),
		client:            &http.Client{Timeout: cfg.HTTP.PeerTimeout.Duration()},
		slots:             make(chan struct{}, cfg.HTTP.MaxConcurrent),
		requests:          make(map[string]*requestEntry),
		recursiveRequests: make(map[string]*recursiveRequestEntry),
		peerScheduler:     newPeerScheduler(),
		metrics:           observability.NewMetrics(),
	}
	if cfg.AgentTeams.Enabled {
		s.agentTeamsStore = agentteams.NewStore(cfg.AgentTeams.TaskDirectory)
		s.agentTeamsProfile = agentteams.AgentProfile{
			Name: cfg.AgentTeams.WorkerName,
			Role: "worker",
		}
		if cfg.AgentTeams.RuntimeConfigFile != "" {
			profile, _, err := agentteams.LoadRuntimeProfile(cfg.AgentTeams.RuntimeConfigFile)
			if err != nil {
				logger.Warn("AgentTeams runtime config not loaded; using local defaults", "error", err)
			} else {
				s.agentTeamsProfile = profile
				if s.agentTeamsProfile.Name == "" {
					s.agentTeamsProfile.Name = cfg.AgentTeams.WorkerName
				}
			}
		}
	}
	if networkManager != nil {
		s.browserHub = browsernode.New(networkManager, logger)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /v1/tools", s.withAuthScope("capabilities:read", s.listTools))
	mux.HandleFunc("GET /v1/skills", s.withAuthScope("capabilities:read", s.listSkills))
	mux.HandleFunc("GET /v1/agents", s.withAuthScope("capabilities:read", s.listAgents))
	mux.HandleFunc("GET /v1/pipelines", s.withAuthScope("capabilities:read", s.listPipelines))
	if s.office != nil {
		mux.HandleFunc("GET /v1/office/components", s.withAuthScope("capabilities:read", s.listOfficeComponents))
		mux.HandleFunc("GET /v1/office/work-orders", s.withAuthScope("reply:execute", s.listOfficeWorkOrders))
		mux.HandleFunc("POST /v1/office/components/{component}/work-orders", s.withAuthScope("reply:execute", s.createOfficeWorkOrder))
		mux.HandleFunc("GET /v1/office/work-orders/{order}", s.withAuthScope("reply:execute", s.getOfficeWorkOrder))
		mux.HandleFunc("POST /v1/office/work-orders/{order}/dispatch", s.withAuthScope("group:execute", s.dispatchOfficeWorkOrder))
		mux.HandleFunc("POST /v1/office/tasks/execute", s.withAuthScope("reply:execute", s.executeOfficeTask))
		mux.HandleFunc("POST /v1/office/tasks/compensate", s.withAuthScope("reply:execute", s.executeOfficeCompensation))
	}
	mux.HandleFunc("GET /v1/traces/{trace}", s.withAuthScope("trace:read", s.getTrace))
	mux.HandleFunc("GET /v1/traces", s.withAuthScope("trace:read", s.listTraces))
	mux.HandleFunc("GET /metrics", s.withAuthScope("capabilities:read", s.getMetrics))
	mux.HandleFunc("GET /v1/capabilities", s.withAuthScope("capabilities:read", s.capabilities))
	mux.HandleFunc("GET /v1/nodes", s.withAuthScope("topology:read", s.nodes))
	mux.HandleFunc("GET /v1/sessions/{session}", s.withAuthScope("session:read", s.getSession))
	if s.discovery.RegistryServing() {
		mux.Handle("/v1/registry/", s.discovery.RegistryHandler())
	}
	mux.HandleFunc("POST /v1/reply", s.withAuthScope("reply:execute", s.reply))
	mux.HandleFunc("POST /v1/groups/{group}/reply", s.withAuthScope("group:execute", s.groupReply))
	mux.HandleFunc("POST /v1/recursive/reply", s.withAuthScope("recursive:execute", s.recursiveReply))
	if s.network != nil {
		mux.HandleFunc("GET /v1/network/identity", s.networkIdentity)
		mux.HandleFunc("POST /v1/network/join", s.networkJoin)
		mux.HandleFunc("POST /v1/network/enrollments/{enrollment}/claim", s.networkClaim)
		mux.HandleFunc("POST /v1/network/heartbeat", s.networkHeartbeat)
		mux.Handle("GET /v1/network/browser/connect", s.browserHub)
		mux.HandleFunc("GET /browser", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/browser/", http.StatusTemporaryRedirect)
		})
		mux.Handle("GET /browser/", browserui.Handler())
	}
	if cfg.Admin.Enabled {
		mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
		})
		mux.Handle("GET /admin/", adminui.Handler())
		mux.HandleFunc("GET /v1/admin/overview", s.withAdminAuth(s.adminOverview))
		mux.HandleFunc("GET /v1/admin/tools", s.withAdminAuth(s.adminTools))
		mux.HandleFunc("PUT /v1/admin/tools/{tool}", s.withAdminAuth(s.saveAdminTool))
		mux.HandleFunc("DELETE /v1/admin/tools/{tool}", s.withAdminAuth(s.deleteAdminTool))
		mux.HandleFunc("GET /v1/admin/logs", s.withAdminAuth(s.adminLogs))
		mux.HandleFunc("GET /v1/admin/config", s.withAdminAuth(s.adminConfig))
		mux.HandleFunc("POST /v1/admin/debug/nodes/{node}/reply", s.withAdminAuth(s.adminDebugNodeReply))
		if s.office != nil {
			mux.HandleFunc(
				"POST /v1/admin/office/work-orders/{order}/tasks/{task}/approval",
				s.withAdminAuth(s.decideOfficeTaskApproval),
			)
		}
		if s.network != nil {
			mux.HandleFunc("GET /v1/admin/network/relationships", s.withAdminAuth(s.adminNetworkRelationships))
			mux.HandleFunc("POST /v1/admin/network/enrollments/{enrollment}/approve", s.withAdminAuth(s.adminNetworkApprove))
			mux.HandleFunc("POST /v1/admin/network/relationships/{enrollment}/status", s.withAdminAuth(s.adminNetworkStatus))
			mux.HandleFunc("PUT /v1/admin/network/relationships/{enrollment}/permissions", s.withAdminAuth(s.adminNetworkPermissions))
			mux.HandleFunc("POST /v1/admin/browser/nodes/{node}/tasks", s.withAdminAuth(s.adminBrowserTask))
		}
	}
	if cfg.AgentTeams.Enabled {
		mux.HandleFunc("POST /v1/agentteams/tasks", s.withAuthScope("agentteams:execute", s.agentTeamsTask))
		mux.HandleFunc("GET /v1/agentteams/tasks/{task}", s.withAuthScope("agentteams:read", s.getAgentTeamsTask))
	}
	return s.recoverPanic(s.requestLog(mux))
}

func (s *Server) listAgents(w http.ResponseWriter, _ *http.Request) {
	if s.roles == nil {
		writeJSON(w, http.StatusOK, []roles.Manifest{})
		return
	}
	writeJSON(w, http.StatusOK, s.roles.All())
}

func (s *Server) listPipelines(w http.ResponseWriter, _ *http.Request) {
	if s.roles == nil {
		writeJSON(w, http.StatusOK, []roles.Pipeline{})
		return
	}
	writeJSON(w, http.StatusOK, s.roles.Pipelines())
}

func (s *Server) getTrace(w http.ResponseWriter, r *http.Request) {
	if s.traces == nil {
		writeError(w, http.StatusNotFound, "trace not found")
		return
	}
	var value trace.Trace
	var err error
	if relationship, ok := r.Context().Value(networkRelationshipKey{}).(networkauth.PublicRelationship); ok {
		value, err = s.traces.LoadOwned(relationship.PeerNodeID, r.PathValue("trace"))
	} else {
		value, err = s.traces.Load(r.PathValue("trace"))
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "trace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if r.URL.Query().Get("aggregate") == "true" || r.URL.Query().Get("aggregate") == "1" {
		depth := 8
		if raw := r.URL.Query().Get("depth"); raw != "" {
			value, parseErr := strconv.Atoi(raw)
			if parseErr != nil || value < 0 || value > 8 {
				writeError(w, http.StatusBadRequest, "depth must be 0..8")
				return
			}
			depth = value
		}
		bundle := s.aggregateTrace(r.Context(), value, depth)
		writeJSON(w, http.StatusOK, bundle)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
