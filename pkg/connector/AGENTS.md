# Connector domain

`pkg/connector` owns persisted `connectors`, immutable HTTP tool catalogs,
connector management and shared admission. `runtime/` defines common generation
leases and tool execution contracts. Parallel `mcp/` and `httpapi/` packages own
upstream execution; `mcp/` also owns MCP configuration, discovery and session
resources. Neither executor nor `runtime/` imports this root package, Agents or
Admin/persistence. HTTP execution must not enter MCP reconnect/retry paths.
MCP exclusive retirement callbacks check competing work and detach sessions
under Manager snapshotMu; close detached resources outside that lock. Display
edits retain the immutable MCP Target and its connection semaphore. `einotool/` is the shared builtin adapter and emits no usage.

Connector `id` is the required, case-sensitive, immutable reference identity;
`name` is mutable, non-unique display text. Do not introduce a `slug` alias or
resolve objects by display name. Update bodies may omit ID or repeat the target
ID; reject a different ID before guards, persistence or publication. Display
renames preserve all references, credential scopes and approval-entry strings.
Tool names have the protocol's separate callable-identity semantics.

Root `type` selects exactly `mcp` plus `mcp`, or `http_api` plus `http_api`.
Removed flat connection fields and management names are errors, including null
aliases. The offline conversion path is `scripts/repair-architecture-config
--convert-connectors`; startup never migrates the old physical table.

Preserve MCP transports, inline auth/env and inert auto_auth. HTTP auth resolves
one explicit credential on every call with exact `connector:<id>` ownership.
Configuration validity does not require credential readiness. Secrets never
enter catalogs, prepared clients, HTTP summaries or model-visible errors.

HTTP tools share argument validation, mapping and results through immutable
handles. Snapshots/catalogs are defensive, GC-managed memory; request paths never
read ConfigStore. Do not add snapshot reference counting or multi-generation
session drain merely to permit edits.

The simplified design requires display-only name/description edits to preserve
MCP sessions and execution/catalog generations. Reject every mcp/http_api/audit
edit and deletion while referenced by an Agent/MCPRoute or busy with admitted/
queued work, remote discovery or initialization, including direct Admin calls.
Idle cached sessions and uninvoked handles do not themselves count as busy.
Check references/busy and invocation admission atomically without holding locks
across network I/O. Stale execution-generation handles fail closed after an
allowed edit. Explicit disablement/credential revocation remain admission
controls. CloseContext stops admission, cancels work, closes owned sessions/processes and
waits within the host budget; retain close/deadline errors and fence late publication.

Readiness queries and execution consult current credential memory; no additional
readiness notification/cache/reverse-index subsystem is required. Executors and
managers own their network/process resources. Cache/checkpoint owners remove
unused graphs and permissions; Go GC reclaims unreachable configuration objects.

MCP upstream sessions remain connector-scoped, without caller isolation. Preserve
Agent MCP-route uniqueness and historical event ServiceID/SQLite service_id.
Logical observation belongs to builtin/MCP ingress; HTTP child spans are tracing
only. Do not change Prometheus labels or protected public event DTOs implicitly.

The complete target is docs/design/connectors.md. Objects update independently;
Bundle apply is sequential with partial success. Do not add cross-resource
transactions, composite publication, group endpoints or group recovery. Release
references before deletion; retain per-resource pending-write protection.

Builtin tool bridges and custom factory dependencies use `connector/runtime.Resolver`
and retained `Lease` exclusively. Observation wraps the Lease and preserves its
descriptor, catalog, generation rejection and full CallOutcome; no ToolCaller fallback.

Internal MCP tool catalogs and unpaged Admin discovery collect all pages, bounded
to 100 pages and 10,000 tools. Repeated cursors, cancellation, page failures or
limit exhaustion reject the whole catalog; never cache a partial catalog. External
MCP ListToolsPage preserves one-page cursor/nextCursor semantics.

MCP transports share one initialization/publication path. Candidate cleanup has
a single owner: startup until cache handoff, then Engine, including rejected
publication. Preserve close errors and detach caller cancellation after success.

HTTP CallTool owns summary capture, safe error mapping and uncertain-write
CallOutcome construction. Root leases only validate identity/catalog, admit and
dispatch; keep raw HTTP execution/capture private to httpapi.

Use Connector terminology for domain helpers, identifiers and errors. Manager
fields distinguish httpEngine/mcpEngine and MCP targets; the MCP engine separates
upstreamSessions resources from sessionViews inspection data. Historical event
ServiceID and SQLite service_id are intentionally retained.
