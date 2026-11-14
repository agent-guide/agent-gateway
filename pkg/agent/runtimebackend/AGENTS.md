# Agent runtime backends

The root and parent Agent rules apply. This package composes Agent contracts
with lower ACP, builtin and A2A runtimes. It must not import `pkg/gateway`; protocol packages must not import this package or Agent definitions.

`Assemble` owns the concrete ACP manager, builtin host, HTTP manager and their
definition listeners in one Set. Gateway owns the injected registries and drains
permission controls before closing that Set. Closed HTTP generations must reject
late prepared commits and stop retry scheduling.

ACP, builtin and HTTP adapters share injected RunRegistry, PermissionBroker and
CheckExecution admission. They do not create private copies of shared controls.
Agent definition listeners prepare off-lock, commit bounded in-memory changes,
and retire external resources in cleanup. Builtin definition commits claim only
old broker/decided continuations; delayed cleanup must preserve permissions from
new definitions. Registration uses the Host definition guard to prevent late
old turns from registering after retirement. HTTP common turns and native A2A proxy
share one HTTPRuntimeManager, Card generation and credential listener.

Trusted client scope includes Route, VirtualKey, Agent ID and runtime definition.
Do not derive ownership from caller-supplied session or permission identifiers.
ACP's process-local native ownership proof remains separate from HTTP context/task
claims and concurrent occupancy. Evicted or restart-lost ACP ownership fails
closed; HTTP claims preserve expiration and continuation rules. Capacity constants
are separately declared even while both currently equal 1024.

Preserve recursive builtin force/graceful cancellation, ACP permission continuation
and pagination, native HTTP proxy fidelity and definition-fingerprint retirement.
Read pkg/acp, pkg/agent/builtin and pkg/a2a AGENTS before changing behavior.

## Runtime-config snapshots (`runtime_backends.go`)

`ACPBackend` builds the canonical `agent_id -> hostconfig.Config`
snapshot solely from `Agent.runtime.acp` during the three-stage Agent
definition commit. A changed fingerprint, disabled state, runtime switch, or
deletion retires stale pools and drains pending permissions before the
replacement generation becomes dispatchable. There is no ACP service store or
fallback configuration source.

The shared `HTTPRuntimeManager` and Path B adapter (`HTTPBackend`) are implemented
according to
[`docs/design/http-agent-runtime.md`](../../../docs/design/http-agent-runtime.md).
`HTTPRuntimeManager` is the sole definition listener/snapshot
owner for both HTTP paths. It imports `pkg/a2a/card`, owns typed
`pkg/a2a/client` clients, publishes separate Path A
proxy-ready and Path B executable views, and is exposed through `AgentGateway`
so the dispatcher never reads config or reaches into a backend.
`HTTPBackend` then registers next to `ACPBackend`/`BuiltinBackend` and composes
that manager; it must not own a parallel Card cache or register another Agent
definition listener. The manager's per-fingerprint Path B execution handle
owns the client and bounded claim/binding/run registries. Its client transport
resolves the stored `auth_ref` and refreshes OAuth credentials on every
southbound operation; no resolved secret is retained in the handle. HTTP
credentials use the exact non-provider scope `http-agent:<agent_id>`, leave
provider fields empty, and may not be borrowed across Agents or from an LLM
provider. The scope prefix is canonical lowercase, while the Agent-id suffix is
case-sensitive just like `Agent.ID`; empty Agent ids fail closed. These mutable
resources are separate from its immutable configuration snapshot and retire
together on definition replacement. The execution fingerprint
covers `card_url`, selected interface URL/tenant, protocol, `auth_ref`, and
transport policy, and any change retires active claims/task bindings before the
replacement generation dispatches. Interrupted bindings are atomically claimed
per `(agent_id, session_id)` so concurrent continuations fail `session_busy`.
HTTP Agents stay `runtime_not_executable` on `protocol agent` until the backend
registration exists; Path A uses the manager's independent proxy-ready gate.
The manager compares a separate definition-input fingerprint before any Card
fetch. It separately tracks Card-input and secret-free credential-eligibility
fingerprints, reuses cached parsed Card candidates for credential-only changes,
inherits unchanged accepted snapshots/resources across full-generation
listener calls, and fetches only new/Card-input-changed Cards or entries
without accepted Card state with bounded concurrency. A secret/OAuth-token
rotation with unchanged eligibility must not fetch a Card, replace the client,
or retire bindings.
Disabled HTTP Agents perform no Card/client I/O. Each definition fetch is
capped at four seconds and by the remaining five-second prepare budget;
one changed entry's failure publishes only that entry non-ready and cannot
degrade inherited entries. Only entries that actually require a Card request
enter the bounded fetch pool; fingerprinting and unchanged-entry inheritance
happen before admission. A changed entry that times out waiting for admission
keeps its new fingerprints and enters the ordinary Card retry path rather than
falling back to stale execution state. Failed Card inputs retry with bounded
exponential backoff by triggering a coalesced Agent Recommit; retries never
mutate a committed generation in place. Unrelated generations inherit the
failure during its backoff window without another request. Retries are canceled
by success, disablement, deletion, or changed Card input. Health reports the
committed `config_error`,
coalesces only by execution fingerprint without serializing unrelated Agents,
and drops retired cache keys. A coalesced health fetch owns its independent
ten-second budget; caller cancellation ends only that caller's wait and never
poisons the shared result or its cache. Retiring an execution first arms every
active run for cancellation and issues bounded, best-effort `CancelTask` calls for
already-bound task ids, then closes its owned transport's idle connections.
`pkg/a2a/card` returns ordered exact-`"JSONRPC"` interface candidates and
structured security alternatives without credential state. This manager alone
applies same-origin plus exact-owner credential policy and chooses the first
surviving interface and satisfiable security alternative. Same-origin compares
scheme, hostname, and effective port, so omitted `:443`/`:80` equals the
corresponding explicit default port. Enforce the design's
1 MiB Card, 4 MiB request/non-stream response, 1 MiB SSE event, and 64 MiB
aggregate stream limits. `SUBMITTED` is a valid non-terminal task state;
`AUTH_REQUIRED` terminates the common run while retaining only validated
`contextId`, never a resumable task id.
A direct Message response likewise retains a returned non-empty `contextId` as
a context-only session binding; it never creates a resumable task binding.
The manager is also a credential lifecycle listener. Maintain a committed
`auth_ref -> agent_id` reverse index, coalesce dependency-scoped Agent
Recommits outside credential/Agent manager locks only when the secret-free
eligibility fingerprint changes, and recompute readiness from cached Card
candidates on create/update/delete/replace. Unrelated and same-eligibility
credential updates must not trigger a Recommit; the live RoundTripper remains
the request-time fail-closed check. A changed eligibility notification arriving
while a Recommit is in flight sets a pending flag and causes one follow-up
Recommit; coalescing must not discard the final credential state.

Path B leaves synchronous `SendMessage` blocking; a returned `SUBMITTED` or
`WORKING` Task is invalid, receives one bounded best-effort `CancelTask`, drops
the claimed binding, and returns `turn_failed` without sequencer success
synthesis. Each active run has the design §9.3 pre-bind cancel slot. Admin
cancel before `taskId` arms the slot and returns retryable
`backend_unavailable`; first valid Task binding issues exactly one force cancel.
Disconnect before binding keeps only a bounded cleanup receiver. Terminal,
bind, cancel, and disconnect races must not duplicate `CancelTask` or restore an
ambiguous binding.
