package config

import (
	"strings"
	"testing"
)

const validSpecYAML = `version: "1.0"
name: "checkout-recovery"

agent:
  argv: ["python3", "examples/checkout/run_agent.py"]
  timeout_seconds: 60

fixture:
  setup_argv: ["python3", "examples/checkout/fixture.py", "setup"]
  quiesce_argv: ["python3", "examples/checkout/fixture.py", "quiesce"]
  teardown_argv: ["python3", "examples/checkout/fixture.py", "teardown"]

proxy:
  transport: "http"
  upstream_url: "http://127.0.0.1:8000"

state_stores:
  orders:
    type: "postgres"
    dsn_env: "AFC_TEST_POSTGRES_DSN"

scenarios:
  - id: "duplicate-write-on-502"
    prompt: "Create a $49.00 order."
    faults:
      - id: "lose-first-response"
        strategy: "commit_then_fail"
        match:
          method: "POST"
          path: "/api/v1/orders"
          call: 1
        success:
          status: 201
          means_durable_commit: true
        inject:
          kind: "http_error"
          status: 502
    assertions:
      - type: "operation_attempts"
        fault: "lose-first-response"
        correlation_header: "X-Operation-ID"
        min_after_injection: 1
      - type: "sql_row_count"
        store: "orders"
        query: "SELECT COUNT(*) FROM orders WHERE run_id = $1"
        args:
          - runtime: "run_id"
        expected: 1
      - type: "max_tool_calls"
        match:
          method: "POST"
          path: "/api/v1/orders"
        max_calls: 3
`

func TestParseValidSpec(t *testing.T) {
	spec, err := Parse([]byte(validSpecYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Proxy.ListenAddr != DefaultListenAddr {
		t.Fatalf("expected default listen addr %q, got %q", DefaultListenAddr, spec.Proxy.ListenAddr)
	}
	if spec.LineNumbers["duplicate-write-on-502"] == 0 {
		t.Fatalf("expected non-zero line number for scenario")
	}
	if spec.LineNumbers["duplicate-write-on-502/lose-first-response"] == 0 {
		t.Fatalf("expected non-zero line number for fault")
	}
}

func TestParseRejections(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unsupported version",
			yaml: strings.Replace(validSpecYAML, `version: "1.0"`, `version: "2.0"`, 1),
			want: "unsupported version",
		},
		{
			name: "unknown field",
			yaml: validSpecYAML + "\nextra_field: 123\n",
			want: "extra_field not found",
		},
		{
			name: "duplicate yaml key",
			yaml: strings.Replace(validSpecYAML, `name: "checkout-recovery"`, "name: \"a\"\nname: \"b\"", 1),
			want: "duplicate YAML key",
		},
		{
			name: "yaml alias",
			yaml: "version: \"1.0\"\nname: &n \"x\"\nscenarios:\n  - id: *n\n",
			want: "YAML aliases are not allowed",
		},
		{
			name: "multi document",
			yaml: validSpecYAML + "\n---\nversion: \"1.0\"\n",
			want: "multi-document",
		},
		{
			name: "non-loopback listen addr",
			yaml: strings.Replace(validSpecYAML, `transport: "http"`, "transport: \"http\"\n  listen_addr: \"0.0.0.0:8080\"", 1),
			want: "loopback",
		},
		{
			name: "upstream embedded creds",
			yaml: strings.Replace(validSpecYAML, "http://127.0.0.1:8000", "http://user:pass@127.0.0.1:8000", 1),
			want: "embedded credentials",
		},
		{
			name: "reserved env override",
			yaml: strings.Replace(validSpecYAML, "timeout_seconds: 60", "timeout_seconds: 60\n  env:\n    AFC_RUN_ID: \"bad\"", 1),
			want: "reserved variable",
		},
		{
			name: "invalid scenario id",
			yaml: strings.Replace(validSpecYAML, "duplicate-write-on-502", "Bad_ID", 1),
			want: "must match",
		},
		{
			name: "duplicate fault selector",
			yaml: strings.Replace(validSpecYAML, "    assertions:", `      - id: "second-fault"
        strategy: "fail_before_upstream"
        match:
          method: "POST"
          path: "/api/v1/orders"
          call: 1
        inject:
          kind: "http_error"
          status: 500
    assertions:`, 1),
			want: "duplicate fault selector",
		},
		{
			name: "commit_then_fail without means_durable_commit",
			yaml: strings.Replace(validSpecYAML, "means_durable_commit: true", "means_durable_commit: false", 1),
			want: "means_durable_commit",
		},
		{
			name: "timeout without hold_ms",
			yaml: strings.Replace(validSpecYAML, "kind: \"http_error\"\n          status: 502", "kind: \"timeout\"", 1),
			want: "hold_ms",
		},
		{
			name: "fail_before_upstream with hold_ms",
			yaml: strings.Replace(validSpecYAML, "strategy: \"commit_then_fail\"\n        match:\n          method: \"POST\"\n          path: \"/api/v1/orders\"\n          call: 1\n        success:\n          status: 201\n          means_durable_commit: true\n        inject:\n          kind: \"http_error\"\n          status: 502", "strategy: \"fail_before_upstream\"\n        match:\n          method: \"POST\"\n          path: \"/api/v1/orders\"\n          call: 1\n        inject:\n          kind: \"http_error\"\n          status: 502\n          hold_ms: 100", 1),
			want: "hold_ms",
		},
		{
			name: "max_tool_calls with query string in path",
			yaml: strings.Replace(validSpecYAML, "          method: \"POST\"\n          path: \"/api/v1/orders\"\n        max_calls: 3", "          method: \"POST\"\n          path: \"/api/v1/orders?foo=bar\"\n        max_calls: 3", 1),
			want: "query",
		},
		{
			name: "store dsn_env references reserved AFC_LICENSE_KEY",
			yaml: strings.Replace(validSpecYAML, `dsn_env: "AFC_TEST_POSTGRES_DSN"`, `dsn_env: "AFC_LICENSE_KEY"`, 1),
			want: "reserved variable",
		},
		{
			name: "http store missing host",
			yaml: strings.Replace(validSpecYAML, "  orders:\n    type: \"postgres\"\n    dsn_env: \"AFC_TEST_POSTGRES_DSN\"", "  orders:\n    type: \"postgres\"\n    dsn_env: \"AFC_TEST_POSTGRES_DSN\"\n  badhttp:\n    type: \"http\"\n    base_url: \"http://\"", 1),
			want: "base_url",
		},
		{
			name: "fixture reserved env override",
			yaml: strings.Replace(validSpecYAML, `teardown_argv: ["python3", "examples/checkout/fixture.py", "teardown"]`, "teardown_argv: [\"python3\", \"examples/checkout/fixture.py\", \"teardown\"]\n  env_allowlist: [\"AFC_LICENSE_KEY\"]", 1),
			want: "reserved variable",
		},
		{
			name: "extraneous field on max_tool_calls",
			yaml: strings.Replace(validSpecYAML, "max_calls: 3", "max_calls: 3\n        query: \"SELECT 1\"", 1),
			want: "does not accept",
		},
		{
			name: "invalid sql runtime arg placeholder",
			yaml: strings.Replace(validSpecYAML, `runtime: "run_id"`, `runtime: "bad_var"`, 1),
			want: "unsupported runtime placeholder",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got: %v", tc.want, err)
			}
		})
	}
}
