package config

// Spec represents the validated chaos-spec.yaml configuration.
type Spec struct {
	Version     string                `yaml:"version"`
	Name        string                `yaml:"name"`
	Agent       AgentConfig           `yaml:"agent"`
	Fixture     FixtureConfig         `yaml:"fixture"`
	Proxy       ProxyConfig           `yaml:"proxy"`
	StateStores map[string]StoreEntry `yaml:"state_stores"`
	Scenarios   []Scenario            `yaml:"scenarios"`

	// LineNumbers maps scenario IDs (and "scenarioID/faultID") to 1-based YAML line numbers.
	LineNumbers map[string]int `yaml:"-"`
}

// AgentConfig defines how to launch the system-under-test agent process.
type AgentConfig struct {
	Argv           []string          `yaml:"argv"`
	WorkingDir     string            `yaml:"working_dir"`
	TimeoutSeconds int               `yaml:"timeout_seconds"`
	EnvAllowlist   []string          `yaml:"env_allowlist"`
	Env            map[string]string `yaml:"env"`
}

// FixtureConfig defines lifecycle hooks for initializing, quiescing, and tearing down test state.
type FixtureConfig struct {
	SetupArgv              []string          `yaml:"setup_argv"`
	QuiesceArgv            []string          `yaml:"quiesce_argv"`
	TeardownArgv           []string          `yaml:"teardown_argv"`
	SetupTimeoutSeconds    int               `yaml:"setup_timeout_seconds"`
	QuiesceTimeoutSeconds  int               `yaml:"quiesce_timeout_seconds"`
	TeardownTimeoutSeconds int               `yaml:"teardown_timeout_seconds"`
	EnvAllowlist           []string          `yaml:"env_allowlist"`
	Env                    map[string]string `yaml:"env"`
}

// ProxyConfig defines the fault-injecting reverse proxy.
type ProxyConfig struct {
	Transport         string `yaml:"transport"`
	ListenAddr        string `yaml:"listen_addr"`
	UpstreamURL       string `yaml:"upstream_url"`
	UpstreamTimeoutMs int    `yaml:"upstream_timeout_ms"`
}

// StoreEntry defines a state store used for readiness checks and assertions.
type StoreEntry struct {
	Type    string `yaml:"type"`
	DSNEnv  string `yaml:"dsn_env"`
	URLEnv  string `yaml:"url_env"`
	BaseURL string `yaml:"base_url"`
}

// Scenario defines a single chaos test scenario.
type Scenario struct {
	ID                  string      `yaml:"id"`
	Prompt              string      `yaml:"prompt"`
	AbortAfterToolCalls int         `yaml:"abort_after_tool_calls"`
	Faults              []FaultRule `yaml:"faults"`
	Assertions          []Assertion `yaml:"assertions"`
}

// FaultRule defines a deterministic fault injection rule.
type FaultRule struct {
	ID       string         `yaml:"id"`
	Strategy string         `yaml:"strategy"`
	Match    FaultMatch     `yaml:"match"`
	Success  *SuccessSpec   `yaml:"success"`
	Inject   *InjectSpec    `yaml:"inject"`
}

// FaultMatch selects a specific HTTP call by method, path, and 1-based admission index.
type FaultMatch struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	Call   int    `yaml:"call"`
}

// SuccessSpec declares the upstream durable-commit contract for commit_then_fail.
type SuccessSpec struct {
	Status             int  `yaml:"status"`
	MeansDurableCommit bool `yaml:"means_durable_commit"`
}

// InjectSpec defines the synthetic failure returned to the agent.
type InjectSpec struct {
	Kind   string `yaml:"kind"`
	Status int    `yaml:"status"`
	HoldMs int    `yaml:"hold_ms"`
}

// Assertion defines a post-run invariant check.
type Assertion struct {
	Type              string     `yaml:"type"`
	Fault             string     `yaml:"fault"`
	CorrelationHeader string     `yaml:"correlation_header"`
	MinAfterInjection int        `yaml:"min_after_injection"`
	Store             string     `yaml:"store"`
	Query             string     `yaml:"query"`
	Args              []any      `yaml:"args"`
	Expected          *int       `yaml:"expected"`
	Match             *CallMatch `yaml:"match"`
	MaxCalls          *int       `yaml:"max_calls"`
	Pattern           string     `yaml:"pattern"`
	Key               string     `yaml:"key"`
	Path              string     `yaml:"path"`

	LineNumber   int `yaml:"-" json:"-"`
	ColumnNumber int `yaml:"-" json:"-"`
}

// CallMatch selects HTTP calls for budget assertions.
type CallMatch struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
}
