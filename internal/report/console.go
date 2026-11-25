package report

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/agent-fault-check/afc/internal/runner"
)

// WriterFunc writes a formatted report atomically to outPath.
type WriterFunc func(res runner.SuiteResult, outPath string) error

var (
	mu           sync.RWMutex
	proReporters = make(map[string]WriterFunc)
)

// RegisterProReporter registers a paid report writer (called by -tags pro builds).
func RegisterProReporter(format string, fn WriterFunc) {
	mu.Lock()
	defer mu.Unlock()
	proReporters[format] = fn
}

// WriteConsole prints a human-readable, actionable summary of the suite execution.
func WriteConsole(w io.Writer, res runner.SuiteResult, verbose bool) {
	fmt.Fprintf(w, "Spec: %s (%s)\n", res.SpecName, res.SpecPath)

	var passed, failed, errored, skipped int
	var totalMs int64

	for _, sc := range res.Scenarios {
		totalMs += sc.DurationMs
		switch sc.Status {
		case runner.StatusPassed:
			passed++
		case runner.StatusFailed:
			failed++
		case runner.StatusError:
			errored++
		case runner.StatusSkipped:
			skipped++
		}

		if sc.RunID != "" && (verbose || sc.Status != runner.StatusPassed) {
			fmt.Fprintf(w, "  [%s] %s (%d ms, run_id=%s)\n", sc.Status, sc.ID, sc.DurationMs, sc.RunID)
		} else {
			fmt.Fprintf(w, "  [%s] %s (%d ms)\n", sc.Status, sc.ID, sc.DurationMs)
		}

		if verbose || sc.Status != runner.StatusPassed {
			if len(sc.CallTimeline) > 0 {
				fmt.Fprintln(w, "    timeline:")
				for _, line := range sc.CallTimeline {
					fmt.Fprintf(w, "      %s\n", line)
				}
			}
			hasFailedAssertion := false
			for _, a := range sc.Assertions {
				mark := "PASS"
				if a.InfraErr {
					mark = "ERR"
					hasFailedAssertion = true
				} else if !a.Passed {
					mark = "FAIL"
					hasFailedAssertion = true
				}
				fmt.Fprintf(w, "    - %s (%s): %s\n", mark, a.Type, a.Message)
			}
			if sc.Error != "" && (!hasFailedAssertion || sc.AgentFailed || sc.BudgetAbort || len(sc.Unfired) > 0 || sc.Status == runner.StatusError) {
				fmt.Fprintf(w, "    error: %s\n", sc.Error)
			}
			if sc.Status == runner.StatusFailed {
				if hint := RemediationHint(sc); hint != "" {
					fmt.Fprintf(w, "    hint: %s\n", hint)
				}
			}
		} else if sc.Error != "" {
			fmt.Fprintf(w, "    error: %s\n", sc.Error)
		}
		if sc.Teardown != "" {
			fmt.Fprintf(w, "    teardown error: %s\n", sc.Teardown)
		}
	}
	fmt.Fprintf(w, "Summary: %d passed, %d failed, %d errors, %d skipped (total %d ms)\n", passed, failed, errored, skipped, totalMs)
	fmt.Fprintf(w, "Exit code: %d\n", res.ExitCode)
}

// RemediationHint returns an actionable one-line fix suggestion for a failed scenario.
func RemediationHint(sc runner.ScenarioResult) string {
	if sc.BudgetAbort {
		return "Add retry caps, exponential backoff, and loop guards to prevent runaway tool-call loops."
	}
	if len(sc.Unfired) > 0 {
		return "Verify the agent invoked the targeted HTTP method and path, or update the fault selector in chaos-spec.yaml."
	}
	for _, a := range sc.Assertions {
		if a.Passed || a.InfraErr {
			continue
		}
		switch a.Type {
		case "sql_row_count", "http_state_count", "redis_key_count", "redis_key_absent":
			return "Ensure retries reuse a stable idempotency key and upstream deduplicates writes (or verify saga compensation releases prior steps)."
		case "operation_attempts":
			return "Verify the agent retries transient tool failures with the same correlation header instead of silently giving up."
		case "max_tool_calls":
			return "Bound retry attempts and add backoff/circuit-breaker limits on repeated tool calls."
		}
	}
	if sc.AgentFailed {
		return "Inspect the agent exit status/stderr above and ensure handled recovery paths exit 0."
	}
	return ""
}

// WriteProReport writes a registered Pro report format (sarif or junit).
func WriteProReport(format string, res runner.SuiteResult, outPath string) error {
	mu.RLock()
	fn := proReporters[format]
	mu.RUnlock()
	if fn == nil {
		return fmt.Errorf("report format %q is not available in this build (requires Pro distribution)", format)
	}
	return fn(res, outPath)
}

// AtomicWrite writes data to a temporary file in the target directory and renames it atomically.
func AtomicWrite(outPath string, data []byte) error {
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".afc-report-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, outPath)
}
