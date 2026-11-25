# Security and Trust Boundary

## 1. Disposable Test Environments Only
`agent-fault-check` (`afc`) is designed exclusively for local and CI test harnesses. **Never point `proxy.upstream_url` or `state_stores` at production services or production databases.**

## 2. Untrusted Pull Requests in CI
`chaos-spec.yaml` specifies executable `argv` vectors (`agent.argv`, `fixture.*_argv`) and SQL queries:
- Treat `chaos-spec.yaml` as **trusted code**, not untrusted user input.
- Never run PR-controlled specs in privileged workflows (such as `pull_request_target` with secrets).
- Never pass `AFC_LICENSE_KEY` or deployment secrets to untrusted fork runs.

## 3. Process & Network Controls
- **No implicit shell:** Commands are executed via direct `exec.Command(argv[0], argv[1:]...)` in isolated process groups (`Setpgid: true`).
- **Minimal child env:** Only `PATH` and explicit `env_allowlist` variables are inherited; `AFC_LICENSE_KEY` is never exposed to child processes.
- **Loopback-only proxy:** `proxy.listen_addr` rejects non-loopback interfaces, and `proxy.upstream_url` rejects embedded credentials, queries, and redirects.
- **Read-only Postgres assertions:** `sql_row_count` executes inside a read-only transaction (`pgx.TxOptions{AccessMode: pgx.ReadOnly}`).

## 4. Sanitized Traces & License Cache
- Proxy trace logs record metadata only (`run_id`, `scenario_id`, `method`, `path`, `call_index`, `fault_id`, `status`, correlation header value) and omit payloads, raw headers, prompts, and DSNs.
- Local license entitlements are cached in `~/.afc/license.cache` with `0600` permissions and bound to a SHA-256 fingerprint of `(key, organization_id, product_id)`.
