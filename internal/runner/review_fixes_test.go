package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/agent-fault-check/afc/internal/config"
	"github.com/agent-fault-check/afc/internal/proxy"
)

func TestSummarizeTraceDeduplicatesPerCall(t *testing.T) {
	tl := proxy.NewTraceLog("run-1", "sc-1", 100)
	// Simulate full lifecycle for call #1 and call #2
	tl.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 1, FaultID: "f1", Event: proxy.EventAdmitted})
	tl.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 1, FaultID: "f1", Event: proxy.EventForwarded})
	tl.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 1, FaultID: "f1", Event: proxy.EventUpstreamOK, Status: 201})
	tl.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 1, FaultID: "f1", Event: proxy.EventFaultInjected, Status: 502})
	tl.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 1, FaultID: "f1", Event: proxy.EventCompleted, Status: 502})

	tl.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 2, Event: proxy.EventAdmitted})
	tl.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 2, Event: proxy.EventForwarded})
	tl.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 2, Event: proxy.EventUpstreamOK, Status: 201})
	tl.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 2, Event: proxy.EventCompleted, Status: 201})

	lines := summarizeTrace(tl)
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 timeline lines (1 per call), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "#1 POST /orders -> upstream 201 -> injected 502 [f1]") {
		t.Fatalf("unexpected line[0]: %s", lines[0])
	}
	if !strings.Contains(lines[1], "#2 POST /orders -> upstream 201") {
		t.Fatalf("unexpected line[1]: %s", lines[1])
	}
}

func TestBuildAgentEnvIncludesBaselineAndPromptVars(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home")
	spec := &config.Spec{}
	sc := &config.Scenario{ID: "sc-env", Prompt: "pay $10"}
	env := buildAgentEnv(spec, sc, "run-xyz", "http://127.0.0.1:9999")

	got := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			got[k] = v
		}
	}
	if got["HOME"] != "/tmp/test-home" {
		t.Fatalf("expected baseline HOME to be preserved, got %q", got["HOME"])
	}
	if got["AFC_SCENARIO_ID"] != "sc-env" || got["AFC_PROMPT"] != "pay $10" || got["AFC_RUN_ID"] != "run-xyz" || got["TOOL_API_BASE_URL"] != "http://127.0.0.1:9999" {
		t.Fatalf("missing expected runtime env vars: %+v", got)
	}
}

func TestRedactSecretsExtendedPatterns(t *testing.T) {
	raw := "polar=polar_oat_1234567890abcdef1234 ant=sk-ant-1234567890abcdef1234 aws=AKIAIOSFODNN7EXAMPLE slack=xoxb-1234567890-abcdef"
	redacted := RedactSecrets(raw)
	for _, leak := range []string{"1234567890abcdef1234", "AKIAIOSFODNN7EXAMPLE", "xoxb-1234567890-abcdef"} {
		if strings.Contains(redacted, leak) {
			t.Fatalf("expected %q to be redacted in %q", leak, redacted)
		}
	}
}

func TestRunScenarioOptionalFixtureAndRedactedStoreError(t *testing.T) {
	t.Setenv("BAD_PG_DSN", "postgres://admin:supersecretpw@127.0.0.1:1/db?sslmode=disable")
	yamlSpec := `version: "1.0"
name: "no-fixture-spec"
agent:
  argv: ["true"]
  timeout_seconds: 5
proxy:
  transport: "http"
  upstream_url: "http://127.0.0.1:8000"
state_stores:
  orders:
    type: "postgres"
    dsn_env: "BAD_PG_DSN"
scenarios:
  - id: "check-redact"
    prompt: "test"
    faults:
      - id: "f1"
        strategy: "fail_before_upstream"
        match: { method: "POST", path: "/x", call: 1 }
        inject: { kind: "http_error", status: 500 }
    assertions:
      - type: "sql_row_count"
        store: "orders"
        query: "SELECT 1"
        expected: 1
`
	spec, err := config.Parse([]byte(yamlSpec))
	if err != nil {
		t.Fatalf("expected spec without fixture block to parse cleanly, got: %v", err)
	}
	res := RunSuite(context.Background(), spec, "chaos-spec.yaml", "")
	if res.ExitCode != 2 || len(res.Scenarios) != 1 || res.Scenarios[0].Status != StatusError {
		t.Fatalf("expected exit 2 infra error on unreachable postgres, got %+v", res)
	}
	if strings.Contains(res.Scenarios[0].Error, "supersecretpw") {
		t.Fatalf("expected postgres password to be redacted in scenario error, got: %q", res.Scenarios[0].Error)
	}
}
