# Observability Design

## 1. Scope

This document describes the design for unified audit logging and usage metrics for `agent-gateway`.

It covers reliable capture, persistence, and query of request-level events and aggregated usage statistics for all gateway traffic — LLM, MCP, and future protocols such as ACP and A2A.

This allows operators and agent builders to:

- audit what LLM tools agents called and which MCP tools were invoked
- measure token consumption, request volume, latency, and error rates
- reconstruct multi-agent call chains through trace and span correlation
- govern agent behavior by inspecting depth, frequency, and error patterns across agent chains

This document extends the MCP gateway architecture described in `mcp-gateway-architecture.md` and fills in the currently unimplemented metrics area referenced by `docs/DESIGN.md`. MCP tool policy is a separate concern described in `mcp-tool-policy-design.md`.

The current implementation baseline is:

- LLM routing and dispatch through `pkg/dispatcher/handler.go` and protocol adapters
- MCP routing and dispatch through `pkg/dispatcher/mcp_handler.go` and `pkg/mcp/service`
- In-memory MCP request history in `pkg/mcp/runtime/registry.go`
- SQLite config store in `pkg/configstore/sqlite`
- Admin API in `pkg/admin`

## 2. Goals

- Capture one structured event per completed LLM request, including request-side tool metadata and response-side tool call summary
- Capture one structured event per completed MCP tool call or resource access
- Carry agent chain identity (`trace_id`, `span_id`, `agent_depth`) on every event from phase 1
- Persist events durably to SQLite so history survives restarts
- Expose useful summaries and recent-event inspection through the Admin API, including a unified cross-protocol view
- Support aggregated rollups for token and request volume trends
- Keep the request critical path impact minimal

## 3. Non-Goals

This design does not attempt to:

- store raw prompt text or response text
- store raw MCP tool arguments by default
- replace provider-native billing systems
- introduce an external observability stack dependency
- guarantee accurate streaming token counts in phase 1
- implement real-time push or webhook delivery of events

## 4. Capability Overview

### 4.1 LLM Observability

Every completed LLM request produces one persisted usage event containing:

- agent chain dimensions: trace ID, span ID, parent span ID, agent depth
- routing dimensions: route, provider, virtual key, logical model, upstream model, credential
- request shape: API family, streaming flag, request-side declared tools summary
- execution outcome: success, error type, status code, latency
- token usage: input tokens, output tokens, total tokens, finalization flag
- tool call summary: count of tool calls and list of tool names invoked in the response

LLM observability must cover both currently supported LLM ingress shapes:

- OpenAI-compatible chat completions and Anthropic messages APIs
- OpenAI-compatible Responses API

LLM request-side tools refer to tool definitions declared by the client in the incoming request payload. LLM response-side tool calls refer to function calls embedded in model responses (`tool_calls` in OpenAI chat format, output tool call items in Responses API, and `tool_use` blocks in Anthropic wire format when available). They are distinct from MCP tool invocations.

### 4.2 MCP Observability

Every completed MCP operation produces one persisted usage event containing:

- agent chain dimensions: trace ID, span ID, parent span ID, agent depth
- routing dimensions: route, service, virtual key
- operation: method, tool name (for `tools/call`), resource URI (for `resources/read`)
- execution outcome: result status, error type, cancelled flag, latency
- policy attribution: presented tool name, executed tool name, execution mode (see `mcp-tool-policy-design.md`)
- argument metadata: argument count only; full argument capture is opt-in per service

The existing in-memory ring buffer in `pkg/mcp/runtime/registry.go` is retained for fast real-time inspection. SQLite persistence runs alongside it for durability.

## 5. Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                        Inbound HTTP                              │
│               http.handlers.agent_route_dispatcher               │
│          (extracts X-Trace-ID / X-Span-ID / X-Agent-Depth)      │
└────────────────────────┬─────────────────────────────────────────┘
                         │
         ┌───────────────┴────────────────┐
         │ LLM dispatch                   │ MCP dispatch
         ▼                                ▼
┌─────────────────┐              ┌─────────────────────┐
│ pkg/dispatcher  │              │ pkg/dispatcher      │
│ llmapi/...      │              │ mcp_handler.go      │
└────────┬────────┘              └────────┬────────────┘
         │                                │
         │ InteractionObserver.Begin()    │ InteractionObserver.Begin()
         ▼                                ▼
┌─────────────────┐              ┌─────────────────────┐
│ pkg/gateway     │              │ pkg/mcp/service      │
│ RoutedProvider  │              │ Manager              │
│ Chat/Stream     │              │ tool_policy filter   │
└────────┬────────┘              └────────┬────────────┘
         │                                │
         │ InteractionSpan.Finish()       │ InteractionSpan.Finish()
         ▼                                ▼
┌──────────────────────────────────────────────────────┐
│                   pkg/metrics/usage                   │
│   InteractionObserver / InteractionSpan interfaces    │
│   InteractionEvent base + LLMExtension / MCPExtension │
│   no-op and pipeline-backed implementations           │
└──────────────────────────────────────────────────────┘
         │
         ▼
┌──────────────────────────────────────────────────────┐
│                 pkg/metrics/pipeline                  │
│   EventPipeline: buffered channel + fan-out           │
│   SQLiteSink / PrometheusSink / [future sinks]        │
└──────────────────────────────────────────────────────┘
         │
         ▼
┌──────────────────────────────────────────────────────┐
│               pkg/configstore/sqlite                  │
│   llm_usage_events table                             │
│   mcp_usage_events table                             │
│   llm_usage_rollups table                            │
└──────────────────────────────────────────────────────┘
         │
         ▼
┌──────────────────────────────────────────────────────┐
│                    pkg/admin                          │
│   GET /admin/metrics                                 │
│   GET /admin/metrics/interactions (unified)          │
│   GET /admin/metrics/llm/...                         │
│   GET /admin/metrics/mcp/...                         │
└──────────────────────────────────────────────────────┘
```

### 5.1 Unified Event Model

All protocol-specific events extend a shared `InteractionEvent` base type. This base captures dimensions common to every gateway interaction regardless of protocol, enabling cross-protocol analytics and consistent governance queries.

```go
// InteractionEvent is the common base for all gateway interaction records.
type InteractionEvent struct {
    EventID      string    // globally unique event identifier
    TraceID      string    // caller-supplied or gateway-generated trace correlation ID
    SpanID       string    // unique span identifier for this request
    ParentSpanID string    // caller's span ID from X-Span-ID header; empty for direct callers
    AgentDepth   int       // agent call chain depth; 0 for direct callers, incremented per hop
    StartedAt    time.Time
    FinishedAt   time.Time
    RouteID      string
    RouteKind    string    // llm | mcp | a2a | acp
    VirtualKeyID string
    Success      bool
    StatusCode   int
    ErrorType    string
    LatencyMS    int64
}
```

`LLMUsageEvent` and `MCPUsageEvent` embed `InteractionEvent` and add protocol-specific fields. When additional protocols such as ACP or A2A are introduced, their event types follow the same embedding pattern without modifying the shared base.

The `InteractionObserver` interface is the single call-site interface used by all dispatchers:

```go
type InteractionSpan interface {
    SetExtension(v any)                  // protocol-specific metadata
    AddAnnotation(key, value string)
    Finish(outcome InteractionOutcome)
}

type InteractionObserver interface {
    Begin(ctx context.Context, dims InteractionDimensions) (InteractionSpan, context.Context)
}
```

The no-op implementation satisfies this interface for testing and unconfigured deployments. The pipeline-backed implementation enqueues events asynchronously (see §5.3).

### 5.2 Agent Identity And Call Chain Tracing

A gateway serving multi-agent workloads must answer questions that span multiple requests:

- which agent chain produced this sequence of tool calls?
- how deep is the recursion, and at what depth did errors appear?
- did a multi-agent pipeline exceed its allowed call depth or volume?

The gateway extracts agent chain context from inbound HTTP headers on every request:

| Header | Stored field | Purpose |
|--------|-------------|---------|
| `X-Trace-ID` | `trace_id` | Caller-supplied correlation ID; gateway generates a UUID if absent |
| `X-Span-ID` | `parent_span_id` | The caller's span ID; recorded as this event's `parent_span_id` |
| `X-Agent-Depth` | `agent_depth` | Hop count from the originating caller; stored as-is and returned as `agent_depth + 1` in the response header |

The gateway always generates a new `span_id` (UUID) per request and emits `X-Trace-ID`, `X-Span-ID`, and `X-Agent-Depth` response headers so downstream agents can propagate them.

These four fields (`trace_id`, `span_id`, `parent_span_id`, `agent_depth`) are stored on every event from phase 1. Agent depth enforcement — rejecting requests exceeding a configured maximum depth — is a later-phase policy gate, but the data must be present from phase 1 to make that gate possible without schema migration.

### 5.3 Event Pipeline

The `InteractionObserver` interface used at call sites does not write to storage directly. It enqueues a completed event into a buffered in-process channel. A background `EventPipeline` goroutine reads from this channel and fans out to registered sinks.

```
InteractionObserver  →  buffered channel  →  EventPipeline
                                                  ├── SQLiteSink     (durable storage, async batch write)
                                                  ├── PrometheusSink (counter and histogram updates, Phase 4)
                                                  └── [future: WebhookSink, OpenTelemetrySink]
```

Properties of this design:

- **Non-blocking call sites**: request-handling goroutines enqueue and return immediately; SQLite I/O is never on the request critical path.
- **Back-pressure handling**: when the channel is full the enqueue call drops the event and increments a `dropped_events` counter rather than blocking the caller.
- **Multiple sinks without interface churn**: adding Prometheus support or webhook delivery requires registering a new sink, not changing the observer interface or call sites.
- **Graceful shutdown**: the pipeline drains its queue on context cancellation before stopping sinks, ensuring in-flight events are not lost on clean shutdown.

The `EventPipeline` is provisioned once by `caddy/gateway/app.go` at startup. The `InteractionObserver` returned to dispatchers is a thin enqueue wrapper over the pipeline's input channel.

## 6. LLM Observability

### 6.1 Event Model

Each completed LLM request produces one `LLMUsageEvent`. It embeds `InteractionEvent` (§5.1) for the shared base fields and adds LLM-specific fields:

```
-- InteractionEvent base (shared across all protocols) --
event_id            string     globally unique event identifier
trace_id            string     caller-supplied or gateway-generated trace correlation ID
span_id             string     unique span identifier for this request
parent_span_id      string     caller's span from X-Span-ID header; empty for direct callers
agent_depth         int        agent call chain depth; 0 for direct callers
started_at          time.Time
finished_at         time.Time
route_id            string
route_kind          string     llm
virtual_key_id      string
success             bool
status_code         int
error_type          string     normalized error category (see §6.3)
latency_ms          int64

-- LLM-specific extension --
llm_api             string     openai | anthropic
api_operation       string     chat_completions | messages | responses
provider_id         string
provider_type       string
logical_model       string     client-facing model name in logical-model routes
upstream_model      string     concrete provider-facing model actually called
credential_source   string     static | cliauth | managed
credential_id       string
stream              bool
input_tokens        int
output_tokens       int
total_tokens        int
usage_finalized     bool       false if token counts are incomplete (streaming phase 1)
request_tool_count  int        number of tools declared in the request
request_tool_names  []string   declared tool names; may be truncated in later phases
tool_call_count     int        number of outbound tool calls in the LLM response
tool_names          []string   names of tools called by the model; empty if none
```

`request_tool_count` and `request_tool_names` capture the tool definitions the client exposed to the model for that request. `tool_call_count` and `tool_names` capture the model's outbound tool invocations embedded in the response. These are not MCP calls; they are the LLM API-level function calling slots.

### 6.2 Capture Points

**Request context initialization** happens in `pkg/dispatcher/handler.go` after route resolution and virtual key validation. At this point the stable dimensions are known: route, virtual key, protocol, and the selected API handler. An `InteractionSpan` is created via `InteractionObserver.Begin` and stored in the request context.

**Request-side tool extraction** happens in the protocol handler before provider execution. This is where the incoming wire payload is still available in protocol-native form:

- OpenAI chat completions: inspect request `tools`
- OpenAI Responses API: inspect request `tools`
- Anthropic messages: inspect request `tools` once the wire model supports it in the gateway

The protocol handler passes the declared tool names and count into the span via `SetExtension` before dispatching upstream.

**Execution outcome recording** happens at the routed provider boundary after the provider call completes. This layer owns credential selection, upstream model resolution, request-model rewrite, and the final provider call boundary. The span records provider dimensions, latency, token counts, and success or failure here.

**Response-side tool call extraction** happens at the protocol handler boundary:

- OpenAI chat completions: extract `ToolCalls` from the provider message before rendering the HTTP response
- OpenAI Responses API: extract tool call outputs from the provider-level responses payload or responses stream events
- Anthropic messages: extract tool call names only if the gateway preserves tool identity through conversion; until then the event may record `tool_call_count` without `tool_names`

The span therefore supports partial finalization: tool counts may be available before tool names are available.

### 6.3 Error Categories

Normalized error types stored in `error_type`:

- `route_not_found`
- `virtual_key_rejected`
- `provider_not_configured`
- `provider_disabled`
- `credential_unavailable`
- `provider_request_failed`
- `provider_stream_failed`
- `protocol_validation_failed`
- `internal_error`

`status_code` is the downstream HTTP status returned by the gateway, not a raw upstream provider status code. This keeps the event model stable across provider implementations.

### 6.4 Streaming Behavior

Phase 1 behavior for streaming requests:

- record the event when the stream completes or errors
- use available final usage metadata if the provider exposes it in the stream
- set `usage_finalized=false` if final usage is not available
- do not invent token counts

This applies separately to:

- chat/messages streaming
- Responses API event streaming

Accurate streaming token counts and richer per-event tool extraction are later-phase improvements.

## 7. MCP Observability

### 7.1 Event Model

Each completed MCP operation produces one `MCPUsageEvent`. It embeds `InteractionEvent` (§5.1) for the shared base fields and adds MCP-specific fields:

```
-- InteractionEvent base (shared across all protocols) --
event_id            string     globally unique event identifier
trace_id            string     caller-supplied or gateway-generated trace correlation ID
span_id             string     unique span identifier for this request
parent_span_id      string     caller's span from X-Span-ID header; empty for direct callers
agent_depth         int        agent call chain depth; 0 for direct callers
started_at          time.Time
finished_at         time.Time
route_id            string
route_kind          string     mcp
virtual_key_id      string
success             bool
status_code         int
error_type          string     normalized error category (see §7.2)
latency_ms          int64

-- MCP-specific extension --
request_id          string     JSON-RPC request id, not globally unique
service_id          string
method              string     tools/call | resources/read | prompts/get | tools/list | ...
tool_name           string     upstream tool name; populated for tools/call
presented_tool_name string     client-visible tool name after alias/policy application
                               (see mcp-tool-policy-design.md for how this is set)
executed_tool_name  string     actual execution target; differs from tool_name when synthetic
execution_mode      string     forwarded_tool | wrapped_tool | synthetic_tool
resource_uri        string     populated for resources/read
prompt_name         string     populated for prompts/get
arg_count           int        number of arguments passed; not the argument values
result_status       string     success | error | cancelled
tool_args_json      string     full JSON of arguments; null unless verbose_audit enabled (see §7.3)
```

`tool_name` and `arg_count` are always captured when the method shape includes them. `tool_args_json` is null by default.

The `presented_tool_name`, `executed_tool_name`, and `execution_mode` fields are populated by the tool policy layer in `pkg/mcp/service/`. See `mcp-tool-policy-design.md` §6.7.7 for audit attribution details.

The existing `CompletedRequest` struct in `pkg/mcp/runtime/registry.go` is extended with the MCP-specific fields above plus the `InteractionEvent` base fields. The in-memory ring buffer continues to serve real-time inspection. The SQLite writer consumes the same event to provide durable history.

### 7.2 MCP Error Categories

- `route_not_found`
- `virtual_key_rejected`
- `service_not_found`
- `service_unavailable`
- `tool_not_found`
- `tool_denied`
- `upstream_error`
- `upstream_timeout`
- `cancelled`
- `internal_error`

### 7.3 Verbose Audit Mode

Full argument capture for `tools/call` is off by default. It can be enabled per MCP service:

```json
{
  "id": "fs-service",
  "name": "Filesystem",
  "transport": "stdio",
  "audit": {
    "capture_tool_args": true
  }
}
```

When enabled, `tool_args_json` is populated with the serialized argument map. Operators are responsible for ensuring this complies with their data handling policies, since arguments may contain file paths, content, or other sensitive data.

Result content is never stored regardless of this setting.

## 8. Admin API

### 8.1 Existing Endpoints Updated

`GET /admin/metrics` — currently returns `501`. Replaced with a real summary response:

```json
{
  "llm": {
    "request_count": 1240,
    "success_count": 1198,
    "failure_count": 42,
    "input_tokens": 3820000,
    "output_tokens": 940000,
    "total_tokens": 4760000,
    "avg_latency_ms": 1842
  },
  "mcp": {
    "request_count": 4870,
    "success_count": 4801,
    "failure_count": 69,
    "tools_call_count": 3210,
    "avg_latency_ms": 320
  }
}
```

### 8.2 New LLM Metrics Endpoints

`GET /admin/metrics/llm/events`

Returns recent LLM usage events. Supports query parameters:

- `route_id`, `provider_id`, `virtual_key_id`, `logical_model`, `upstream_model`
- `api_operation`
- `from`, `to` (RFC3339)
- `success` (bool)
- `request_tool_name`
- `has_tool_use` (bool) — filter to requests where tool calls were made
- `limit` (default 100, max 1000)

`GET /admin/metrics/llm/timeseries`

Returns bucketed request and token counts. Parameters: `from`, `to`, `bucket` (hour|day), `group_by` (route_id|provider_id|virtual_key_id|upstream_model).

`GET /admin/metrics/llm/breakdown`

Returns grouped totals ranked by request count, token count, or failure count. Parameters: `group_by`, `from`, `to`.

### 8.3 New MCP Metrics Endpoints

`GET /admin/metrics/mcp/events`

Returns recent MCP usage events. Supports query parameters:

- `route_id`, `service_id`, `virtual_key_id`
- `method` (tools/call | resources/read | ...)
- `tool_name`
- `from`, `to`
- `result_status`
- `limit` (default 100, max 1000)

`GET /admin/metrics/mcp/tools/summary`

Returns per-tool aggregated statistics:

```json
[
  {
    "tool_name": "read_file",
    "service_id": "fs-service",
    "call_count": 2341,
    "success_count": 2298,
    "failure_count": 43,
    "avg_latency_ms": 280
  }
]
```

Supports `from`, `to`, `service_id`, `route_id` filters.

### 8.4 Unified Cross-Protocol Interactions Endpoint

Protocol-specific endpoints are the right tool for protocol-specific drill-downs. Cross-protocol governance queries — "how many interactions did virtual key X have across all protocols?", "which agent chains had the highest error rates?" — require a unified view.

`GET /admin/metrics/interactions`

Returns recent interaction events across all protocols, backed by the shared `InteractionEvent` base fields. Supports query parameters:

- `route_kind` (llm | mcp | a2a) — filter to a single protocol family
- `route_id`, `virtual_key_id`
- `trace_id` — retrieve all events belonging to a single agent trace
- `parent_span_id` — retrieve direct children of a span
- `agent_depth` — filter by call chain depth (e.g., `agent_depth=0` for direct callers only)
- `from`, `to` (RFC3339)
- `success` (bool)
- `limit` (default 100, max 1000)

Example response:

```json
[
  {
    "event_id": "evt_01jx...",
    "trace_id": "trc_abc123",
    "span_id": "spn_def456",
    "parent_span_id": "",
    "agent_depth": 0,
    "route_id": "llm-main",
    "route_kind": "llm",
    "virtual_key_id": "vk_xyz",
    "success": true,
    "latency_ms": 1240,
    "started_at": "2026-05-21T10:00:00Z",
    "finished_at": "2026-05-21T10:00:01.240Z"
  },
  {
    "event_id": "evt_01jy...",
    "trace_id": "trc_abc123",
    "span_id": "spn_ghi789",
    "parent_span_id": "spn_def456",
    "agent_depth": 1,
    "route_id": "mcp:fs-service:/mcp/fs",
    "route_kind": "mcp",
    "virtual_key_id": "vk_xyz",
    "success": true,
    "latency_ms": 85,
    "started_at": "2026-05-21T10:00:00.500Z",
    "finished_at": "2026-05-21T10:00:00.585Z"
  }
]
```

`GET /admin/metrics/interactions/summary`

Returns aggregated totals grouped by `route_kind`, `route_id`, or `virtual_key_id`. Parameters: `group_by`, `from`, `to`.

## 9. Storage Schema

The usage storage package is introduced as a new concern within the existing SQLite configstore. It does not reuse the generic JSON store used for providers, routes, and services. It uses typed tables suited for time-series and aggregation queries.

### 9.1 LLM Usage Events Table

Table: `llm_usage_events`

```sql
CREATE TABLE llm_usage_events (
    -- InteractionEvent base columns (present on all protocol event tables)
    event_id          TEXT PRIMARY KEY,
    trace_id          TEXT,              -- caller-supplied or gateway-generated
    span_id           TEXT NOT NULL,
    parent_span_id    TEXT,              -- null for direct callers
    agent_depth       INTEGER NOT NULL DEFAULT 0,
    started_at        INTEGER NOT NULL,  -- unix milliseconds
    finished_at       INTEGER NOT NULL,
    route_id          TEXT,
    route_kind        TEXT NOT NULL DEFAULT 'llm',
    virtual_key_id    TEXT,
    success           INTEGER NOT NULL DEFAULT 0,
    status_code       INTEGER,
    error_type        TEXT,
    latency_ms        INTEGER,
    -- LLM-specific columns
    request_id        TEXT UNIQUE,       -- per-request ID used at call sites
    llm_api           TEXT,
    api_operation     TEXT,
    provider_id       TEXT,
    provider_type     TEXT,
    logical_model     TEXT,
    upstream_model    TEXT,
    credential_source TEXT,
    credential_id     TEXT,
    stream            INTEGER NOT NULL DEFAULT 0,
    input_tokens      INTEGER,
    output_tokens     INTEGER,
    total_tokens      INTEGER,
    usage_finalized   INTEGER NOT NULL DEFAULT 1,
    request_tool_count INTEGER NOT NULL DEFAULT 0,
    request_tool_names TEXT,            -- JSON array, nullable
    tool_call_count   INTEGER NOT NULL DEFAULT 0,
    tool_names        TEXT              -- JSON array, nullable
);

CREATE INDEX idx_llm_events_started    ON llm_usage_events (started_at);
CREATE INDEX idx_llm_events_route      ON llm_usage_events (route_id, started_at);
CREATE INDEX idx_llm_events_vkey       ON llm_usage_events (virtual_key_id, started_at);
CREATE INDEX idx_llm_events_trace      ON llm_usage_events (trace_id, started_at)
    WHERE trace_id IS NOT NULL;
CREATE INDEX idx_llm_events_tool_use   ON llm_usage_events (tool_call_count, started_at)
    WHERE tool_call_count > 0;
```

### 9.2 MCP Usage Events Table

Table: `mcp_usage_events`

```sql
CREATE TABLE mcp_usage_events (
    -- InteractionEvent base columns (present on all protocol event tables)
    event_id          TEXT PRIMARY KEY,
    trace_id          TEXT,              -- caller-supplied or gateway-generated
    span_id           TEXT NOT NULL,
    parent_span_id    TEXT,              -- null for direct callers
    agent_depth       INTEGER NOT NULL DEFAULT 0,
    started_at        INTEGER NOT NULL,  -- unix milliseconds
    finished_at       INTEGER NOT NULL,
    route_id          TEXT,
    route_kind        TEXT NOT NULL DEFAULT 'mcp',
    virtual_key_id    TEXT,
    success           INTEGER NOT NULL DEFAULT 0,
    status_code       INTEGER,
    error_type        TEXT,
    latency_ms        INTEGER,
    -- MCP-specific columns
    request_id        TEXT,              -- JSON-RPC request id, not globally unique
    service_id        TEXT,
    method            TEXT,
    tool_name         TEXT,              -- upstream tool name
    presented_tool_name TEXT,
    executed_tool_name  TEXT,
    execution_mode      TEXT,            -- forwarded_tool | wrapped_tool | synthetic_tool
    resource_uri      TEXT,
    prompt_name       TEXT,
    arg_count         INTEGER,
    result_status     TEXT,              -- success | error | cancelled
    tool_args_json    TEXT               -- null unless verbose audit enabled
);

CREATE INDEX idx_mcp_events_started    ON mcp_usage_events (started_at);
CREATE INDEX idx_mcp_events_route      ON mcp_usage_events (route_id, started_at);
CREATE INDEX idx_mcp_events_request    ON mcp_usage_events (route_id, request_id, started_at);
CREATE INDEX idx_mcp_events_trace      ON mcp_usage_events (trace_id, started_at)
    WHERE trace_id IS NOT NULL;
CREATE INDEX idx_mcp_events_tool       ON mcp_usage_events (tool_name, started_at)
    WHERE tool_name IS NOT NULL;
```

### 9.3 LLM Rollups Table

Table: `llm_usage_rollups`

```sql
CREATE TABLE llm_usage_rollups (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    bucket_start      INTEGER NOT NULL,
    bucket_granularity TEXT NOT NULL,  -- hour | day
    route_id          TEXT,
    provider_id       TEXT,
    virtual_key_id    TEXT,
    upstream_model    TEXT,
    stream            INTEGER,
    request_count     INTEGER NOT NULL DEFAULT 0,
    success_count     INTEGER NOT NULL DEFAULT 0,
    failure_count     INTEGER NOT NULL DEFAULT 0,
    input_tokens      INTEGER NOT NULL DEFAULT 0,
    output_tokens     INTEGER NOT NULL DEFAULT 0,
    total_tokens      INTEGER NOT NULL DEFAULT 0,
    latency_ms_sum    INTEGER NOT NULL DEFAULT 0,
    tool_use_count    INTEGER NOT NULL DEFAULT 0,

    UNIQUE (bucket_start, bucket_granularity, route_id, provider_id,
            virtual_key_id, upstream_model, stream)
);

CREATE INDEX idx_llm_rollups_bucket ON llm_usage_rollups (bucket_start, bucket_granularity);
```

### 9.4 Retention

Default retention policy:

- `llm_usage_events`: 30 days
- `mcp_usage_events`: 30 days
- `llm_usage_rollups`: indefinite

A background cleanup job runs periodically to delete expired event rows. The cleanup interval and retention window are configurable in a later phase; phase 1 uses a fixed 30-day window and runs cleanup on gateway startup.

## 10. Package Structure

```
pkg/metrics/usage/
    event.go         InteractionEvent base; LLMUsageEvent, MCPUsageEvent types
    observer.go      InteractionObserver interface and InteractionSpan interface
    noop.go          no-op observer for testing and unconfigured deployments
    service.go       UsageService: shared runtime service wired by caddy/gateway/app.go

pkg/metrics/pipeline/
    pipeline.go      EventPipeline: buffered input channel, fan-out loop, Sink interface
    sqlite_sink.go   SQLite sink (wraps usage_writer logic; async batch write)
    prom_sink.go     Prometheus sink for counter and histogram updates (Phase 4)

pkg/configstore/sqlite/
    usage_writer.go  low-level SQLite INSERT helpers consumed by sqlite_sink
    usage_query.go   query helpers for Admin API handlers
```

The `InteractionObserver` interface is defined in `pkg/metrics/usage/`. Call sites receive a no-op or pipeline-backed implementation. The pipeline-backed implementation enqueues events into `pkg/metrics/pipeline/EventPipeline`, which fans out to registered sinks asynchronously.

The `UsageService` is provisioned once by `caddy/gateway/app.go` alongside other shared runtime services. It owns the `EventPipeline` lifecycle (start, graceful drain on shutdown) and provides the `InteractionObserver` to the dispatcher and admin handler.

## 11. Integration Points

### 11.1 LLM Path

`pkg/dispatcher/handler.go`:
- extract `X-Trace-ID`, `X-Span-ID`, and `X-Agent-Depth` from inbound request headers; generate a `trace_id` UUID if `X-Trace-ID` is absent; generate a new `span_id` UUID for this request
- emit `X-Trace-ID`, `X-Span-ID` (this request's span), and `X-Agent-Depth` (`agent_depth + 1`) response headers so downstream agents can propagate the chain
- after route and virtual key resolution, call `InteractionObserver.Begin(ctx, dims)` with the resolved trace/span/agent fields to attach a span to the request context

`pkg/dispatcher/llmapi/openai/handler.go` and `pkg/dispatcher/llmapi/anthropic/handler.go`:
- set `api_operation`
- extract request-side declared tools from the incoming payload

`pkg/gateway/agentgateway.go` or `RoutedProvider`:
- after provider execution, call `span.Finish(outcome)` with latency, token counts, and error

`pkg/dispatcher/llmapi/openai/handler.go`:
- before writing a chat-completions response, extract `ToolCalls` names and count and add them to the span via `SetExtension`
- before writing a responses response or responses stream completion event, extract output tool call names and count

`pkg/dispatcher/llmapi/anthropic/handler.go` and converter layer:
- extract tool call count
- populate tool names only when the protocol conversion preserves them end-to-end

### 11.2 MCP Path

`pkg/dispatcher/mcp_handler.go`:
- extract `X-Trace-ID`, `X-Span-ID`, and `X-Agent-Depth` from inbound request headers using the same extraction logic as §11.1; emit corresponding response headers
- the existing `beginRequest` / cleanup func pattern already wraps each request; extend the cleanup func to also call `span.Finish(outcome)` via the observer
- pass the resolved route into policy-aware tool listing and tool execution helpers (see `mcp-tool-policy-design.md` §9)

`pkg/mcp/runtime/registry.go`:
- extend `CompletedRequest` with `EventID`, `TraceID`, `SpanID`, `ParentSpanID`, `AgentDepth`, `ServiceID`, `VirtualKeyID`, `ToolName`, `PresentedToolName`, `ExecutedToolName`, `ExecutionMode`, `ResourceURI`, `PromptName`, `ArgCount`, and `ToolArgsJSON`
- populate these fields in `BeginRequest` from the method-specific context

`pkg/mcp/service/types.go`:
- add optional `AuditConfig` to `MCPServiceConfig` so `audit.capture_tool_args` is part of the actual persisted shape

### 11.3 Admin Handler

`pkg/admin/handler.go`:
- accept `UsageService` as a dependency alongside the existing `MCPServiceManager` and `AgentGateway`
- wire new metrics endpoints to query helpers in `pkg/configstore/sqlite/usage_query.go`

## 12. Security And Privacy

The metrics layer must not persist sensitive request content by default.

Fields that are never stored regardless of configuration:

- prompt text
- LLM response text
- MCP tool result content
- bearer key values
- provider API keys

Fields that are stored as stable identifiers only:

- `virtual_key_id` (not the bearer key string)
- `credential_id` (not the credential secret)
- `provider_id`

MCP tool argument storage is off by default and must be explicitly enabled per service via `audit.capture_tool_args`. Because this is persisted configuration, the field must be part of `pkg/mcp/service.MCPServiceConfig` rather than an undocumented sidecar shape. Operators enabling this are responsible for ensuring arguments do not contain secrets or PII that should not be written to the SQLite database.

## 13. Implementation Order

### Phase 1: Observability Foundation (target scope)

Goal: durable event capture for both LLM and MCP traffic, real `/admin/metrics` summary.

1. Define `InteractionEvent` base type in `pkg/metrics/usage/event.go`
2. Define `InteractionObserver` and `InteractionSpan` interfaces; implement no-op
3. Define `LLMUsageEvent` and `MCPUsageEvent` as extensions of `InteractionEvent`
4. Implement `EventPipeline` in `pkg/metrics/pipeline/` with `Sink` interface and `SQLiteSink`
5. Implement SQLite writer helpers: `llm_usage_events` and `mcp_usage_events` tables with all base columns including `trace_id`, `span_id`, `parent_span_id`, `agent_depth`
6. Wire `UsageService` (owns pipeline lifecycle) into `caddy/gateway/app.go`
7. Add trace/span/agent-depth header extraction and response header emission to `pkg/dispatcher/handler.go`
8. Instrument LLM dispatch in `pkg/dispatcher/handler.go`, protocol handlers, and `pkg/gateway`
9. Extend `CompletedRequest` with `InteractionEvent` base fields and MCP-specific audit fields
10. Instrument MCP dispatch in `pkg/dispatcher/mcp_handler.go` (add trace header handling to `beginRequest`)
11. Replace `GET /admin/metrics` `501` with real summary from SQLite
12. Add `GET /admin/metrics/mcp/events`, `GET /admin/metrics/llm/events`, and `GET /admin/metrics/interactions`

### Phase 3: Aggregated Statistics

Goal: rollup tables and time-series query endpoints.

1. Implement `llm_usage_rollups` table and additive update on event write
2. Implement `GET /admin/metrics/llm/timeseries` and `breakdown` endpoints
3. Implement `GET /admin/metrics/mcp/tools/summary`
4. Implement `GET /admin/metrics/interactions/summary` with cross-protocol aggregation
5. Add startup retention cleanup for event tables
6. Add verbose audit mode for MCP tool arguments

### Phase 4: Advanced Observability

Goal: better streaming coverage, Prometheus export, and agent chain analytics.

1. Improve streaming token finalization for providers that expose final usage
2. Add `GET /admin/metrics/llm/breakdown` with full dimension grouping
3. Register `PrometheusSink` in `pkg/metrics/pipeline/` and expose optional Prometheus exporter endpoint
4. Add configurable retention window
5. Add agent depth enforcement policy (configurable max `agent_depth` per route; reject at dispatch)

## 14. Relationship To Existing Documents

`docs/DESIGN.md`:
- this document fills in the currently unimplemented metrics area referenced there
- once implementation lands, `docs/DESIGN.md` should be updated so metrics no longer appear as future work

`mcp-tool-policy-design.md`:
- tool policy populates `presented_tool_name`, `executed_tool_name`, and `execution_mode` on MCP events
- the `InteractionObserver` interface defined here is used by the tool policy dispatch path to record policy-attributed events

`mcp-gateway-architecture.md`:
- §11 Security And Policy and §14 Evolution Path in that document describe audit logging as future work
- those items are now defined concretely in this document
- `mcp-gateway-architecture.md` remains the primary reference for MCP gateway architecture and transport design
