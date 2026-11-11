# Connector domain

`pkg/connector` owns persisted `connectors`, connector management, neutral
generation leases and MCP discovery/session adapters. `runtime/` imports neither
Agents nor Admin/persistence. `einotool/` is the builtin adapter and emits no usage.

Connector `id` is the required, case-sensitive, immutable reference identity;
`name` is mutable, non-unique display text. Do not introduce a `slug` alias or
resolve objects by display name. Update bodies may omit ID or repeat the target
ID; reject a different ID before guards, persistence or publication. Display
renames preserve all references, credential scopes and approval-entry strings.
Tool names have the protocol's separate callable-identity semantics.

Root `type` selects `mcp` with a nested `mcp` connection. Removed flat connection
fields and management names are errors, including null aliases. The offline
conversion path is `scripts/repair-architecture-config --convert-connectors`;
startup never migrates the old physical table. Preserve MCP transports, inline
auth/env and inert auto_auth. HTTP API execution remains Proposed in the design.

Snapshots/catalogs are defensive, GC-managed memory; request paths never read
ConfigStore. Do not add snapshot reference counting or multi-generation session
drain merely to permit edits.

The simplified design requires display-only name/description edits to preserve
MCP sessions and execution/catalog generations. Reject every mcp/audit
edit and deletion while referenced by an Agent/MCPRoute or busy with admitted/
queued work, remote discovery or initialization, including direct Admin calls.
Idle cached sessions and uninvoked handles do not themselves count as busy.
Check references/busy and invocation admission atomically without holding locks
across network I/O. Stale execution-generation handles fail closed after an
allowed edit. Explicit disablement/credential revocation remain admission
controls. CloseContext stops admission, cancels work, closes owned sessions/processes and
waits within the host budget; retain close/deadline errors and fence late publication.

Managers own their network/process resources. Cache/checkpoint owners remove
unused graphs and permissions; Go GC reclaims unreachable configuration objects.

MCP upstream sessions remain connector-scoped, without caller isolation. Preserve
Agent MCP-route uniqueness and historical event ServiceID/SQLite service_id.
Logical observation belongs to builtin/MCP ingress. Do not change Prometheus labels or protected public event DTOs implicitly.

The complete target is docs/design/connectors.md. Objects update independently;
Bundle apply is sequential with partial success. Do not add cross-resource
transactions, composite publication, group endpoints or group recovery. Release
references before deletion; retain per-resource pending-write protection.
