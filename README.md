# agent-fault-check (`afc`)

**`agent-fault-check` (`afc`)** is a Go CLI and GitHub Action that chaos-tests AI agents against disposable **HTTP tool APIs** before code merges to production *(MCP-stdio transport is planned for a future release)*. It catches:
- **Duplicate writes** when a tool call commits upstream and fails or times out on the response (`commit_then_fail`).
- **Broken saga rollbacks** when a downstream step fails mid-workflow (`fail_before_upstream`).
- **Malformed tool payloads & retry loops** (`malformed_json`, `operation_attempts`, `max_tool_calls`, and live `abort_after_tool_calls`).

---

## Quickstart

### 1. Build or Install
```bash
go build -o afc ./cmd/afc
```

### 2. Validate a Spec
```bash
afc validate --spec examples/checkout/chaos-spec.yaml
```

### 3. Run Locally
Start Postgres and the checkout fixture server, and ensure the host Python environment has `psycopg[binary]` (or `psycopg2`) installed for the fixture lifecycle hooks:
```bash
docker compose -f examples/checkout/docker-compose.yml up -d
python3 -m pip install "psycopg[binary]"
export AFC_TEST_POSTGRES_DSN="postgres://postgres:postgres@127.0.0.1:5432/agent_test?sslmode=disable"
afc run --spec examples/checkout/chaos-spec.yaml --verbose
```

---

## CLI Commands

| Command | Description |
| :--- | :--- |
| `afc validate --spec PATH` | Strictly validates `chaos-spec.yaml` with line numbers (no network or child processes). |
| `afc run --spec PATH [--scenario ID] [--sarif PATH] [--junit PATH] [--verbose]` | Runs scenarios sequentially, injects faults, verifies assertions, and writes reports. |
| `afc activate [--license-key KEY]` | Validates `AFC_LICENSE_KEY` (or `--license-key`) with Polar and caches entitlement in `~/.afc/license.cache`. |
| `afc version` | Prints version, commit SHA, and supported spec version. |

### Exit Codes
- `0`: All executed scenarios passed (every declared fault fired and every assertion passed).
- `1`: Scenario expectation failed (assertion mismatch, unfired fault, non-zero agent exit, or budget exceeded).
- `2`: Infrastructure or preflight error (invalid config, missing/invalid entitlement, store failure, or unknown upstream commit outcome).

---

## GitHub Action

### Free (Public OSS Release — Console Output)
```yaml
- uses: evhu-dev/agent-fault-check@v0.2.0
  with:
    spec-path: "examples/checkout/chaos-spec.yaml"
    version: "v0.2.0"
    report-mode: "console"
```

### Pro (Commercial Release — SARIF / JUnit)
Paid report modes (`sarif`, `junit`, `both`) require `release-repository` pointing to the public Pro binary release repository (`evhu-dev/agent-fault-check-releases`) along with a Polar license key:
```yaml
- uses: evhu-dev/agent-fault-check@v0.2.0
  with:
    spec-path: "examples/checkout/chaos-spec.yaml"
    version: "v0.2.0"
    release-repository: "evhu-dev/agent-fault-check-releases"
    report-mode: "sarif" # console | junit | sarif | both
    license-key: ${{ secrets.AFC_LICENSE_KEY }}
```

---

## Editions & Pricing

| Capability | Free (Apache-2.0) | [Pro ($29/mo · 7-day trial)](https://buy.polar.sh/polar_cl_sOS2ZlhspTGEIduyBMAXwPMExlM8WNzBK6Khk0FyC4p) | [Team ($99/mo · 7-day trial)](https://buy.polar.sh/polar_cl_EuoNFqEKjwiVY8lMUkx752HmKYTo6JShkHQ8L4PGhJa) |
| :--- | :--- | :--- | :--- |
| Repositories | Unlimited | Up to 3 repos | Unlimited |
| Scenarios per run | Up to 3 | Unlimited | Unlimited |
| HTTP proxy & all fault strategies | Included | Included | Included |
| Postgres, `operation_attempts`, `max_tool_calls` | Included | Included | Included |
| Redis & HTTP state assertion packs | — | Included | Included |
| SARIF 2.1.0 PR annotations & JUnit XML | — | Included | Included |
| Priority support & upcoming MCP-stdio / trace packs | — | — | Included |


See [docs/SECURITY.md](docs/SECURITY.md) for CI trust boundaries and security controls.
