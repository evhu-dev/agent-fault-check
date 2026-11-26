# How the Proxy & Runner Work

For each scenario in `chaos-spec.yaml`, `afc` executes a deterministic 6-step lifecycle inside your own environment:

1. **Fixture Setup:** Runs `fixture.setup_argv` to seed or reset disposable test state.
2. **Local Fault Proxy:** Starts an ephemeral HTTP reverse proxy on `127.0.0.1` forwarding to `proxy.upstream_url`.
3. **Agent Execution:** Spawns `agent.argv` in its own process group with injected environment variables so your agent routes HTTP tool calls through the proxy.
4. **Fault Injection & Tracing:** Matches configured HTTP requests, injects faults deterministically, and records a full call timeline.
5. **Quiesce & Assertions:** Runs `fixture.quiesce_argv`, then verifies database, Redis, HTTP state, retry, and tool-budget assertions.
6. **Teardown & Reporting:** Always runs `fixture.teardown_argv` (even on timeout), redacts secrets from logs, and writes Console, SARIF, and JUnit outputs.

## Environment Variables Injected Into Your Agent & Fixture

| Variable | Description |
| :--- | :--- |
| `TOOL_API_BASE_URL` | Base URL of the local fault-injection proxy (e.g. `http://127.0.0.1:54321`). Point your agent's tool API client here. |
| `AFC_RUN_ID` | Unique ID generated per scenario execution. Use it to isolate test rows/keys cleanly across parallel runs. |

> **Tip:** To pass extra host environment variables (such as `OPENAI_API_KEY`) into your agent or fixture, explicitly list them under `agent.env_allowlist` or `fixture.env_allowlist`.
