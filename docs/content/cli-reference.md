# CLI Reference & Exit Codes

## Commands

| Command | Flags | Description |
| :--- | :--- | :--- |
| `afc version` | — | Prints binary version, edition (`-pro`), commit hash, and supported spec version. |
| `afc validate` | `--spec <path>` | Validates `chaos-spec.yaml` syntax, fault selectors, and assertion fields without running agents. |
| `afc activate` | `--license-key <key>` | Validates a Polar Pro/Team license key (or reads `AFC_LICENSE_KEY`) and prints the activated tier. |
| `afc run` | `--spec`, `--scenario`, `--license-key`, `--sarif`, `--junit`, `--verbose` | Executes all scenarios (or a single scenario via `--scenario`) and writes configured reports. |

## Exit Codes

| Exit Code | Meaning | CI Merge Gate Behavior |
| :--- | :--- | :--- |
| `0` | All scenarios and assertions passed. | PR merge gate passes. |
| `1` | One or more chaos assertions failed, budget exceeded, fault unfired, or agent exited non-zero. | PR merge gate fails (agent reliability regression). |
| `2` | Config/CLI error, missing/invalid license entitlement, or infrastructure/fixture error. | Pipeline error (fix config, secrets, or test service). |
