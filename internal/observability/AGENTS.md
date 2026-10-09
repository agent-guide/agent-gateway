# internal/observability — AGENTS.md

Scope: usage events, the event pipeline, OTLP export, and eino callback
integration. The root `AGENTS.md` rules apply. Keep API inventories and
operational descriptions in the architecture documentation rather than here.

## Event invariants

- Usage is stored in typed SQLite event tables (`llm_usage_events`,
  `mcp_usage_events`, `acp_usage_events`, `builtin_usage_events`,
  `a2a_usage_events`), not generic config stores or internal rollups. Preserve
  nullable `agent_id`, `run_id`, and `runtime_type` correlation fields for
  direct non-Agent traffic.
- Unified `kind=agent` ingress selects the ACP/builtin typed event family from
  bounded `runtime_type` while preserving `route_kind=agent`.
  `route_protocol=agent` identifies translated ingress and
  `route_protocol=a2a` identifies native A2A ingress. Stamp `agent_id` directly
  from AgentRoute; never use the historical ACP `service_id` column as an
  active Agent identity.
- A failed span without an explicit `error_type` maps 4xx statuses to
  `client_error` and other statuses to `internal_error`.
- When the dispatcher passes an unhandled request to the next handler, call
  `InteractionSpan.Discard`; such requests must not emit usage events.
- Retention cleanup runs at startup and through the SQLite sink janitor.
  High-volume aggregation belongs in external Prometheus/Grafana systems.

## Tracing and callbacks

- OTLP export reconstructs the existing interaction tree from stored W3C
  trace/span/parent ids. Builtin inner LLM/MCP events are internal-kind spans;
  ingress interactions are server-kind spans.
- Exporter setup failure disables export without preventing request serving.
  Component calls without an interaction span are intentionally not exported.
- Register `einotap` once across both bootstrap paths. It only folds synchronous
  chat-model completion detail into the current interaction span; it must not
  emit separate usage events or consume/copy streams.
- The process-global component exporter may be replaced on reload, but callback
  handlers must not be registered repeatedly.
- Prometheus usage counters label only bounded `route_kind` and `runtime_type`;
  never add Agent, route, endpoint, run, session, request, or permission ids.

## Integration points

- Pipeline sinks are wired in `caddy/gateway/app.go` and
  `standalone/server/server.go`.
- `metrics.max_agent_depth` rejects ingress when `X-Agent-Depth` reaches the
  configured limit; zero disables the gate.

## Public event boundary

The existing pipeline is the single authority for public DTO publishing and
usage accounting. Public sinks receive only `pkg/event` envelopes, never `any`
usage rows. Each sink has one bounded worker; production writes never allocate
one goroutine per callback. Slow/erroring sinks are isolated from each other and
usage accounting. Counts and health remain observable in metrics summaries.
Write/Flush/Close honor contexts; in-flight flush inherits the shutdown deadline.
Close stops publication, drains and closes once; concurrent shorter waiters do not
change the first caller's common budget. Contract probes reject stalled sinks.
Runtime dimension updates carry bounded attribution without replacing trusted
subject/tenant and allowlisted attributes. Keep arbitrary client identity headers
out of trusted context and all secret/config/content fields out of public DTOs.
