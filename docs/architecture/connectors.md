# Connector runtime

`pkg/connector` owns the `connectors` configuration collection and SQLite table.
A connector has `type: mcp` with a nested `mcp` connection, or `type: http_api`
with a nested `http_api` configuration. Admin management is `/admin/connectors`,
Bundle definitions use `connectors`, and the root CLI command is `connector`.
MCP publishing routes retain their protocol identity and target `connector_id`.
Agent declarations use `resources.connector_ids`; builtin selections and
permission DTOs use `connector_id`.

Connector `id` is the immutable, case-sensitive identifier used in all
references; `name` is an editable, non-unique display name. There is no
independent `slug`. A Connector PUT may omit its body ID or repeat the target
ID; a different ID is rejected before mutation. Renaming the display name
preserves Agent/route references, credential scopes and approval-entry strings.
Tool `name` remains the protocol's callable identifier.

`pkg/connector/runtime` defines generation leases, catalogs, tool calls and typed
outcomes. HTTP calls through a retained lease use its compiled Schema and mapping.
Admission compares its execution token and object incarnation with the current
snapshot. Replaced or deleted/recreated objects reject stale handles; display
edits preserve the token. Disablement, pending-write, credential and egress
checks also apply. Builtin materialization includes the connector generation
vector in its cache identity; approval resume rejects changed generations.

`pkg/connector/httpapi` validates the restricted JSON Schema subset and compiles
GET/POST path, query and JSON-body mappings. Numbers stay lossless through
argument parsing, validation, mapping and JSON responses. The executor does not
retry HTTP requests. Known execution failures become safe tool-error results;
a POST that may have been sent carries an unconfirmed-write explanation.

`pkg/connector/mcp` owns MCP configuration, typed discovery/execution methods,
initialization, progress decoding, cached connector-scoped sessions and session
inspection. Manager admits calls and delegates to its MCP engine using an
immutable Target; display edits and unchanged Refresh retain that Target and its
connection semaphore. Reconnect and idle expiry request exclusive retirement:
Manager checks competing work and detaches the session under its admission lock,
then the engine closes the resource outside the lock. The engine fences late
session publication on shutdown and retains close errors; Manager waits for both
engine resource closure and its admitted work within the shutdown budget.

Wire transports live in `pkg/mcp/transport`. The separate `pkg/mcp/tracking`
package records ingress requests, cancellation, progress and bounded in-memory
history; it owns no upstream execution or sessions. Admin inspection keeps
`/admin/mcp/runtime` URLs. MCP configuration and session inspection Go types now
belong to `pkg/connector/mcp`; stored Connector JSON and Admin payloads retain
their existing shape.

Both hosts accept the local `http_api_policy_file` setting (standalone flag
`--http-api-policy-file`). The default permits public HTTPS on port 443.
Explicit policies can grant internal endpoints. Each invocation resolves and
checks every DNS address, dials an admitted address and verifies the peer.
Per-call transports disable connection reuse, proxies, redirects, compression
and cookies. Coordinated global/connector admission counts waiting time against
the call deadline without reserving active slots for queued work.

HTTP credentials reference one explicit `api_key` credential with exact scope
`connector:<id>` and empty Provider fields. Missing or revoked credentials do
not invalidate stored definitions. State inspection computes local readiness;
every execution rechecks the live credential. It does not use Provider
selection, failover, OAuth refresh or scheduling. MCP inline auth/env and inert
`auto_auth` retain their previous semantics under the nested connection object.

Builtin and external MCP ingress remain the owners of logical tool usage.
`MCPUsageEvent` retains its historical ServiceID/SQLite `service_id` mapping and
adds nullable `connector_type` and typed `http_execution` JSON. HTTP summaries
contain templates and byte/status/timing measures, without arguments, secrets,
queries or response bodies. Optional OTLP `http_client_spans` exports a CLIENT
child through the lifecycle exporter. It creates no usage row, public event or
Prometheus metric family.

Configuration writes compile before persistence and read authoritative records
back. Unconfirmed connector outcomes gate new execution until mutation recovery
verifies stored facts without replaying writes. Agent and MCP route references block execution-affecting edits and deletion
under application coordination, including disabled objects and retained pending
Agent references. Agent writes require existing connectors and configured HTTP
tool selections. Connector Refresh uses the same reference and active-work guards;
a rejected candidate preserves the last valid snapshot.

Resources update independently. CLI apply uses normal per-object CRUD in
dependency order and reports partial success; earlier writes remain if a later
step fails. There are no cross-resource publication groups or group recovery.
Remove Agent/MCPRoute references before deleting a connector. A staged
replacement can use a separate Connector ID, moving references individually
before removing the old object. Single-resource mutation inspection/recovery remains available.

## Mutation admission and lifecycle

Name/description and semantically unchanged updates retain MCP sessions, compiled
catalogs and execution tokens. Every mcp/http_api/audit change is an execution
edit; id/type are immutable. Execution edits and deletion return conflicts while
referenced or busy. Disablement remains available and blocks subsequent calls;
credential revocation is checked from current credential memory.

The manager registers active work atomically with token/pending/closed checks,
before queueing or network I/O. Every tool/resource/prompt operation, remote
discovery and initialization uses this admission, including Admin and retained
builtin handles. Completion, failure and cancellation release admission. Mutation
installs its pending gate under the same lock only when conflicting work is absent.
Idle cached sessions and dormant handles do not count as work. Initialization is
serialized per connector; metadata edits do not reconnect an initialized session.
A failed shared MCP session is not closed for a reconnect while other calls use it.

Immutable leases/catalogs use ordinary Go GC and own no per-handle sockets or
subprocesses. Agent definition commits remove obsolete builtin graphs and claim
old permission continuations; cleanup removes their checkpoints outside the
publication lock. New-definition permissions survive delayed cleanup. Late graph
builds and permission registration reject retired definitions.

Shutdown stops admission, cancels active/queued work and session initialization,
closes owned MCP sessions/processes, and waits for calls and builtin turns within
the host budget. Connector/host CloseContext returns close errors and deadline
failures; late initialization cannot publish into a closed manager. Stdio closure
closes stdin, waits briefly, then kills and reaps a non-exiting child. Cached session
inspection reports closure. Configuration memory needs no resource reference count.

Builtin and custom-factory tool dependencies both use `runtime.Resolver` and
retained `Lease` execution. Factory observation decorates the Lease, preserving
execution identity and complete outcomes, including HTTP summaries and uncertain
writes. Tool bridge construction closes its lease if catalog/schema selection fails.

Internal MCP tool catalogs and unpaged Admin discovery collect all pages, bounded
to 100 pages and 10,000 tools. Repeated cursors, cancellation, page failures or
limit exhaustion reject the whole catalog; never cache a partial catalog. External
MCP ListToolsPage preserves one-page cursor/nextCursor semantics.

MCP transport selection only constructs a client. One shared startup path connects,
negotiates initialization, checks cancellation and publishes the session. Startup
owns cleanup until cache handoff; the cache owns both accepted and rejected
publication cleanup. Engine shutdown cancels initialization, and failed startup
retains close errors without closing the same candidate twice.

`httpapi.Engine.CallTool` is the HTTP tool execution entry point and returns
`runtime.CallOutcome`. It owns summary capture, safe error classification, panic
containment and uncertain-write results. Root leases retain admission and catalog
validation, then delegate; raw HTTP execution and capture are package-private.

Manager names its composed engines `httpEngine` and `mcpEngine`, and each MCP
entry retains `mcpTarget`. Within the MCP engine, `upstreamSessions` holds owned
resources while `sessionViews` holds inspection records. Domain helpers and
errors use Connector terminology; historical usage `ServiceID`/`service_id`
remain the persisted event contract.
