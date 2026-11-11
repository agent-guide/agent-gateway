# Connector runtime

`pkg/connector` owns the `connectors` configuration collection and SQLite table.
A connector has `type: mcp` with a nested `mcp` connection. Admin management is
`/admin/connectors`,
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
outcomes. MCP calls through a retained lease use its captured connection
definition.
Admission compares its execution token and object incarnation with the current
snapshot. Replaced or deleted/recreated objects reject stale handles; display
edits preserve the token. Disablement and pending-write checks also apply.
Builtin materialization includes the connector generation
vector in its cache identity; approval resume rejects changed generations.

MCP inline auth/env and inert `auto_auth` retain their previous semantics under
the nested connection object. MCP logical tool usage retains its existing event
schema and historical `service_id`.

Configuration writes compile before persistence and read authoritative records
back. Unconfirmed connector outcomes gate new execution until mutation recovery
verifies stored facts without replaying writes. Agent and MCP route references block execution-affecting edits and deletion
under application coordination, including disabled objects and retained pending
Agent references. Agent writes require existing connectors. MCP tool selections
resolve against the remote catalog during builtin materialization. Connector
Refresh uses the same reference and active-work guards;
a rejected candidate preserves the last valid snapshot.

Resources update independently. CLI apply uses normal per-object CRUD in
dependency order and reports partial success; earlier writes remain if a later
step fails. There are no cross-resource publication groups or group recovery.
Remove Agent/MCPRoute references before deleting a connector. A staged
replacement can use a separate Connector ID, moving references individually
before removing the old object. Single-resource mutation inspection/recovery remains available.

## Mutation admission and lifecycle

Name/description and semantically unchanged updates retain MCP sessions and
execution tokens. Every mcp/audit change is an execution
edit; id/type are immutable. Execution edits and deletion return conflicts while
referenced or busy. Disablement remains available and blocks subsequent calls;
MCP authentication remains owned by the configured connection.

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
