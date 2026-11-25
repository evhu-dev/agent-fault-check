package report

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fault-check/afc/internal/assertions"
	"github.com/agent-fault-check/afc/internal/runner"
)

func TestWriteConsoleAndAtomicWrite(t *testing.T) {
	var buf bytes.Buffer
	res := runner.SuiteResult{
		SpecName: "demo",
		SpecPath: "chaos-spec.yaml",
		ExitCode: 1,
		Scenarios: []runner.ScenarioResult{
			{
				ID:         "s-pass",
				Status:     runner.StatusPassed,
				DurationMs: 12,
				Assertions: []assertions.Result{{Type: "max_tool_calls", Passed: true, Message: "ok"}},
			},
			{
				ID:         "s-fail",
				Status:     runner.StatusFailed,
				DurationMs: 34,
				Error:      "mismatch",
				Teardown:   "td fail",
				Assertions: []assertions.Result{
					{Type: "sql_row_count", Passed: false, Message: "expected 1 got 2"},
					{Type: "http_state_count", InfraErr: true, Message: "conn refused"},
				},
			},
		},
	}

	WriteConsole(&buf, res, false)
	out := buf.String()
	if strings.Contains(out, "max_tool_calls") {
		t.Fatalf("non-verbose output should not list passing scenario assertions: %s", out)
	}
	if !strings.Contains(out, "FAIL (sql_row_count)") || !strings.Contains(out, "ERR (http_state_count)") || !strings.Contains(out, "teardown error: td fail") || !strings.Contains(out, "Summary: 1 passed, 1 failed, 0 errors, 0 skipped") || !strings.Contains(out, "hint:") {
		t.Fatalf("unexpected console output:\n%s", out)
	}

	dest := filepath.Join(t.TempDir(), "sub", "report.txt")
	if err := AtomicWrite(dest, []byte("hello")); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "hello" {
		t.Fatalf("unexpected AtomicWrite content: %q err=%v", string(got), err)
	}
}
