# Troubleshooting Guide

## `AFC104: Declared fault never fired`

Your agent did not send a request matching `faults[].match` through the proxy. Check:
- Your agent's HTTP client reads `TOOL_API_BASE_URL` as its base URL (not hardcoded `localhost:8000`).
- `match.method` (e.g. `POST`), `match.path` (exact path without query string), and `match.call` (1-indexed) match what appears in the call timeline with `--verbose`.

## `commit_then_fail` fails with upstream status mismatch (`AFC201`)

`commit_then_fail` verifies the upstream API actually committed (returned `success.status`, e.g. `201`) before injecting the fault. If your upstream returns `200` instead of `201`, update `success.status: 200`.

## `report-mode 'sarif' is not supported by the public OSS release`

In the GitHub Action, SARIF and JUnit require the Pro binary. Set:
- `release-repository: "evhu-dev/agent-fault-check-releases"`
- `license-key: ${{ secrets.AFC_LICENSE_KEY }}`

## `entitlement error: license key rejected by Polar (HTTP 404)`

Verify the key has no trailing whitespace/newlines, belongs to an active Pro or Team subscription in the Polar Customer Portal, and that your runner has outbound HTTPS access to `api.polar.sh`.

## `http_state_count` returns unmarshal error

Ensure your HTTP state endpoint returns a JSON integer (`1`) or `{"count": 1}`.

## Postgres or Redis connection refused in CI

Ensure your CI service container has a healthcheck (e.g. `pg_isready` / `redis-cli ping`) and that `dsn_env` / `url_env` matches the exact environment variable exported to the step.

## `commit_then_fail` with `kind: timeout` reports `AFC104` / `504`

If the proxy returns `504` (`fault hold expired before client disconnect`), your agent's HTTP client timeout is longer than `inject.hold_ms`. Either lower your agent's tool-call timeout below `hold_ms` (or raise `hold_ms`), or switch to `inject: { kind: http_error, status: 504 }` if you want to inject an immediate gateway timeout.
