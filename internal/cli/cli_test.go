package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-fault-check/afc/internal/report"
	"github.com/agent-fault-check/afc/internal/runner"
)

func TestReportWriteFailureDoesNotSkipOtherFormat(t *testing.T) {
	dir := t.TempDir()
	if err := report.WriteProReport("junit", runner.SuiteResult{}, filepath.Join(dir, "probe.xml")); err != nil {
		t.Skipf("requires Pro reporters: %v", err)
	}
	junit := filepath.Join(dir, "result.xml")
	if code := writeRequestedReports(runner.SuiteResult{ExitCode: 1}, dir, junit); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if _, err := os.Stat(junit); err != nil {
		t.Fatalf("JUnit report was not written: %v", err)
	}
}

func TestCLIBasicAndPositionalArgRejection(t *testing.T) {
	if code := Run(nil); code != 2 {
		t.Fatalf("expected exit 2 with no args, got %d", code)
	}
	if code := Run([]string{"version"}); code != 0 {
		t.Fatalf("expected exit 0 for version, got %d", code)
	}
	if code := Run([]string{"unknown"}); code != 2 {
		t.Fatalf("expected exit 2 for unknown command, got %d", code)
	}
	if code := Run([]string{"validate", "--spec", "foo.yaml", "extra"}); code != 2 {
		t.Fatalf("expected exit 2 for unexpected positional arg in validate, got %d", code)
	}
	if code := Run([]string{"activate", "extra"}); code != 2 {
		t.Fatalf("expected exit 2 for unexpected positional arg in activate, got %d", code)
	}
	if code := Run([]string{"run", "--spec", "foo.yaml", "extra"}); code != 2 {
		t.Fatalf("expected exit 2 for unexpected positional arg in run, got %d", code)
	}
}
