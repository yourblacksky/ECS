package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Duration time.Duration

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("duration must be a string such as \"30s\"")
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration().String())
}

type Config struct {
	NodeName         string            `json:"node_name"`
	Listen           string            `json:"listen"`
	AuthToken        string            `json:"auth_token"`
	AuthTokenSecret  string            `json:"auth_token_secret,omitempty"`
	AdminToken       string            `json:"admin_token,omitempty"`
	AdminTokenSecret string            `json:"admin_token_secret,omitempty"`
	Log              string            `json:"log_level"`
	LLM              LLMConfig         `json:"llm"`
	Agent            AgentConfig       `json:"agent"`
	AgentRoles       AgentRolesConfig  `json:"agent_roles"`
	AgentTeams       AgentTeamsConfig  `json:"agentteams"`
	Office           OfficeConfig      `json:"office"`
	Skills           SkillsConfig      `json:"skills"`
	Tools            ToolsConfig       `json:"tools"`
	Trace            TraceConfig       `json:"trace"`
	Admin            AdminConfig       `json:"admin"`
	Sessions         SessionsConfig    `json:"sessions"`
	Discovery        DiscoveryConfig   `json:"discovery"`
	Nested           NestedConfig      `json:"nested"`
	Network          NetworkAuthConfig `json:"network_auth"`
	IoT              IoTConfig         `json:"iot"`
	HTTP             HTTPConfig        `json:"http"`
	Groups           map[string][]Peer `json:"groups"`
}

type LLMConfig struct {
	BaseURL         string   `json:"base_url"`
	APIKey          string   `json:"api_key"`
	APIKeySecret    string   `json:"api_key_secret,omitempty"`
	Model           string   `json:"model"`
	Timeout         Duration `json:"timeout"`
	NativeTools     bool     `json:"native_tools"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
}

type AgentConfig struct {
	SystemPromptFile          string `json:"system_prompt_file"`
	ConversationGuidanceFile  string `json:"conversation_guidance_file"`
	ConversationGuidance      bool   `json:"conversation_guidance"`
	MaxSystemPromptChars      int    `json:"max_system_prompt_chars"`
	MaxAgentContextChars      int    `json:"max_agent_context_chars"`
	MaxConversationGuideChars int    `json:"max_conversation_guide_chars"`
	MaxToolCatalogChars       int    `json:"max_tool_catalog_chars"`
	MaxToolRounds             int    `json:"max_tool_rounds"`
	MaxPromptTools            int    `json:"max_prompt_tools"`
	ApprovalMode              string `json:"approval_mode"`
	GroupSynthesis            bool   `json:"group_synthesis"`
	GroupDispatchMode         string `json:"group_dispatch_mode"`
	GroupRaceFanout           int    `json:"group_race_fanout"`
}

type AgentRolesConfig struct {
	Directory         string `json:"directory"`
	PipelineDirectory string `json:"pipeline_directory"`
	DefaultPipeline   string `json:"default_pipeline"`
}

type AgentTeamsConfig struct {
	Enabled            bool     `json:"enabled"`
	ControllerURL      string   `json:"controller_url"`
	WorkerName         string   `json:"worker_name"`
	RuntimeConfigFile  string   `json:"runtime_config_file"`
	TaskDirectory      string   `json:"task_directory"`
	MaxTaskPromptChars int      `json:"max_task_prompt_chars"`
	HeartbeatInterval  Duration `json:"heartbeat_interval"`
	MatrixURL          string   `json:"matrix_url"`
	MatrixTokenEnv     string   `json:"matrix_token_env"`
	MatrixSyncTimeout  Duration `json:"matrix_sync_timeout"`
	MatrixSinceFile    string   `json:"matrix_since_file"`
}

type OfficeConfig struct {
	Directory         string   `json:"directory"`
	OrderDirectory    string   `json:"order_directory"`
	MaxStages         int      `json:"max_stages"`
	MaxContextEntries int      `json:"max_context_entries"`
	ApprovalTTL       Duration `json:"approval_ttl"`
	AttemptLease      Duration `json:"attempt_lease"`
	ReaperInterval    Duration `json:"reaper_interval"`
}

type ToolsConfig struct {
	Directory      string   `json:"directory"`
	DefaultTimeout Duration `json:"default_timeout"`
	MaxOutputBytes int64    `json:"max_output_bytes"`
}

type SkillsConfig struct {
	Directory             string `json:"directory"`
	KnowledgeDirectory    string `json:"knowledge_directory"`
	MaxPromptSkills       int    `json:"max_prompt_skills"`
	MaxSteps              int    `json:"max_steps"`
	DocumentationRequired bool   `json:"documentation_required"`
	GroundingEnabled      bool   `json:"grounding_enabled"`
	MaxDocumentChars      int    `json:"max_document_chars"`
	MaxGroundingChars     int    `json:"max_grounding_chars"`
	MaxKnowledgeDocuments int    `json:"max_knowledge_documents"`
}

type TraceConfig struct {
	Enabled       bool     `json:"enabled"`
	Directory     string   `json:"directory"`
	MaxValueChars int      `json:"max_value_chars"`
	MaxTraces     int      `json:"max_traces"`
	Retention     Duration `json:"retention"`
}

type AdminConfig struct {
	Enabled     bool `json:"enabled"`
	LogCapacity int  `json:"log_capacity"`
}

type SessionsConfig struct {
	Directory       string `json:"directory"`
	MaxMessages     int    `json:"max_messages"`
	ContextMessages int    `json:"context_messages"`
	MaxContextChars int    `json:"max_context_chars"`
}

type DiscoveryConfig struct {
	Enabled      bool           `json:"enabled"`
	ServiceType  string         `json:"service_type"`
	AdvertiseURL string         `json:"advertise_url"`
	Groups       []string       `json:"groups"`
	Interval     Duration       `json:"interval"`
	Expiry       Duration       `json:"expiry"`
	Registry     RegistryConfig `json:"registry"`
}

// RegistryConfig adds cross-network discovery without changing enrollment or
// authorization. Nodes may consume multiple registries and serve one locally.
type RegistryConfig struct {
	Enabled           bool     `json:"enabled"`
	Serve             bool     `json:"serve"`
	ControllerURLs    []string `json:"controller_urls"`
	Token             string   `json:"token,omitempty"`
	TokenEnv          string   `json:"token_env,omitempty"`
	TokenSecret       string   `json:"token_secret,omitempty"`
	HeartbeatInterval Duration `json:"heartbeat_interval"`
	LeaseTimeout      Duration `json:"lease_timeout"`
	RequestTimeout    Duration `json:"request_timeout"`
}

type NestedConfig struct {
	Enabled         bool     `json:"enabled"`
	Children        []Peer   `json:"children"`
	DiscoveryGroups []string `json:"discovery_groups"`
	DefaultMaxDepth int      `json:"default_max_depth"`
}

type NetworkAuthConfig struct {
	Enabled           bool              `json:"enabled"`
	Directory         string            `json:"directory"`
	Parent            ParentConfig      `json:"parent"`
	RouteOverrides    map[string]string `json:"route_overrides,omitempty"`
	HeartbeatInterval Duration          `json:"heartbeat_interval"`
	LeaseTimeout      Duration          `json:"lease_timeout"`
	EnrollmentTTL     Duration          `json:"enrollment_ttl"`
}

type ParentConfig struct {
	URL               string   `json:"url"`
	NodeID            string   `json:"node_id,omitempty"`
	AutoDiscover      bool     `json:"auto_discover,omitempty"`
	DiscoveryGroups   []string `json:"discovery_groups,omitempty"`
	PairingCode       string   `json:"pairing_code,omitempty"`
	PairingCodeEnv    string   `json:"pairing_code_env,omitempty"`
	PairingCodeSecret string   `json:"pairing_code_secret,omitempty"`
}

// IoTConfig contains named, administrator-controlled connector profiles. Tool
// calls select a profile but cannot supply an arbitrary host or broker.
type IoTConfig struct {
	HTTP             map[string]IoTHTTPProfile `json:"http"`
	MQTT             map[string]IoTMQTTProfile `json:"mqtt"`
	MaxResponseBytes int64                     `json:"max_response_bytes"`
}

type IoTHTTPProfile struct {
	BaseURL             string            `json:"base_url"`
	AllowedMethods      []string          `json:"allowed_methods"`
	AllowedPathPrefixes []string          `json:"allowed_path_prefixes"`
	HeadersEnv          map[string]string `json:"headers_env,omitempty"`
	Timeout             Duration          `json:"timeout"`
}

type IoTMQTTProfile struct {
	BrokerURL            string   `json:"broker_url"`
	ClientID             string   `json:"client_id,omitempty"`
	UsernameEnv          string   `json:"username_env,omitempty"`
	PasswordEnv          string   `json:"password_env,omitempty"`
	AllowedTopicPrefixes []string `json:"allowed_topic_prefixes"`
	Timeout              Duration `json:"timeout"`
}

type HTTPConfig struct {
	ReadTimeout   Duration `json:"read_timeout"`
	WriteTimeout  Duration `json:"write_timeout"`
	MaxBodyBytes  int64    `json:"max_body_bytes"`
	MaxConcurrent int      `json:"max_concurrent"`
	PeerTimeout   Duration `json:"peer_timeout"`
	PeerRetries   int      `json:"peer_retries"`
	RetryBackoff  Duration `json:"retry_backoff"`
}

type Peer struct {
	NodeID      string `json:"node_id,omitempty"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Token       string `json:"token"`
	TokenEnv    string `json:"token_env,omitempty"`
	TokenSecret string `json:"token_secret,omitempty"`
	Transport   string `json:"-"`
}

var configIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

func validateIoTConfig(cfg IoTConfig) error {
	if cfg.MaxResponseBytes < 1024 || cfg.MaxResponseBytes > 1<<20 {
		return errors.New("iot.max_response_bytes must be between 1024 and 1048576")
	}
	for name, profile := range cfg.HTTP {
		if !configIdentifier.MatchString(name) {
			return fmt.Errorf("iot.http profile name %q is invalid", name)
		}
		parsed, err := url.Parse(profile.BaseURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("iot.http.%s.base_url must be an http(s) origin or base path without credentials, query, or fragment", name)
		}
		if profile.Timeout.Duration() <= 0 || profile.Timeout.Duration() > 2*time.Minute {
			return fmt.Errorf("iot.http.%s.timeout must be positive and at most 2m", name)
		}
		if len(profile.AllowedMethods) == 0 || len(profile.AllowedPathPrefixes) == 0 {
			return fmt.Errorf("iot.http.%s requires allowed_methods and allowed_path_prefixes", name)
		}
		for _, method := range profile.AllowedMethods {
			switch strings.ToUpper(strings.TrimSpace(method)) {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
			default:
				return fmt.Errorf("iot.http.%s contains unsupported method %q", name, method)
			}
		}
		for _, prefix := range profile.AllowedPathPrefixes {
			if !strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "..") || strings.ContainsAny(prefix, "?#") {
				return fmt.Errorf("iot.http.%s contains invalid path prefix %q", name, prefix)
			}
		}
		for header, environment := range profile.HeadersEnv {
			if strings.TrimSpace(header) == "" || strings.ContainsAny(header, "\r\n:") ||
				!validEnvironmentName(environment) {
				return fmt.Errorf("iot.http.%s contains an invalid header environment mapping", name)
			}
		}
	}
	for name, profile := range cfg.MQTT {
		if !configIdentifier.MatchString(name) {
			return fmt.Errorf("iot.mqtt profile name %q is invalid", name)
		}
		parsed, err := url.Parse(profile.BrokerURL)
		if err != nil || parsed.Host == "" || !allowedMQTTScheme(parsed.Scheme) || parsed.User != nil {
			return fmt.Errorf("iot.mqtt.%s.broker_url must use mqtt, mqtts, tcp, ssl, ws, or wss without embedded credentials", name)
		}
		if profile.Timeout.Duration() <= 0 || profile.Timeout.Duration() > 2*time.Minute {
			return fmt.Errorf("iot.mqtt.%s.timeout must be positive and at most 2m", name)
		}
		if len(profile.AllowedTopicPrefixes) == 0 {
			return fmt.Errorf("iot.mqtt.%s.allowed_topic_prefixes is required", name)
		}
		for _, prefix := range profile.AllowedTopicPrefixes {
			if strings.TrimSpace(prefix) == "" || strings.ContainsAny(prefix, "+#\x00") {
				return fmt.Errorf("iot.mqtt.%s contains invalid topic prefix %q", name, prefix)
			}
		}
		if profile.UsernameEnv != "" && !validEnvironmentName(profile.UsernameEnv) {
			return fmt.Errorf("iot.mqtt.%s.username_env is invalid", name)
		}
		if profile.PasswordEnv != "" && !validEnvironmentName(profile.PasswordEnv) {
			return fmt.Errorf("iot.mqtt.%s.password_env is invalid", name)
		}
	}
	return nil
}

func validEnvironmentName(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if (char >= 'A' && char <= 'Z') || char == '_' ||
			(index > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func allowedMQTTScheme(value string) bool {
	switch strings.ToLower(value) {
	case "mqtt", "mqtts", "tcp", "ssl", "ws", "wss":
		return true
	default:
		return false
	}
}

type SecretResolver interface {
	Get(string) (string, error)
}

func defaults() Config {
	return Config{
		NodeName: "ecs-node",
		Listen:   "127.0.0.1:8090",
		Log:      "info",
		LLM: LLMConfig{
			BaseURL:         "http://127.0.0.1:11434/v1",
			Timeout:         Duration(90 * time.Second),
			ReasoningEffort: "none",
		},
		Agent: AgentConfig{
			SystemPromptFile:          "prompts/system.small.zh.txt",
			ConversationGuidanceFile:  "prompts/conversation.default.zh.txt",
			ConversationGuidance:      true,
			MaxSystemPromptChars:      8000,
			MaxAgentContextChars:      2200,
			MaxConversationGuideChars: 900,
			MaxToolCatalogChars:       3200,
			MaxToolRounds:             3,
			MaxPromptTools:            12,
			ApprovalMode:              "high_risk",
			GroupSynthesis:            true,
			GroupDispatchMode:         "single",
			GroupRaceFanout:           2,
		},
		AgentRoles: AgentRolesConfig{
			Directory:         "agents.d",
			PipelineDirectory: "pipelines.d",
			DefaultPipeline:   "default",
		},
		AgentTeams: AgentTeamsConfig{
			TaskDirectory:      "data/agentteams/shared",
			MaxTaskPromptChars: 3000,
			HeartbeatInterval:  Duration(30 * time.Second),
			MatrixTokenEnv:     "AGENTTEAMS_WORKER_MATRIX_TOKEN",
			MatrixSyncTimeout:  Duration(30 * time.Second),
			MatrixSinceFile:    "data/agentteams/matrix-since",
		},
		Office: OfficeConfig{
			Directory:         "office.d",
			OrderDirectory:    "data/office/orders",
			MaxStages:         16,
			MaxContextEntries: 64,
			ApprovalTTL:       Duration(15 * time.Minute),
			AttemptLease:      Duration(2 * time.Minute),
			ReaperInterval:    Duration(30 * time.Second),
		},
		Tools: ToolsConfig{
			Directory:      "tools.d",
			DefaultTimeout: Duration(10 * time.Second),
			MaxOutputBytes: 64 << 10,
		},
		Skills: SkillsConfig{
			Directory:             "skills.d",
			KnowledgeDirectory:    "skills.d/_knowledge",
			MaxPromptSkills:       5,
			MaxSteps:              4,
			DocumentationRequired: true,
			GroundingEnabled:      true,
			MaxDocumentChars:      12000,
			MaxGroundingChars:     1800,
			MaxKnowledgeDocuments: 3,
		},
		Trace: TraceConfig{
			Enabled:       true,
			Directory:     "data/traces",
			MaxValueChars: 4000,
			MaxTraces:     1000,
			Retention:     Duration(7 * 24 * time.Hour),
		},
		Admin: AdminConfig{
			Enabled:     true,
			LogCapacity: 1000,
		},
		Sessions: SessionsConfig{
			Directory:       "data/sessions",
			MaxMessages:     0,
			ContextMessages: 20,
			MaxContextChars: 4000,
		},
		Discovery: DiscoveryConfig{
			Enabled:     true,
			ServiceType: "_ecs._tcp",
			Interval:    Duration(5 * time.Second),
			Expiry:      Duration(20 * time.Second),
			Registry: RegistryConfig{
				TokenEnv:          "ECS_DISCOVERY_REGISTRY_TOKEN",
				HeartbeatInterval: Duration(15 * time.Second),
				LeaseTimeout:      Duration(45 * time.Second),
				RequestTimeout:    Duration(5 * time.Second),
			},
		},
		Nested: NestedConfig{
			Enabled:         true,
			DefaultMaxDepth: 8,
		},
		Network: NetworkAuthConfig{
			Directory:         "data/network",
			RouteOverrides:    map[string]string{},
			HeartbeatInterval: Duration(30 * time.Second),
			LeaseTimeout:      Duration(90 * time.Second),
			EnrollmentTTL:     Duration(10 * time.Minute),
		},
		IoT: IoTConfig{
			HTTP:             map[string]IoTHTTPProfile{},
			MQTT:             map[string]IoTMQTTProfile{},
			MaxResponseBytes: 64 << 10,
		},
		HTTP: HTTPConfig{
			ReadTimeout:   Duration(15 * time.Second),
			WriteTimeout:  Duration(120 * time.Second),
			MaxBodyBytes:  1 << 20,
			MaxConcurrent: 16,
			PeerTimeout:   Duration(120 * time.Second),
			PeerRetries:   2,
			RetryBackoff:  Duration(500 * time.Millisecond),
		},
		Groups: map[string][]Peer{},
	}
}

func Load(path string) (Config, error) {
	return LoadWithSecrets(path, nil)
}

func LoadWithSecrets(path string, secrets SecretResolver) (Config, error) {
	cfg := defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := applySecrets(&cfg, secrets); err != nil {
		return Config{}, err
	}
	if value := os.Getenv("ECS_AUTH_TOKEN"); value != "" {
		cfg.AuthToken = value
	}
	if value := os.Getenv("ECS_ADMIN_TOKEN"); value != "" {
		cfg.AdminToken = value
	}
	if value := os.Getenv("ECS_LLM_API_KEY"); value != "" {
		cfg.LLM.APIKey = value
	}
	if value := os.Getenv("ECS_AGENTTEAMS_CONTROLLER_URL"); value != "" {
		cfg.AgentTeams.ControllerURL = value
	}
	if value := os.Getenv("ECS_AGENTTEAMS_WORKER_NAME"); value != "" {
		cfg.AgentTeams.WorkerName = value
	}
	if value := os.Getenv("AGENTTEAMS_MATRIX_URL"); value != "" {
		cfg.AgentTeams.MatrixURL = value
	}
	if cfg.Discovery.Registry.TokenEnv != "" {
		if value := os.Getenv(cfg.Discovery.Registry.TokenEnv); value != "" {
			cfg.Discovery.Registry.Token = value
		}
	}
	for group, peers := range cfg.Groups {
		for index := range peers {
			if peers[index].TokenEnv != "" {
				if value := os.Getenv(peers[index].TokenEnv); value != "" {
					peers[index].Token = value
				}
			}
		}
		cfg.Groups[group] = peers
	}
	for index := range cfg.Nested.Children {
		if cfg.Nested.Children[index].TokenEnv != "" {
			if value := os.Getenv(cfg.Nested.Children[index].TokenEnv); value != "" {
				cfg.Nested.Children[index].Token = value
			}
		}
	}
	if cfg.Network.Parent.PairingCodeEnv != "" {
		if value := os.Getenv(cfg.Network.Parent.PairingCodeEnv); value != "" {
			cfg.Network.Parent.PairingCode = value
		}
	}
	base := filepath.Dir(path)
	if !filepath.IsAbs(cfg.Agent.SystemPromptFile) {
		cfg.Agent.SystemPromptFile = filepath.Join(base, cfg.Agent.SystemPromptFile)
	}
	if cfg.Agent.ConversationGuidanceFile != "" && !filepath.IsAbs(cfg.Agent.ConversationGuidanceFile) {
		cfg.Agent.ConversationGuidanceFile = filepath.Join(base, cfg.Agent.ConversationGuidanceFile)
	}
	if !filepath.IsAbs(cfg.AgentRoles.Directory) {
		cfg.AgentRoles.Directory = filepath.Join(base, cfg.AgentRoles.Directory)
	}
	if !filepath.IsAbs(cfg.AgentRoles.PipelineDirectory) {
		cfg.AgentRoles.PipelineDirectory = filepath.Join(base, cfg.AgentRoles.PipelineDirectory)
	}
	if cfg.AgentTeams.RuntimeConfigFile != "" && !filepath.IsAbs(cfg.AgentTeams.RuntimeConfigFile) {
		cfg.AgentTeams.RuntimeConfigFile = filepath.Join(base, cfg.AgentTeams.RuntimeConfigFile)
	}
	if !filepath.IsAbs(cfg.AgentTeams.TaskDirectory) {
		cfg.AgentTeams.TaskDirectory = filepath.Join(base, cfg.AgentTeams.TaskDirectory)
	}
	if !filepath.IsAbs(cfg.Office.Directory) {
		cfg.Office.Directory = filepath.Join(base, cfg.Office.Directory)
	}
	if !filepath.IsAbs(cfg.Office.OrderDirectory) {
		cfg.Office.OrderDirectory = filepath.Join(base, cfg.Office.OrderDirectory)
	}
	if cfg.AgentTeams.MatrixSinceFile != "" && !filepath.IsAbs(cfg.AgentTeams.MatrixSinceFile) {
		cfg.AgentTeams.MatrixSinceFile = filepath.Join(base, cfg.AgentTeams.MatrixSinceFile)
	}
	if !filepath.IsAbs(cfg.Tools.Directory) {
		cfg.Tools.Directory = filepath.Join(base, cfg.Tools.Directory)
	}
	if !filepath.IsAbs(cfg.Skills.Directory) {
		cfg.Skills.Directory = filepath.Join(base, cfg.Skills.Directory)
	}
	if cfg.Skills.KnowledgeDirectory != "" && !filepath.IsAbs(cfg.Skills.KnowledgeDirectory) {
		cfg.Skills.KnowledgeDirectory = filepath.Join(base, cfg.Skills.KnowledgeDirectory)
	}
	if !filepath.IsAbs(cfg.Trace.Directory) {
		cfg.Trace.Directory = filepath.Join(base, cfg.Trace.Directory)
	}
	if !filepath.IsAbs(cfg.Sessions.Directory) {
		cfg.Sessions.Directory = filepath.Join(base, cfg.Sessions.Directory)
	}
	if !filepath.IsAbs(cfg.Network.Directory) {
		cfg.Network.Directory = filepath.Join(base, cfg.Network.Directory)
	}
	if (cfg.Discovery.Enabled || cfg.Discovery.Registry.Enabled || cfg.Network.Enabled) &&
		cfg.Discovery.AdvertiseURL == "" {
		cfg.Discovery.AdvertiseURL = deriveAdvertiseURL(cfg.Listen)
	}
	return cfg, cfg.Validate()
}

func applySecrets(cfg *Config, secrets SecretResolver) error {
	if secrets == nil {
		return nil
	}
	resolve := func(reference string, destination *string) error {
		reference = strings.TrimSpace(reference)
		if reference == "" {
			return nil
		}
		value, err := secrets.Get(reference)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("resolve secret %q: %w", reference, err)
		}
		*destination = value
		return nil
	}
	if err := resolve(cfg.AuthTokenSecret, &cfg.AuthToken); err != nil {
		return err
	}
	if err := resolve(cfg.AdminTokenSecret, &cfg.AdminToken); err != nil {
		return err
	}
	if err := resolve(cfg.LLM.APIKeySecret, &cfg.LLM.APIKey); err != nil {
		return err
	}
	if err := resolve(cfg.Network.Parent.PairingCodeSecret, &cfg.Network.Parent.PairingCode); err != nil {
		return err
	}
	if err := resolve(cfg.Discovery.Registry.TokenSecret, &cfg.Discovery.Registry.Token); err != nil {
		return err
	}
	for group, peers := range cfg.Groups {
		for index := range peers {
			if err := resolve(peers[index].TokenSecret, &peers[index].Token); err != nil {
				return err
			}
		}
		cfg.Groups[group] = peers
	}
	for index := range cfg.Nested.Children {
		if err := resolve(cfg.Nested.Children[index].TokenSecret, &cfg.Nested.Children[index].Token); err != nil {
			return err
		}
	}
	return nil
}

func deriveAdvertiseURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		interfaces, _ := net.Interfaces()
		for _, networkInterface := range interfaces {
			addresses, _ := networkInterface.Addrs()
			for _, address := range addresses {
				var ip net.IP
				switch value := address.(type) {
				case *net.IPNet:
					ip = value.IP
				case *net.IPAddr:
					ip = value.IP
				}
				if ip != nil && ip.To4() != nil && !ip.IsLoopback() {
					host = ip.String()
					break
				}
			}
			if host != "" && host != "0.0.0.0" && host != "::" {
				break
			}
		}
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func (c Config) Validate() error {
	if err := validateIoTConfig(c.IoT); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	ip := net.ParseIP(host)
	isLoopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if c.Admin.Enabled && !isLoopback && strings.TrimSpace(c.AdminToken) == "" {
		return errors.New("admin_token or ECS_ADMIN_TOKEN is required when Admin is enabled outside loopback")
	}
	if !isLoopback && strings.TrimSpace(c.AuthToken) == "" &&
		!(c.Network.Enabled && strings.TrimSpace(c.AdminToken) != "") {
		return errors.New("auth_token, ECS_AUTH_TOKEN, or network_auth with admin_token is required when listening outside loopback")
	}
	if strings.TrimSpace(c.LLM.BaseURL) == "" {
		return errors.New("llm.base_url is required")
	}
	if strings.TrimSpace(c.LLM.Model) == "" {
		return errors.New("llm.model is required and must be declared explicitly")
	}
	switch c.LLM.ReasoningEffort {
	case "", "none", "low", "medium", "high":
	default:
		return errors.New("llm.reasoning_effort must be none, low, medium, or high")
	}
	if c.Agent.MaxToolRounds < 0 || c.Agent.MaxToolRounds > 10 {
		return errors.New("agent.max_tool_rounds must be between 0 and 10")
	}
	if c.Agent.MaxPromptTools < 1 {
		return errors.New("agent.max_prompt_tools must be positive")
	}
	if strings.TrimSpace(c.Skills.Directory) == "" ||
		c.Skills.MaxPromptSkills < 1 || c.Skills.MaxPromptSkills > 20 ||
		c.Skills.MaxSteps < 1 || c.Skills.MaxSteps > 10 ||
		c.Skills.MaxDocumentChars < 1000 || c.Skills.MaxDocumentChars > 100000 ||
		c.Skills.MaxGroundingChars < 200 || c.Skills.MaxGroundingChars > c.Skills.MaxDocumentChars ||
		c.Skills.MaxKnowledgeDocuments < 0 || c.Skills.MaxKnowledgeDocuments > 20 {
		return errors.New("skills.directory is required, max_prompt_skills must be 1..20, max_steps must be 1..10, max_document_chars must be 1000..100000, max_grounding_chars must be 200..max_document_chars, and max_knowledge_documents must be 0..20")
	}
	if c.Trace.Enabled && (strings.TrimSpace(c.Trace.Directory) == "" ||
		c.Trace.MaxValueChars < 200 || c.Trace.MaxValueChars > 100000 ||
		c.Trace.MaxTraces < 1 || c.Trace.MaxTraces > 100000 ||
		c.Trace.Retention.Duration() < time.Hour) {
		return errors.New("trace requires directory, max_value_chars 200..100000, max_traces 1..100000, and retention of at least 1h")
	}
	if c.Admin.Enabled && (c.Admin.LogCapacity < 100 || c.Admin.LogCapacity > 10000) {
		return errors.New("admin.log_capacity must be between 100 and 10000")
	}
	if c.Agent.MaxSystemPromptChars < 1000 {
		return errors.New("agent.max_system_prompt_chars must be at least 1000")
	}
	if c.Agent.MaxAgentContextChars < 200 ||
		c.Agent.MaxConversationGuideChars < 0 ||
		c.Agent.MaxToolCatalogChars < 200 {
		return errors.New("agent prompt section limits are invalid")
	}
	switch c.Agent.ApprovalMode {
	case "off", "high_risk", "all":
	default:
		return errors.New("agent.approval_mode must be off, high_risk, or all")
	}
	switch c.Agent.GroupDispatchMode {
	case "single", "broadcast", "race":
	default:
		return errors.New("agent.group_dispatch_mode must be single, broadcast, or race")
	}
	if c.Agent.GroupRaceFanout < 1 || c.Agent.GroupRaceFanout > 32 {
		return errors.New("agent.group_race_fanout must be between 1 and 32")
	}
	if strings.TrimSpace(c.AgentRoles.Directory) == "" ||
		strings.TrimSpace(c.AgentRoles.PipelineDirectory) == "" ||
		strings.TrimSpace(c.AgentRoles.DefaultPipeline) == "" {
		return errors.New("agent_roles.directory, pipeline_directory, and default_pipeline are required")
	}
	if c.Sessions.MaxMessages < 0 {
		return errors.New("sessions.max_messages cannot be negative")
	}
	if c.Sessions.ContextMessages < 1 {
		return errors.New("sessions.context_messages must be positive")
	}
	if c.Discovery.Enabled {
		serviceType := strings.TrimSuffix(strings.TrimSpace(c.Discovery.ServiceType), ".")
		if !strings.HasPrefix(serviceType, "_") || !strings.HasSuffix(serviceType, "._tcp") ||
			strings.ContainsAny(serviceType, " /") {
			return errors.New("discovery.service_type must be a DNS-SD TCP service such as _ecs._tcp")
		}
	}
	if c.Discovery.Enabled || c.Discovery.Registry.Enabled {
		parsed, err := url.Parse(c.Discovery.AdvertiseURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("discovery.advertise_url must be an http(s) URL when discovery is enabled")
		}
		if c.Discovery.Interval.Duration() <= 0 || c.Discovery.Expiry.Duration() <= c.Discovery.Interval.Duration() {
			return errors.New("discovery expiry must be greater than its positive interval")
		}
	}
	if c.Discovery.Registry.Enabled {
		if !c.Network.Enabled {
			return errors.New("network_auth must be enabled when discovery.registry is enabled")
		}
		if len(c.Discovery.Registry.ControllerURLs) == 0 {
			return errors.New("discovery.registry.controller_urls is required when registry discovery is enabled")
		}
	}
	if c.Discovery.Registry.Enabled || c.Discovery.Registry.Serve {
		if c.Discovery.Registry.HeartbeatInterval.Duration() <= 0 ||
			c.Discovery.Registry.LeaseTimeout.Duration() <= c.Discovery.Registry.HeartbeatInterval.Duration() {
			return errors.New("discovery.registry.lease_timeout must be greater than its positive heartbeat_interval")
		}
		if c.Discovery.Registry.RequestTimeout.Duration() <= 0 {
			return errors.New("discovery.registry.request_timeout must be positive")
		}
		for _, controllerURL := range c.Discovery.Registry.ControllerURLs {
			parsed, err := url.Parse(controllerURL)
			if err != nil || parsed.Host == "" ||
				(parsed.Scheme != "http" && parsed.Scheme != "https") {
				return errors.New("discovery.registry.controller_urls must contain only http(s) URLs")
			}
		}
	}
	if c.Nested.DefaultMaxDepth < 0 {
		return errors.New("nested.default_max_depth cannot be negative")
	}
	if c.Network.Enabled {
		if !c.Admin.Enabled {
			return errors.New("admin must be enabled when network authentication is enabled")
		}
		if strings.TrimSpace(c.AdminToken) == "" {
			return errors.New("admin_token or ECS_ADMIN_TOKEN is required when network authentication is enabled")
		}
		if strings.TrimSpace(c.Network.Directory) == "" {
			return errors.New("network_auth.directory is required when network authentication is enabled")
		}
		if c.Network.HeartbeatInterval.Duration() <= 0 ||
			c.Network.LeaseTimeout.Duration() <= c.Network.HeartbeatInterval.Duration() {
			return errors.New("network_auth.lease_timeout must be greater than its positive heartbeat_interval")
		}
		if c.Network.EnrollmentTTL.Duration() < time.Minute {
			return errors.New("network_auth.enrollment_ttl must be at least 1m")
		}
		advertised, err := url.Parse(c.Discovery.AdvertiseURL)
		if err != nil || advertised.Host == "" ||
			(advertised.Scheme != "http" && advertised.Scheme != "https") {
			return errors.New("discovery.advertise_url must be an http(s) URL when network authentication is enabled")
		}
		if c.Network.Parent.URL != "" {
			parsed, err := url.Parse(c.Network.Parent.URL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return errors.New("network_auth.parent.url must be an http(s) URL")
			}
		}
		if c.Network.Parent.AutoDiscover &&
			!c.Discovery.Enabled && !c.Discovery.Registry.Enabled {
			return errors.New("local or registry discovery must be enabled when network_auth.parent.auto_discover is enabled")
		}
		for nodeID, route := range c.Network.RouteOverrides {
			if strings.TrimSpace(nodeID) == "" {
				return errors.New("network_auth.route_overrides keys must be non-empty node IDs")
			}
			parsed, err := url.Parse(route)
			if err != nil || parsed.Host == "" ||
				(parsed.Scheme != "http" && parsed.Scheme != "https") {
				return errors.New("network_auth.route_overrides must contain only http(s) URLs")
			}
		}
	}
	if c.AgentTeams.Enabled {
		if strings.TrimSpace(c.AgentTeams.WorkerName) == "" {
			return errors.New("agentteams.worker_name is required when AgentTeams is enabled")
		}
		if strings.TrimSpace(c.AgentTeams.TaskDirectory) == "" {
			return errors.New("agentteams.task_directory is required when AgentTeams is enabled")
		}
		if c.AgentTeams.MaxTaskPromptChars < 500 {
			return errors.New("agentteams.max_task_prompt_chars must be at least 500")
		}
		if c.AgentTeams.ControllerURL != "" {
			parsed, err := url.Parse(c.AgentTeams.ControllerURL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return errors.New("agentteams.controller_url must be an http(s) URL")
			}
			if c.AgentTeams.HeartbeatInterval.Duration() <= 0 {
				return errors.New("agentteams.heartbeat_interval must be positive")
			}
		}
		if c.AgentTeams.MatrixURL != "" {
			parsed, err := url.Parse(c.AgentTeams.MatrixURL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return errors.New("agentteams.matrix_url must be an http(s) URL")
			}
			if c.AgentTeams.RuntimeConfigFile == "" {
				return errors.New("agentteams.runtime_config_file is required when Matrix is enabled")
			}
			if c.AgentTeams.MatrixSyncTimeout.Duration() < time.Second {
				return errors.New("agentteams.matrix_sync_timeout must be at least 1s")
			}
			if c.AgentTeams.MatrixSinceFile == "" {
				return errors.New("agentteams.matrix_since_file is required when Matrix is enabled")
			}
		}
	}
	if strings.TrimSpace(c.Office.Directory) == "" ||
		strings.TrimSpace(c.Office.OrderDirectory) == "" {
		return errors.New("office.directory and office.order_directory are required")
	}
	if c.Office.MaxStages < 1 || c.Office.MaxStages > 64 ||
		c.Office.MaxContextEntries < 1 || c.Office.MaxContextEntries > 256 {
		return errors.New("office.max_stages must be 1..64 and max_context_entries must be 1..256")
	}
	if c.Office.ApprovalTTL.Duration() < time.Minute || c.Office.ApprovalTTL.Duration() > 24*time.Hour {
		return errors.New("office.approval_ttl must be between 1m and 24h")
	}
	if c.Office.AttemptLease.Duration() < 10*time.Second || c.Office.AttemptLease.Duration() > 24*time.Hour {
		return errors.New("office.attempt_lease must be between 10s and 24h")
	}
	if c.Office.ReaperInterval.Duration() < time.Second ||
		c.Office.ReaperInterval.Duration() > c.Office.AttemptLease.Duration() {
		return errors.New("office.reaper_interval must be between 1s and office.attempt_lease")
	}
	if c.HTTP.MaxBodyBytes < 1 || c.HTTP.MaxConcurrent < 1 {
		return errors.New("http.max_body_bytes and http.max_concurrent must be positive")
	}
	if c.HTTP.PeerRetries < 0 || c.HTTP.PeerRetries > 10 || c.HTTP.RetryBackoff.Duration() < 0 {
		return errors.New("http.peer_retries must be 0..10 and retry_backoff cannot be negative")
	}
	for group, peers := range c.Groups {
		for _, peer := range peers {
			if err := validatePeer(fmt.Sprintf("group %q", group), peer); err != nil {
				return err
			}
		}
	}
	for _, peer := range c.Nested.Children {
		if err := validatePeer("nested.children", peer); err != nil {
			return err
		}
	}
	return nil
}

func validatePeer(location string, peer Peer) error {
	if strings.TrimSpace(peer.Name) == "" {
		return fmt.Errorf("%s contains a peer without a name", location)
	}
	parsed, err := url.Parse(peer.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s peer %q has invalid http(s) URL", location, peer.Name)
	}
	return nil
}

func (c Config) LogLevel() slog.Level {
	switch strings.ToLower(c.Log) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
