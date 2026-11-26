# Fault Strategies & Assertions

## Fault Injection Strategies

| Strategy | Behavior | Bugs It Catches |
| :--- | :--- | :--- |
| `commit_then_fail` | Forwards the request to upstream, verifies upstream returned `success.status` (durable commit), then returns an injected `http_error` (e.g. `502`) or `timeout` (`hold_ms`) to the agent. | Duplicate charges, double orders, missing idempotency keys when retrying after ambiguous failures. |
| `fail_before_upstream` | Immediately returns an injected `http_error` (e.g. `500` / `503`) without calling upstream. | Broken saga rollbacks, leaked locks/reservations, unhandled downstream errors. |
| `malformed_json` | Returns HTTP `200` with a truncated/corrupted JSON body to the agent. | Unhandled parse crashes, infinite retry loops on corrupt tool payloads. |

## State & Budget Assertions

| Assertion Type | Tier | Purpose |
| :--- | :--- | :--- |
| `sql_row_count` | OSS + Pro | Runs a parameterized PostgreSQL `SELECT COUNT(*)` query and asserts exact `expected` count. |
| `operation_attempts` | OSS + Pro | Verifies the agent actually retried after a fault (using `correlation_header` and `min_after_injection`), preventing false passes from agents that silently abort. |
| `max_tool_calls` | OSS + Pro | Caps matching proxied HTTP calls (`max_calls`) to catch retry storms and token-burning loops. |
| `redis_key_absent` | Pro / Team | Asserts a Redis key (e.g. distributed lock) does not exist after the run. |
| `redis_key_count` | Pro / Team | Uses deduplicated Redis `SCAN` on `pattern` and asserts exact `expected` key count. |
| `http_state_count` | Pro / Team | Queries an HTTP state endpoint returning a JSON integer (e.g. `1`) and asserts exact `expected` count. |
