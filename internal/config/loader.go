package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MaxSpecBytes          = 1 << 20 // 1 MiB
	DefaultAgentTimeoutS  = 60
	DefaultUpstreamMs     = 5000
	DefaultSetupTimeoutS  = 30
	DefaultQuiesceTimeout = 5
	DefaultTeardownS      = 10
	DefaultListenAddr     = "127.0.0.1:0"
)

var (
	idRegex      = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	reservedEnvs = map[string]bool{
		"AFC_RUN_ID":        true,
		"AFC_SCENARIO_ID":   true,
		"AFC_PROMPT":        true,
		"TOOL_API_BASE_URL": true,
		"AFC_LICENSE_KEY":   true,
	}
)

// LoadFile reads, parses, and strictly validates a chaos-spec.yaml file.
func LoadFile(path string) (*Spec, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat spec %q: %w", path, err)
	}
	if info.Size() > MaxSpecBytes {
		return nil, fmt.Errorf("spec %q exceeds maximum size of %d bytes", path, MaxSpecBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spec %q: %w", path, err)
	}
	if len(data) > MaxSpecBytes {
		return nil, fmt.Errorf("spec %q exceeds maximum size of %d bytes", path, MaxSpecBytes)
	}
	return Parse(data)
}

// Parse parses and strictly validates YAML bytes into a Spec.
func Parse(data []byte) (*Spec, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("multi-document YAML streams are not supported")
	}

	if err := rejectAliasesAndDuplicateKeys(&doc); err != nil {
		return nil, err
	}

	var spec Spec
	strictDec := yaml.NewDecoder(bytes.NewReader(data))
	strictDec.KnownFields(true)
	if err := strictDec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode spec: %w", err)
	}

	applyDefaults(&spec)

	if err := validateSpec(&spec); err != nil {
		return nil, err
	}
	extractSourceLocations(&doc, &spec)
	return &spec, nil
}

func rejectAliasesAndDuplicateKeys(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.AliasNode {
		return fmt.Errorf("line %d: YAML aliases are not allowed", node.Line)
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]int, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i]
			if prevLine, ok := seen[k.Value]; ok {
				return fmt.Errorf("line %d: duplicate YAML key %q (first defined on line %d)", k.Line, k.Value, prevLine)
			}
			seen[k.Value] = k.Line
		}
	}
	for _, child := range node.Content {
		if err := rejectAliasesAndDuplicateKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func extractSourceLocations(doc *yaml.Node, spec *Spec) {
	spec.LineNumbers = make(map[string]int)
	if doc == nil || len(doc.Content) == 0 {
		return
	}
	scenarios := mappingValue(doc.Content[0], "scenarios")
	if scenarios == nil || scenarios.Kind != yaml.SequenceNode {
		return
	}
	scenarioIndex := 0
	for _, scNode := range scenarios.Content {
		// YAML decoding skips null entries in these slices of structs.
		if scNode.ShortTag() == "!!null" {
			continue
		}
		if scenarioIndex >= len(spec.Scenarios) {
			break
		}
		sc := &spec.Scenarios[scenarioIndex]
		scenarioIndex++
		spec.LineNumbers[sc.ID] = scNode.Line
		if faultsNode := mappingValue(scNode, "faults"); faultsNode != nil && faultsNode.Kind == yaml.SequenceNode {
			for _, fNode := range faultsNode.Content {
				if fID := mappingScalar(fNode, "id"); fID != "" {
					spec.LineNumbers[sc.ID+"/"+fID] = fNode.Line
				}
			}
		}
		if assertionsNode := mappingValue(scNode, "assertions"); assertionsNode != nil && assertionsNode.Kind == yaml.SequenceNode {
			assertionIndex := 0
			for _, aNode := range assertionsNode.Content {
				if aNode.ShortTag() == "!!null" {
					continue
				}
				if assertionIndex >= len(sc.Assertions) {
					break
				}
				a := &sc.Assertions[assertionIndex]
				assertionIndex++
				a.LineNumber, a.ColumnNumber = aNode.Line, aNode.Column
			}
		}
	}
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func mappingScalar(node *yaml.Node, key string) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1].Value
		}
	}
	return ""
}

func applyDefaults(s *Spec) {
	if s.Agent.TimeoutSeconds == 0 {
		s.Agent.TimeoutSeconds = DefaultAgentTimeoutS
	}
	if s.Fixture.SetupTimeoutSeconds == 0 {
		s.Fixture.SetupTimeoutSeconds = DefaultSetupTimeoutS
	}
	if s.Fixture.QuiesceTimeoutSeconds == 0 {
		s.Fixture.QuiesceTimeoutSeconds = DefaultQuiesceTimeout
	}
	if s.Fixture.TeardownTimeoutSeconds == 0 {
		s.Fixture.TeardownTimeoutSeconds = DefaultTeardownS
	}
	if s.Proxy.ListenAddr == "" {
		s.Proxy.ListenAddr = DefaultListenAddr
	}
	if s.Proxy.UpstreamTimeoutMs == 0 {
		s.Proxy.UpstreamTimeoutMs = DefaultUpstreamMs
	}
}

func validateSpec(s *Spec) error {
	if s.Version != "1.0" {
		return fmt.Errorf("unsupported version %q: only \"1.0\" is supported", s.Version)
	}
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is required")
	}
	if err := validateAgent(&s.Agent); err != nil {
		return err
	}
	if err := validateFixture(&s.Fixture); err != nil {
		return err
	}
	if err := validateProxy(&s.Proxy); err != nil {
		return err
	}
	if err := validateStores(s.StateStores); err != nil {
		return err
	}
	if len(s.Scenarios) == 0 {
		return errors.New("scenarios must be non-empty")
	}

	seenScenarios := make(map[string]bool, len(s.Scenarios))
	for i := range s.Scenarios {
		sc := &s.Scenarios[i]
		if !idRegex.MatchString(sc.ID) {
			return fmt.Errorf("scenario[%d] id %q must match %s", i, sc.ID, idRegex.String())
		}
		if seenScenarios[sc.ID] {
			return fmt.Errorf("duplicate scenario id %q", sc.ID)
		}
		seenScenarios[sc.ID] = true

		if err := validateScenario(sc, s.StateStores); err != nil {
			return fmt.Errorf("scenario %q: %w", sc.ID, err)
		}
	}
	return nil
}

func validateEnvConfig(prefix string, allowlist []string, envMap map[string]string) error {
	for _, k := range allowlist {
		if strings.TrimSpace(k) == "" || strings.Contains(k, "=") {
			return fmt.Errorf("%s.env_allowlist contains invalid variable name %q", prefix, k)
		}
		if reservedEnvs[k] {
			return fmt.Errorf("%s.env_allowlist cannot include reserved variable %q", prefix, k)
		}
	}
	for k := range envMap {
		if strings.TrimSpace(k) == "" || strings.Contains(k, "=") {
			return fmt.Errorf("%s.env contains invalid variable name %q", prefix, k)
		}
		if reservedEnvs[k] {
			return fmt.Errorf("%s.env cannot override reserved variable %q", prefix, k)
		}
	}
	return nil
}

func validateAgent(a *AgentConfig) error {
	if len(a.Argv) == 0 || strings.TrimSpace(a.Argv[0]) == "" {
		return errors.New("agent.argv must contain a non-empty executable")
	}
	if a.TimeoutSeconds <= 0 {
		return errors.New("agent.timeout_seconds must be positive")
	}
	return validateEnvConfig("agent", a.EnvAllowlist, a.Env)
}

func validateFixture(f *FixtureConfig) error {
	if len(f.SetupArgv) > 0 && strings.TrimSpace(f.SetupArgv[0]) == "" {
		return errors.New("fixture.setup_argv executable cannot be empty")
	}
	if len(f.QuiesceArgv) > 0 && strings.TrimSpace(f.QuiesceArgv[0]) == "" {
		return errors.New("fixture.quiesce_argv executable cannot be empty")
	}
	if len(f.TeardownArgv) > 0 && strings.TrimSpace(f.TeardownArgv[0]) == "" {
		return errors.New("fixture.teardown_argv executable cannot be empty")
	}
	if f.SetupTimeoutSeconds <= 0 || f.QuiesceTimeoutSeconds <= 0 || f.TeardownTimeoutSeconds <= 0 {
		return errors.New("fixture timeouts must be positive")
	}
	return validateEnvConfig("fixture", f.EnvAllowlist, f.Env)
}

func validateProxy(p *ProxyConfig) error {
	if p.Transport != "http" {
		return fmt.Errorf("unsupported proxy.transport %q: v1 supports \"http\" only", p.Transport)
	}
	host, _, err := net.SplitHostPort(p.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid proxy.listen_addr %q: %w", p.ListenAddr, err)
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("proxy.listen_addr %q must use a loopback address", p.ListenAddr)
	}
	u, err := url.Parse(p.UpstreamURL)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("proxy.upstream_url %q must be an absolute http(s) URL", p.UpstreamURL)
	}
	if u.User != nil {
		return errors.New("proxy.upstream_url must not contain embedded credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("proxy.upstream_url must not contain query or fragment")
	}
	if p.UpstreamTimeoutMs <= 0 {
		return errors.New("proxy.upstream_timeout_ms must be positive")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateStores(stores map[string]StoreEntry) error {
	for name, st := range stores {
		switch st.Type {
		case "postgres":
			if strings.TrimSpace(st.DSNEnv) == "" || strings.Contains(st.DSNEnv, "=") {
				return fmt.Errorf("state_stores.%s: dsn_env is required for postgres", name)
			}
			if reservedEnvs[st.DSNEnv] {
				return fmt.Errorf("state_stores.%s: dsn_env cannot reference reserved variable %q", name, st.DSNEnv)
			}
		case "redis":
			if strings.TrimSpace(st.URLEnv) == "" || strings.Contains(st.URLEnv, "=") {
				return fmt.Errorf("state_stores.%s: url_env is required for redis", name)
			}
			if reservedEnvs[st.URLEnv] {
				return fmt.Errorf("state_stores.%s: url_env cannot reference reserved variable %q", name, st.URLEnv)
			}
		case "http":
			u, err := url.Parse(st.BaseURL)
			if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
				return fmt.Errorf("state_stores.%s: base_url must be a valid http(s) URL with host and without credentials", name)
			}
			if u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("state_stores.%s: base_url must not contain query or fragment", name)
			}
		default:
			return fmt.Errorf("state_stores.%s: unsupported type %q", name, st.Type)
		}
	}
	return nil
}

func validateScenario(sc *Scenario, stores map[string]StoreEntry) error {
	if strings.TrimSpace(sc.Prompt) == "" {
		return errors.New("prompt must be non-empty")
	}
	if sc.AbortAfterToolCalls < 0 {
		return errors.New("abort_after_tool_calls cannot be negative")
	}
	if len(sc.Faults) == 0 {
		return errors.New("at least one fault is required")
	}
	if len(sc.Assertions) == 0 {
		return errors.New("at least one assertion is required")
	}

	faultIDs := make(map[string]bool, len(sc.Faults))
	type selector struct {
		method string
		path   string
		call   int
	}
	selectors := make(map[selector]bool, len(sc.Faults))

	for i := range sc.Faults {
		f := &sc.Faults[i]
		if !idRegex.MatchString(f.ID) {
			return fmt.Errorf("fault[%d] id %q must match %s", i, f.ID, idRegex.String())
		}
		if faultIDs[f.ID] {
			return fmt.Errorf("duplicate fault id %q", f.ID)
		}
		faultIDs[f.ID] = true

		if err := validateFault(f); err != nil {
			return fmt.Errorf("fault %q: %w", f.ID, err)
		}
		sel := selector{f.Match.Method, f.Match.Path, f.Match.Call}
		if selectors[sel] {
			return fmt.Errorf("duplicate fault selector (%s %s call=%d)", sel.method, sel.path, sel.call)
		}
		selectors[sel] = true
	}

	for i := range sc.Assertions {
		if err := validateAssertion(&sc.Assertions[i], faultIDs, stores); err != nil {
			return fmt.Errorf("assertion[%d]: %w", i, err)
		}
	}
	return nil
}

var httpMethodRegex = regexp.MustCompile(`^[A-Z]+$`)

func normalizeMatchPath(raw string) (string, error) {
	if !strings.HasPrefix(raw, "/") || strings.Contains(raw, "?") || strings.Contains(raw, "#") {
		return "", fmt.Errorf("path %q must start with '/' and exclude query parameters or fragments", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid path %q: %w", raw, err)
	}
	return u.EscapedPath(), nil
}

func validateFault(f *FaultRule) error {
	if !httpMethodRegex.MatchString(f.Match.Method) {
		return fmt.Errorf("match.method %q must be a non-empty uppercase HTTP method", f.Match.Method)
	}
	normPath, err := normalizeMatchPath(f.Match.Path)
	if err != nil {
		return fmt.Errorf("match.%w", err)
	}
	f.Match.Path = normPath
	if f.Match.Call < 1 {
		return errors.New("match.call must be >= 1")
	}

	switch f.Strategy {
	case "commit_then_fail":
		if f.Success == nil || !f.Success.MeansDurableCommit || f.Success.Status < 200 || f.Success.Status > 299 {
			return errors.New("commit_then_fail requires success.status (2xx) and success.means_durable_commit: true")
		}
		if f.Inject == nil {
			return errors.New("commit_then_fail requires inject")
		}
		switch f.Inject.Kind {
		case "http_error":
			if f.Inject.Status < 400 || f.Inject.Status > 599 {
				return errors.New("inject.status must be 400..599 for http_error")
			}
			if f.Inject.HoldMs != 0 {
				return errors.New("inject.hold_ms is only valid for kind: timeout")
			}
		case "timeout":
			if f.Inject.HoldMs <= 0 {
				return errors.New("inject.hold_ms must be positive for kind: timeout")
			}
			if f.Inject.Status != 0 {
				return errors.New("inject.status is not valid for kind: timeout")
			}
		default:
			return fmt.Errorf("unsupported inject.kind %q for commit_then_fail", f.Inject.Kind)
		}
	case "fail_before_upstream":
		if f.Success != nil {
			return errors.New("fail_before_upstream does not accept success")
		}
		if f.Inject == nil || f.Inject.Kind != "http_error" || f.Inject.Status < 400 || f.Inject.Status > 599 {
			return errors.New("fail_before_upstream requires inject.kind: http_error and status 400..599")
		}
		if f.Inject.HoldMs != 0 {
			return errors.New("inject.hold_ms is not valid for fail_before_upstream")
		}
	case "malformed_json":
		if f.Success != nil {
			return errors.New("malformed_json does not accept success")
		}
		if f.Inject != nil {
			return errors.New("malformed_json does not accept inject")
		}
	default:
		return fmt.Errorf("unsupported strategy %q", f.Strategy)
	}
	return nil
}

func validateAssertion(a *Assertion, faultIDs map[string]bool, stores map[string]StoreEntry) error {
	switch a.Type {
	case "operation_attempts":
		if a.Store != "" || a.Query != "" || len(a.Args) > 0 || a.Expected != nil || a.Match != nil || a.MaxCalls != nil || a.Pattern != "" || a.Key != "" || a.Path != "" {
			return errors.New("operation_attempts does not accept store/query/args/expected/match/max_calls/pattern/key/path")
		}
		if !faultIDs[a.Fault] {
			return fmt.Errorf("unknown fault %q", a.Fault)
		}
		if strings.TrimSpace(a.CorrelationHeader) == "" {
			return errors.New("correlation_header is required")
		}
		if a.MinAfterInjection < 1 {
			return errors.New("min_after_injection must be >= 1")
		}
	case "sql_row_count":
		if a.Fault != "" || a.CorrelationHeader != "" || a.MinAfterInjection != 0 || a.Match != nil || a.MaxCalls != nil || a.Pattern != "" || a.Key != "" || a.Path != "" {
			return errors.New("sql_row_count does not accept fault/correlation_header/min_after_injection/match/max_calls/pattern/key/path")
		}
		st, ok := stores[a.Store]
		if !ok || st.Type != "postgres" {
			return fmt.Errorf("store %q must reference a declared postgres store", a.Store)
		}
		if strings.TrimSpace(a.Query) == "" {
			return errors.New("query is required")
		}
		if err := validateSQLArgs(a.Args); err != nil {
			return err
		}
		if a.Expected == nil || *a.Expected < 0 {
			return errors.New("expected must be a non-negative integer")
		}
	case "max_tool_calls":
		if a.Fault != "" || a.CorrelationHeader != "" || a.MinAfterInjection != 0 || a.Store != "" || a.Query != "" || len(a.Args) > 0 || a.Expected != nil || a.Pattern != "" || a.Key != "" || a.Path != "" {
			return errors.New("max_tool_calls does not accept fault/correlation_header/min_after_injection/store/query/args/expected/pattern/key/path")
		}
		if a.Match == nil || !httpMethodRegex.MatchString(a.Match.Method) {
			return errors.New("match.method (uppercase) and match.path (starting with '/' and excluding query parameters) are required")
		}
		normPath, err := normalizeMatchPath(a.Match.Path)
		if err != nil {
			return fmt.Errorf("match.%w", err)
		}
		a.Match.Path = normPath
		if a.MaxCalls == nil || *a.MaxCalls < 0 {
			return errors.New("max_calls must be a non-negative integer")
		}
	case "redis_key_count":
		if a.Fault != "" || a.CorrelationHeader != "" || a.MinAfterInjection != 0 || a.Query != "" || len(a.Args) > 0 || a.Match != nil || a.MaxCalls != nil || a.Key != "" || a.Path != "" {
			return errors.New("redis_key_count does not accept fault/correlation_header/min_after_injection/query/args/match/max_calls/key/path")
		}
		st, ok := stores[a.Store]
		if !ok || st.Type != "redis" {
			return fmt.Errorf("store %q must reference a declared redis store", a.Store)
		}
		if strings.TrimSpace(a.Pattern) == "" {
			return errors.New("pattern is required")
		}
		if a.Expected == nil || *a.Expected < 0 {
			return errors.New("expected must be a non-negative integer")
		}
	case "redis_key_absent":
		if a.Fault != "" || a.CorrelationHeader != "" || a.MinAfterInjection != 0 || a.Query != "" || len(a.Args) > 0 || a.Expected != nil || a.Match != nil || a.MaxCalls != nil || a.Pattern != "" || a.Path != "" {
			return errors.New("redis_key_absent does not accept fault/correlation_header/min_after_injection/query/args/expected/match/max_calls/pattern/path")
		}
		st, ok := stores[a.Store]
		if !ok || st.Type != "redis" {
			return fmt.Errorf("store %q must reference a declared redis store", a.Store)
		}
		if strings.TrimSpace(a.Key) == "" {
			return errors.New("key is required")
		}
	case "http_state_count":
		if a.Fault != "" || a.CorrelationHeader != "" || a.MinAfterInjection != 0 || a.Query != "" || len(a.Args) > 0 || a.Match != nil || a.MaxCalls != nil || a.Pattern != "" || a.Key != "" {
			return errors.New("http_state_count does not accept fault/correlation_header/min_after_injection/query/args/match/max_calls/pattern/key")
		}
		st, ok := stores[a.Store]
		if !ok || st.Type != "http" {
			return fmt.Errorf("store %q must reference a declared http store", a.Store)
		}
		if !strings.HasPrefix(a.Path, "/") {
			return errors.New("path must start with '/'")
		}
		if a.Expected == nil || *a.Expected < 0 {
			return errors.New("expected must be a non-negative integer")
		}
	default:
		return fmt.Errorf("unsupported assertion type %q", a.Type)
	}
	return nil
}

func validateSQLArgs(args []any) error {
	for i, v := range args {
		if m, ok := v.(map[string]any); ok {
			if len(m) != 1 {
				return fmt.Errorf("args[%d]: map argument must contain only {\"runtime\": \"run_id\"}", i)
			}
			rt, ok := m["runtime"].(string)
			if !ok || rt != "run_id" {
				return fmt.Errorf("args[%d]: unsupported runtime placeholder %v (only \"run_id\" is supported)", i, m["runtime"])
			}
		}
	}
	return nil
}
