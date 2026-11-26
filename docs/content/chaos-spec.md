# `chaos-spec.yaml` Reference

Below is a complete annotated spec showing both OSS Core and Pro/Team options:

```yaml
version: "1.0"
name: "checkout-recovery"

agent:
  argv: ["python3", "run_agent.py"]
  timeout_seconds: 30
  env:
    AGENT_MODE: "idempotent"
  env_allowlist: ["OPENAI_API_KEY"]

fixture:
  setup_argv: ["python3", "fixture.py", "setup"]
  quiesce_argv: ["python3", "fixture.py", "quiesce"]
  teardown_argv: ["python3", "fixture.py", "teardown"]

proxy:
  transport: "http"
  listen_addr: "127.0.0.1:0"          # :0 picks an ephemeral free port
  upstream_url: "http://127.0.0.1:8000"
  upstream_timeout_ms: 5000

state_stores:
  orders:
    type: "postgres"
    dsn_env: "AFC_TEST_POSTGRES_DSN"
  cache:                              # Pro / Team
    type: "redis"
    url_env: "AFC_TEST_REDIS_URL"
  ledger:                             # Pro / Team
    type: "http"
    base_url: "http://127.0.0.1:8000"

scenarios:
  - id: "duplicate-write-on-502"
    prompt: "Create a $49.00 order for customer cust_123."
    abort_after_tool_calls: 5         # Hard-kill runaway agent loops
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
          kind: "http_error"          # or "timeout" with hold_ms: 2500
          status: 502
    assertions:
      - type: "operation_attempts"
        fault: "lose-first-response"
        correlation_header: "X-Operation-ID"
        min_after_injection: 1
      - type: "sql_row_count"
        store: "orders"
        query: "SELECT COUNT(*) FROM orders WHERE run_id = $1 AND customer_id = $2"
        args: [{ runtime: "run_id" }, "cust_123"]
        expected: 1
      - type: "redis_key_absent"      # Pro / Team
        store: "cache"
        key: "order:lock:{{run_id}}"
      - type: "http_state_count"      # Pro / Team
        store: "ledger"
        path: "/state/orders?run_id={{run_id}}"
        expected: 1
      - type: "max_tool_calls"
        match: { method: "POST", path: "/api/v1/orders" }
        max_calls: 3
```

## Runtime Placeholders

- **SQL `args`:** Pass `{ runtime: "run_id" }` so it is safely bound as a parameterized query argument (`$1`).
- **Redis keys/patterns & HTTP state paths:** Use `{{run_id}}`, which `afc` expands to the scenario's unique `AFC_RUN_ID`.
