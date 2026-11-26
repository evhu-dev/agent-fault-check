# Frequently Asked Questions

### Does `agent-fault-check` work with any AI agent framework or language?
Yes. `afc` is completely framework- and language-agnostic. It launches your agent via `agent.argv` (Python, TypeScript/Node, Go, Java, etc.) and intercepts standard HTTP tool calls via `TOOL_API_BASE_URL`. It works with LangGraph, CrewAI, OpenAI Agents SDK, Anthropic SDK, AutoGen, or custom loops.

### Does `afc` call my LLM or replace my LLM mocks?
No. `afc` only proxies the HTTP tool APIs you point at `TOOL_API_BASE_URL`. You can run against a real LLM or a deterministic stubbed LLM depending on your CI budget.

### Should I point `afc` at a production database?
**Never.** Always run `afc` against disposable local or CI service containers (such as GitHub Actions Postgres/Redis service containers or local Docker Compose).

### Does the CLI collect telemetry or upload my code/prompts?
No. Both `afc` and `afc-pro` collect **zero usage telemetry** and never transmit prompts, source code, HTTP payloads, or database contents. Pro license validation contacts `api.polar.sh` solely to verify your license key.

### What is the difference between the OSS and Pro binaries?
The OSS binary (`afc`, Apache-2.0) supports up to 3 scenarios per run, all HTTP fault strategies, PostgreSQL + budget assertions, and console output. The Pro binary (`afc-pro`) adds unlimited scenarios, Redis and HTTP state assertions, and SARIF 2.1.0 + JUnit XML reporting.

### How do billing, upgrades, cancellations, and refunds work?
All billing is handled by **Polar.sh** (Merchant of Record). You can upgrade, download invoices, or cancel anytime in the Polar Customer Portal. New Pro and Team subscriptions include a 14-day refund policy — see [Commercial Terms & Refunds](./terms.html).

### Need help or found a bug?
Open an issue on [GitHub Issues](https://github.com/evhu-dev/agent-fault-check/issues) or email [support@evhu.net](mailto:support@evhu.net).
