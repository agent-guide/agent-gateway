# Evaluated capabilities

This matrix describes the Community v0.7.2 distribution. “Implemented” means
the capability is exercised by checked-in tests or release gates. “External”
means the gateway integration exists but evaluation needs separately supplied
software, credentials, or infrastructure. “Roadmap” is not implemented.

| Area | Status | v0.7.2 boundary |
| --- | --- | --- |
| OpenAI-compatible ingress | Implemented | Non-streaming and SSE requests through governed LLM routes. |
| Anthropic Messages and Claude Code profile | Implemented | Anthropic-compatible ingress plus the separate `cc` profile; Claude Code is a client profile, not an ACP backend. |
| Logical-model and direct-provider routing | Implemented | Candidate filtering, managed model resolution, credentials, and bounded fallback. |
| MCP gateway | Implemented | Streamable HTTP and stdio services, discovery, execution, routes, policies, and runtime inspection. |
| Builtin Agent runtime | Implemented | In-process eino ADK host with `single`, `sequential`, `parallel`, `loop`, `supervisor`, `planexecute`, `deep`, and linked `custom` topologies. |
| ACP Agent runtime | External | Gateway pooling, turns, sessions, transcripts, cancellation, and permission decisions are implemented; evaluation requires an external Codex or OpenCode ACP adapter, workspace, and upstream authentication. |
| HTTP/A2A Agent runtime | Implemented | Common-turn translation and governed native A2A Protocol 1.0 JSON-RPC ingress; the release fixture proves both without provider credentials. |
| VirtualKeys and admission limits | Implemented | Per-route authorization and LLM/MCP/Agent dimensions; the Community limiter is process-local. |
| OAuth credential refresh | External | Request-time refresh is implemented; `agw-auth` or a compatible external command and its account authentication are not bundled. |
| Configuration and persistence | Implemented | SQLite ConfigStore, Admin CRUD, declarative Bundle validate/apply/export, and idempotent apply. |
| Metrics and traces | Implemented | Persisted usage events, Prometheus counters, Admin summaries, and optional OTLP export; the first-run stack provisions Prometheus, Tempo, and Grafana. |
| Distributed limiter or HA coordination | Roadmap | No production distributed limiter backend or multi-node coordination ships in Community. |
| Memory | Roadmap | `/admin/memory/...` is reserved and returns `501 Not Implemented`. |
| Durable workflows, schedules, and human tasks | Roadmap | Owned by an upper-layer workbench and external workflow engine; the gateway exposes request-bound Agent/LLM/MCP APIs only. |
| Enterprise IAM, SSO, organizations, and roles | Roadmap | Admin protection is supplied at the deployment boundary; these identity models do not ship in Community. |
| Durable compliance audit and cost budgets | Roadmap | Current events and public sinks are bounded operational telemetry, not a durable audit ledger or budget enforcement system. |

The release proves packaging and protocol paths, not the security or reliability
of an operator's upstream providers, adapters, reverse proxies, or telemetry
deployment. See the [release notes](../releases/README.md) for version-specific
limitations.
