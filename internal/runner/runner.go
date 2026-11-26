package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-fault-check/afc/internal/assertions"
	"github.com/agent-fault-check/afc/internal/config"
	"github.com/agent-fault-check/afc/internal/faults"
	"github.com/agent-fault-check/afc/internal/proxy"
)

const maxChildOutputBytes = 256 << 10 // 256 KiB

// ScenarioStatus classifies the outcome of a single scenario.
type ScenarioStatus string

const (
	StatusPassed  ScenarioStatus = "passed"
	StatusFailed  ScenarioStatus = "failed"
	StatusError   ScenarioStatus = "error"
	StatusSkipped ScenarioStatus = "skipped"
)

// ScenarioResult captures the full outcome of running one scenario.
type ScenarioResult struct {
	ID               string              `json:"id"`
	RunID            string              `json:"run_id,omitempty"`
	LineNumber       int                 `json:"line_number,omitempty"`
	FaultLineNumbers map[string]int      `json:"fault_line_numbers,omitempty"`
	Status           ScenarioStatus      `json:"status"`
	DurationMs       int64               `json:"duration_ms"`
	Assertions       []assertions.Result `json:"assertions,omitempty"`
	CallTimeline     []string            `json:"call_timeline,omitempty"`
	Unfired          []string            `json:"unfired_faults,omitempty"`
	BudgetAbort      bool                `json:"budget_aborted,omitempty"`
	AgentFailed      bool                `json:"agent_failed,omitempty"`
	Error            string              `json:"error,omitempty"`
	Teardown         string              `json:"teardown_error,omitempty"`
}

// SuiteResult captures the aggregate outcome of an afc run invocation.
type SuiteResult struct {
	SpecName    string           `json:"spec_name"`
	SpecPath    string           `json:"spec_path"`
	ToolVersion string           `json:"tool_version,omitempty"`
	StartedAt   time.Time        `json:"started_at"`
	FinishedAt  time.Time        `json:"finished_at"`
	ExitCode    int              `json:"exit_code"`
	Scenarios   []ScenarioResult `json:"scenarios"`
}

// RunSuite executes selected scenarios sequentially and returns the suite result.
func RunSuite(ctx context.Context, spec *config.Spec, specPath string, scenarioFilter string) SuiteResult {
	specDir := filepath.Dir(specPath)
	res := SuiteResult{
		SpecName:  spec.Name,
		SpecPath:  specPath,
		StartedAt: time.Now().UTC(),
	}

	infraHalted := false
	hasFailure := false

	for i := range spec.Scenarios {
		sc := &spec.Scenarios[i]
		if scenarioFilter != "" && sc.ID != scenarioFilter {
			continue
		}

		if infraHalted || ctx.Err() != nil {
			res.Scenarios = append(res.Scenarios, ScenarioResult{
				ID:         sc.ID,
				LineNumber: spec.LineNumbers[sc.ID],
				Status:     StatusSkipped,
				Error:      "skipped due to earlier infrastructure error or cancellation",
			})
			continue
		}

		scRes := runScenario(ctx, spec, specDir, sc)
		scRes.LineNumber = spec.LineNumbers[sc.ID]
		if len(sc.Faults) > 0 {
			flines := make(map[string]int, len(sc.Faults))
			for _, f := range sc.Faults {
				if ln := spec.LineNumbers[sc.ID+"/"+f.ID]; ln > 0 {
					flines[f.ID] = ln
				}
			}
			if len(flines) > 0 {
				scRes.FaultLineNumbers = flines
			}
		}
		res.Scenarios = append(res.Scenarios, scRes)

		switch scRes.Status {
		case StatusError:
			infraHalted = true
			res.ExitCode = 2
		case StatusFailed:
			hasFailure = true
		}
	}

	if res.ExitCode == 0 && hasFailure {
		res.ExitCode = 1
	}
	if ctx.Err() != nil {
		res.ExitCode = 2
	}
	res.FinishedAt = time.Now().UTC()
	return res
}

func runScenario(ctx context.Context, spec *config.Spec, specDir string, sc *config.Scenario) (out ScenarioResult) {
	start := time.Now()
	runID, err := generateRunID()
	if err != nil {
		return ScenarioResult{ID: sc.ID, Status: StatusError, Error: fmt.Sprintf("generate run id: %v", err)}
	}
	out = ScenarioResult{
		ID:    sc.ID,
		RunID: runID,
	}

	// Always run bounded, idempotent teardown once execution begins, and redact
	// any secrets that may appear in store/driver/teardown/assertion errors.
	scenarioStarted := false
	defer func() {
		out.DurationMs = time.Since(start).Milliseconds()
		if scenarioStarted && len(spec.Fixture.TeardownArgv) > 0 {
			tdCtx, tdCancel := context.WithTimeout(context.Background(), time.Duration(spec.Fixture.TeardownTimeoutSeconds)*time.Second)
			defer tdCancel()
			if tdErr := runArgv(tdCtx, spec.Fixture.TeardownArgv, specDir, buildFixtureEnv(spec, sc.ID, runID, ""), nil); tdErr != nil {
				out.Teardown = tdErr.Error()
				if out.Status == StatusPassed || out.Status == StatusFailed {
					out.Status = StatusError
					if out.Error == "" {
						out.Error = fmt.Sprintf("teardown failed: %v", tdErr)
					}
				}
			}
		}
		out.Error = RedactSecrets(out.Error)
		out.Teardown = RedactSecrets(out.Teardown)
		for i := range out.Assertions {
			out.Assertions[i].Message = RedactSecrets(out.Assertions[i].Message)
		}
	}()

	// 1. Fixture setup (optional)
	scenarioStarted = true
	if len(spec.Fixture.SetupArgv) > 0 {
		setupCtx, setupCancel := context.WithTimeout(ctx, time.Duration(spec.Fixture.SetupTimeoutSeconds)*time.Second)
		err = runArgv(setupCtx, spec.Fixture.SetupArgv, specDir, buildFixtureEnv(spec, sc.ID, runID, ""), nil)
		setupCancel()
		if err != nil {
			out.Status = StatusError
			out.Error = fmt.Sprintf("fixture setup failed: %v", err)
			return out
		}
	}

	// 2. Store readiness checks (only for stores referenced by this scenario)
	checkedStores := make(map[string]bool)
	for _, a := range sc.Assertions {
		if a.Store == "" || checkedStores[a.Store] {
			continue
		}
		checkedStores[a.Store] = true
		if st, ok := spec.StateStores[a.Store]; ok {
			if err := assertions.CheckStoreReady(ctx, st); err != nil {
				out.Status = StatusError
				out.Error = fmt.Sprintf("store %q readiness failed: %v", a.Store, err)
				return out
			}
		}
	}

	// 3. Start loopback HTTP proxy
	corrHeaders := collectCorrelationHeaders(sc)
	eng := faults.NewEngine(sc.Faults)
	trace := proxy.NewTraceLog(runID, sc.ID, proxy.DefaultMaxTraceEvents)
	px, err := proxy.NewHTTPProxy(proxy.Options{
		ListenAddr:          spec.Proxy.ListenAddr,
		UpstreamURL:         spec.Proxy.UpstreamURL,
		UpstreamTimeout:     time.Duration(spec.Proxy.UpstreamTimeoutMs) * time.Millisecond,
		Engine:              eng,
		Trace:               trace,
		AbortAfterToolCalls: sc.AbortAfterToolCalls,
		CorrelationHeaders:  corrHeaders,
	})
	if err != nil {
		out.Status = StatusError
		out.Error = fmt.Sprintf("start proxy: %v", err)
		return out
	}

	// 4. Launch agent with stdin JSON prompt
	agentDir := specDir
	if spec.Agent.WorkingDir != "" {
		if filepath.IsAbs(spec.Agent.WorkingDir) {
			agentDir = spec.Agent.WorkingDir
		} else {
			agentDir = filepath.Join(specDir, spec.Agent.WorkingDir)
		}
	}
	promptPayload, _ := json.Marshal(struct {
		Prompt string `json:"prompt"`
		RunID  string `json:"run_id"`
	}{
		Prompt: sc.Prompt,
		RunID:  runID,
	})
	promptPayload = append(promptPayload, '\n')

	agentCtx, agentCancel := context.WithTimeout(ctx, time.Duration(spec.Agent.TimeoutSeconds)*time.Second)
	agentErr := runArgv(agentCtx, spec.Agent.Argv, agentDir, buildAgentEnv(spec, sc, runID, px.URL()), promptPayload)
	agentCancel()

	// 5. Drain proxy before quiesce/assertions so no writes remain in flight.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
	drainErr := px.Shutdown(drainCtx)
	drainCancel()

	// Check for cancellation, drain failure, or launch/infra errors before evaluating assertions.
	if ctx.Err() != nil {
		out.Status = StatusError
		out.Error = fmt.Sprintf("run cancelled: %v", ctx.Err())
		return out
	}
	if drainErr != nil {
		out.Status = StatusError
		out.Error = fmt.Sprintf("proxy drain failed: %v", drainErr)
		return out
	}
	if execErr := asLaunchError(agentErr); execErr != nil {
		out.Status = StatusError
		out.Error = fmt.Sprintf("agent launch failed: %v", execErr)
		return out
	}

	// 6. Quiesce fixture (proxy is already shut down, so do not pass dead proxy URL)
	if len(spec.Fixture.QuiesceArgv) > 0 {
		qCtx, qCancel := context.WithTimeout(ctx, time.Duration(spec.Fixture.QuiesceTimeoutSeconds)*time.Second)
		qErr := runArgv(qCtx, spec.Fixture.QuiesceArgv, specDir, buildFixtureEnv(spec, sc.ID, runID, ""), nil)
		qCancel()
		if qErr != nil {
			out.Status = StatusError
			out.Error = fmt.Sprintf("fixture quiesce failed: %v", qErr)
			return out
		}
	}

	// 7. Check proxy-level infra errors
	if uErr := eng.UnknownCommitError(); uErr != nil {
		out.Status = StatusError
		out.Error = uErr.Error()
		return out
	}
	if bErr := px.BodyOversizeError(); bErr != nil {
		out.Status = StatusError
		out.Error = bErr.Error()
		return out
	}

	// 8. Evaluate assertions & capture sanitized call timeline
	out.CallTimeline = summarizeTrace(trace)
	aResults := assertions.EvaluateAll(ctx, assertions.EvalContext{
		RunID:    runID,
		Spec:     spec,
		Scenario: sc,
		Engine:   eng,
		Trace:    trace,
	})
	out.Assertions = aResults

	if ctx.Err() != nil {
		out.Status = StatusError
		out.Error = fmt.Sprintf("run cancelled: %v", ctx.Err())
		return out
	}
	for _, ar := range aResults {
		if ar.InfraErr {
			out.Status = StatusError
			out.Error = ar.Message
			return out
		}
	}

	// 9. Determine scenario pass/fail, preserving all failure reasons including agent stderr.
	var failures []string
	if px.BudgetAborted() {
		out.BudgetAbort = true
		failures = append(failures, fmt.Sprintf("live tool-call budget (%d) exceeded", sc.AbortAfterToolCalls))
	}
	if unfired := eng.UnfiredFaults(); len(unfired) > 0 {
		out.Unfired = unfired
		failures = append(failures, fmt.Sprintf("declared faults never fired: %v", unfired))
	}
	for _, ar := range aResults {
		if !ar.Passed {
			failures = append(failures, ar.Message)
		}
	}
	if agentErr != nil {
		out.AgentFailed = true
		failures = append(failures, fmt.Sprintf("agent failed: %v", agentErr))
	}
	if len(failures) > 0 {
		out.Status = StatusFailed
		out.Error = strings.Join(failures, "; ")
		return out
	}

	out.Status = StatusPassed
	return out
}

func summarizeTrace(trace *proxy.TraceLog) []string {
	events, truncated := trace.Snapshot()
	type callKey struct {
		method string
		path   string
		idx    int
	}
	order := make([]callKey, 0)
	seen := make(map[callKey]bool)
	steps := make(map[callKey][]string)
	for _, ev := range events {
		k := callKey{ev.Method, ev.Path, ev.CallIndex}
		if !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
		switch ev.Event {
		case proxy.EventUpstreamOK:
			steps[k] = append(steps[k], fmt.Sprintf("upstream %d", ev.Status))
		case proxy.EventUpstreamNonMatch:
			steps[k] = append(steps[k], fmt.Sprintf("upstream %d (non-match)", ev.Status))
		case proxy.EventUpstreamError:
			steps[k] = append(steps[k], "upstream error")
		case proxy.EventFaultInjected:
			steps[k] = append(steps[k], fmt.Sprintf("injected %d [%s]", ev.Status, ev.FaultID))
		case proxy.EventFaultUnfired:
			steps[k] = append(steps[k], fmt.Sprintf("fault unfired [%s]", ev.FaultID))
		case proxy.EventBudgetAborted:
			steps[k] = append(steps[k], fmt.Sprintf("budget aborted (%d)", ev.Status))
		}
	}
	const maxTimelineLines = 20
	out := make([]string, 0, len(order))
	for i, k := range order {
		if i >= maxTimelineLines {
			out = append(out, fmt.Sprintf("... (%d more calls omitted)", len(order)-maxTimelineLines))
			break
		}
		desc := strings.Join(steps[k], " -> ")
		if desc == "" {
			desc = "admitted"
		}
		out = append(out, fmt.Sprintf("#%d %s %s -> %s", k.idx, k.method, k.path, desc))
	}
	if truncated {
		out = append(out, "(trace log truncated at event cap)")
	}
	return out
}

func runArgv(ctx context.Context, argv []string, dir string, env []string, stdinBytes []byte) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = 2 * time.Second
	configureProcGroup(cmd)

	if stdinBytes != nil {
		cmd.Stdin = bytes.NewReader(stdinBytes)
	}
	stdoutBuf := &boundedBuffer{limit: maxChildOutputBytes}
	stderrBuf := &boundedBuffer{limit: maxChildOutputBytes}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return &launchError{err: err}
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%w (stderr: %s)", err, RedactSecrets(strings.TrimSpace(stderrBuf.String())))
		}
		return nil
	case <-ctx.Done():
		killProcGroup(cmd, done)
		return fmt.Errorf("process timed out or cancelled: %w", ctx.Err())
	}
}

type launchError struct {
	err error
}

func (e *launchError) Error() string { return e.err.Error() }
func (e *launchError) Unwrap() error { return e.err }

func asLaunchError(err error) error {
	var le *launchError
	if errors.As(err, &le) {
		return le
	}
	return nil
}

type boundedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	rem := b.limit - b.buf.Len()
	if rem > 0 {
		if len(p) > rem {
			_, _ = b.buf.Write(p[:rem])
		} else {
			_, _ = b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return b.buf.String()
}

var baselineHostEnvs = []string{
	"PATH",
	"HOME",
	"USER",
	"TMPDIR",
	"TMP",
	"TEMP",
	"LANG",
	"LC_ALL",
	"SSL_CERT_FILE",
	"SSL_CERT_DIR",
	"SYSTEMROOT",
}

func baselineEnvMap() map[string]string {
	envMap := make(map[string]string, len(baselineHostEnvs)+4)
	for _, k := range baselineHostEnvs {
		if v, ok := os.LookupEnv(k); ok {
			envMap[k] = v
		}
	}
	if _, ok := envMap["PATH"]; !ok {
		envMap["PATH"] = os.Getenv("PATH")
	}
	return envMap
}

func buildAgentEnv(spec *config.Spec, sc *config.Scenario, runID, proxyURL string) []string {
	envMap := baselineEnvMap()
	for _, k := range spec.Agent.EnvAllowlist {
		if v, ok := os.LookupEnv(k); ok {
			envMap[k] = v
		}
	}
	for k, v := range spec.Agent.Env {
		envMap[k] = v
	}
	envMap["AFC_RUN_ID"] = runID
	envMap["AFC_SCENARIO_ID"] = sc.ID
	envMap["AFC_PROMPT"] = sc.Prompt
	envMap["TOOL_API_BASE_URL"] = proxyURL
	return flattenEnvMap(envMap)
}

func buildFixtureEnv(spec *config.Spec, scenarioID, runID, proxyURL string) []string {
	envMap := baselineEnvMap()
	for _, k := range spec.Fixture.EnvAllowlist {
		if v, ok := os.LookupEnv(k); ok {
			envMap[k] = v
		}
	}
	for k, v := range spec.Fixture.Env {
		envMap[k] = v
	}
	for _, st := range spec.StateStores {
		if st.DSNEnv != "" {
			if v, ok := os.LookupEnv(st.DSNEnv); ok {
				envMap[st.DSNEnv] = v
			}
		}
		if st.URLEnv != "" {
			if v, ok := os.LookupEnv(st.URLEnv); ok {
				envMap[st.URLEnv] = v
			}
		}
	}
	envMap["AFC_RUN_ID"] = runID
	envMap["AFC_SCENARIO_ID"] = scenarioID
	if proxyURL != "" {
		envMap["TOOL_API_BASE_URL"] = proxyURL
	}
	return flattenEnvMap(envMap)
}

func flattenEnvMap(m map[string]string) []string {
	env := make([]string, 0, len(m))
	for k, v := range m {
		env = append(env, k+"="+v)
	}
	return env
}

func collectCorrelationHeaders(sc *config.Scenario) []string {
	var hdrs []string
	for _, a := range sc.Assertions {
		if a.Type == "operation_attempts" && a.CorrelationHeader != "" {
			hdrs = append(hdrs, a.CorrelationHeader)
		}
	}
	return hdrs
}

func generateRunID() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
