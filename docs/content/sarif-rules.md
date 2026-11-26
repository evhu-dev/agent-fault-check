# SARIF 2.1.0 Rule Reference (Pro / Team)

Each SARIF finding includes deterministic SHA-256 fingerprints, remediation markdown, and execution timelines for GitHub Security / PR annotations.

| Rule ID | Name | Meaning |
| :--- | :--- | :--- |
| `AFC101` | `PersistentStateMismatch` | Post-run Postgres, Redis, or HTTP state did not match expected count (duplicate write or leaked rollback state). |
| `AFC102` | `MissingRetryAfterFault` | Agent did not retry after an injected fault using the expected correlation header. |
| `AFC103` | `ToolCallBudgetExceeded` | Agent exceeded `max_tool_calls` or hit `abort_after_tool_calls`. |
| `AFC104` | `DeclaredFaultUnfired` | A declared fault rule never matched any proxied HTTP request. |
| `AFC105` | `AgentExecutionFailed` | Agent process exited non-zero or exceeded `timeout_seconds`. |
| `AFC201` | `ChaosRunnerInfraError` | Fixture hook, state store connection, proxy drain, or upstream verification error. |
