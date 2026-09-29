# Agents Control Plane

Capability status: **Implemented**.

## 1. Purpose

This document defines the product and technical direction for first-class
`agents` support in `agent-gateway`.

The project should not evolve into an agent framework that owns an agent's
internal reasoning loop. Instead, `agent-gateway` should provide the external
control plane for agents:

- manage agent identities and workspaces
- store each agent's runtime-specific configuration and dispatch it through a
  runtime backend
- govern the LLM and MCP resources an agent can use
- observe sessions, transcripts, permissions, usage, and call chains
- provide a stable AgentRoute execution boundary for upper-layer Workflow
  Workers without owning Project, Task, schedule, or handoff state

This is the layer that turns the existing LLM, MCP, ACP, and metrics surfaces
from separate protocol gateways into one agent gateway.

## 2. Product Positioning

The primary user problem is not "proxy one protocol." The primary user problem
is:

> I need to register agents, expose them safely, assign their resources, monitor
> what they are doing, resolve human approvals, and coordinate work across
> agents and tools.

The repository already contains the protocol-level building blocks:

- LLM routes and providers for model access
- MCP services and routes for tool access
- ACP runtime/process management for agent execution
- VirtualKeys and managed credentials for access control
- metrics and interactions for usage, latency, errors, and call-chain traces

`pkg/agent` should be the product layer that composes these building blocks.
It should not replace `pkg/llm`, `pkg/mcp`, or `pkg/acp`; it should organize
them around Agent management, governance, and execution.

The ACP model stores execution config directly under
`Agent.runtime.acp`; `agent_id` owns the runtime pool. There is no independent
ACP management object or store family.

## 3. Explicit Non-Goal

`agent-gateway` should not own general-purpose internal agent orchestration.

The gateway does not provide an internal LLM-native reasoning loop combining
provider calls, memory retrieval, tool execution, and iteration control. Those
concerns belong to dedicated agent frameworks and runtimes.

The gateway does support a **builtin runtime**
(see [5.7](#57-builtin-runtime-adk-hosted-agents)): agents hosted inside the
gateway process, built on eino ADK. This does not conflict with the non-goal,
because the gateway still does not implement a reasoning loop of its own — the
loop, interrupt/resume machinery, and multi-agent topologies are delegated
wholesale to eino ADK as building material, while the gateway owns exactly what
it owns for every other runtime: definition, lifecycle, governance, resources,
and observation. The builtin runtime is a third `runtime.type` behind the same
`agents` model; it must not reshape that model or weaken the external
control-plane boundary.

The control plane is the external service; there is no internal LLM-native
orchestration mode.

## 4. Architecture Boundary

### 4.1 Current Protocol Layers

```text
pkg/llm
  - providers
  - credentials
  - model catalog
  - LLM protocol adapters

pkg/mcp
  - MCP service config
  - discovery and execution
  - runtime request inspection

pkg/acp
  - ACP runtime config
  - ACP route handling
  - codex/opencode runtime process management
  - sessions, transcript replay, permissions, pooled instances

internal/observability
  - LLM/MCP/ACP usage events
  - interaction traces
  - Prometheus counters
```

### 4.2 Agent Layer

```text
pkg/agent
  - agent management model
  - runtime-specific Agent config
  - agent config store manager
  - agent workspace aggregation
  - agent policies
  - observability aggregation
```

`pkg/agent` depends on the lower-level runtime managers and query services. The
lower-level protocol packages must not depend on `pkg/agent`.

```text
pkg/agent
  -> pkg/acp/host
  -> pkg/gateway/agentroute + pkg/gateway/llmroute + pkg/gateway/mcproute
  -> pkg/llm/provider + pkg/credential + pkg/gateway/modelcatalog
  -> pkg/mcp/service + pkg/mcp/runtime
  -> pkg/gateway/virtualkey
  -> internal/observability/usage
```

## 5. Core Concepts

### 5.1 Agent

An `Agent` is a first-class management object. It represents an operator-facing
agent identity, not a protocol-specific service.

Stored shape:

```json
{
  "id": "coding-agent",
  "name": "Coding Agent",
  "description": "Codex-backed development agent",
  "runtime": {
    "type": "acp",
    "acp": {
      "agent_type": "codex",
      "cwd": "/workspace",
      "allowed_roots": ["/workspace"],
      "default_model": "gpt-5",
      "permission_mode": "interactive",
      "idle_ttl": "30m",
      "max_instances": 4,
      "codex": {
        "mode": "adapter",
        "adapter_command": "codex-acp"
      }
    }
  },
  "routes": {
    "llm_route_ids": [],
    "mcp_route_ids": []
  },
  "resources": {
    "provider_ids": ["openrouter-main"],
    "mcp_service_ids": ["filesystem-tools"],
    "virtual_key_ids": ["coding-agent-key"]
  },
  "policy": {
    "max_agent_depth": 3,
    "budget": {
      "max_turns_per_day": 500,
      "max_tokens_per_day": 2000000
    }
  },
  "disabled": false,
  "created_at": "2026-06-20T00:00:00Z",
  "updated_at": "2026-06-20T00:00:00Z"
}
```

The released stored shape is the Agent-owned schema above. Pre-unification
databases used a second ACP identity and runtime-specific route references;
startup detects those shapes and requires offline migration.

The model defines three built-in runtime types, split by a single axis —
**who owns the agent's
lifecycle, and whether there is a separate process at all**:

- `acp`: the **gateway** owns the lifecycle of an **external process**
  (process pool, sessions, permission flow, transcript). For local,
  embeddable agent executables the gateway should drive directly. A bespoke
  executable that needs this depth should be wrapped to speak ACP (the same
  way `codex-acp` bridges codex), not given a new runtime type.
- `http`: the **agent service** owns its own lifecycle; the gateway is only a
  client that hands it a task and observes the result. For business agents that
  expose a network endpoint and consume LLM/MCP through `resources`.
- `builtin` (summarized in [5.7](#57-builtin-runtime-adk-hosted-agents)):
  there is **no separate process**. The agent is a persisted definition
  materialized inside the gateway process by an eino-ADK-based host. The
  gateway owns the agent's entire existence, not just a process around it.

A `runtime.type = "http"` agent carries an `http` block instead of `acp`.
The persistable and executable schema defined by
[HTTP Agent Runtime](http-agent-runtime.md) uses `card_url`, required
`protocol` (`a2a`; `custom` is reserved and rejected), optional `auth_ref`, and
`timeout_seconds`. The obsolete design-only `endpoint` field is not accepted:

```json
"runtime": {
  "type": "http",
  "http": {
    "card_url": "https://agents.internal/.well-known/agent-card.json",
    "protocol": "a2a",
    "auth_ref": "agent-callback-key",
    "timeout_seconds": 300
  }
}
```

A `runtime.type = "builtin"` agent carries a `builtin` block holding the agent
definition itself (model binding, prompt, tools, topology — see
[Builtin Agent Runtime](builtin-agent-runtime.md#4-definition-schema) for the
full schema). Like ACP and HTTP, its runtime-specific definition is Agent-owned;
none of the target runtime types introduces a second management identity.

Crucially, **LLM and MCP are resources, not runtime types**. An agent's ability
to use models and tools lives in `resources` (and is governed there), regardless
of runtime. The runtime field only describes how the gateway dispatches work and
observes it. There is intentionally no "native non-ACP external-process
lifecycle" runtime: for a separate executable, that need collapses into `acp`
(wrap it) or `http` (don't manage its lifecycle). `builtin` is not that case —
it has no executable at all. See [5.4](#54-runtime-backends) for the executor
contract and the SPI escape hatch.

#### Generic policy vs runtime-specific config

Keep `policy` for cross-runtime governance only. Fields whose meaning depends on
the runtime backend belong under `runtime.<type>`, not under `policy`:

- `policy` holds runtime-agnostic governance: `max_agent_depth`, `budget`, and
  later schedule enablement, retention, and transcript visibility.
- `runtime.acp` owns `agent_type`, cwd/allowed roots, default model, environment,
  config overrides, pool limits, permission mode, and agent-specific adapter
  config.
- `runtime.http` owns the Agent Card URL, southbound `protocol` (A2A Protocol
  1.0 JSON-RPC first), auth, and timeouts. The actual service URL and tenant
  come from the selected, validated Card interface. See
  [HTTP Agent Runtime](http-agent-runtime.md).
- `runtime.builtin` owns the in-process definition.

The Agent's top-level `id`, `name`, `description`, `disabled`, and timestamps
remain generic identity/lifecycle metadata and are not repeated inside
`runtime.acp`. Conversely, ACP execution fields do not move into `policy`.
This leaves one source of truth without erasing the runtime-specific schema.

Reusable normalization and validation live in `pkg/acp/hostconfig` without
service-management fields. The Agent adapter converts `Agent.runtime.acp` to
that type; `pkg/acp` does not import `pkg/agent`.

#### Source of truth: `runtime` vs `routes`

`runtime` is authoritative for execution. For an ACP-backed Agent,
`runtime.acp` is the complete execution configuration and `agent_id` is the
owner key passed separately to the ACP runtime manager. AgentRoute uses one
ownership direction: `AgentRoute.agent_id` targets the Agent. There is
no persisted reverse `agent_route_ids` list. Workspace views derive ingress
routes from the in-memory AgentRoute snapshot. `routes.llm_route_ids` and
`routes.mcp_route_ids` remain because they describe resources the Agent may use,
not ingress ownership.

AgentRoute management validation requires the target Agent to exist, but does
not require it to be enabled or currently executable. Disabled Agents and HTTP
Agents whose runtime is not ready may be configured in advance; capability
and workspace views expose that state, while dispatch fails with
`agent_disabled` or `runtime_not_executable` before backend invocation.

The removed pre-unification tree implemented this indirectly through a second
ACP identity and runtime-specific ingress route families. That shape is
accepted only by the versioned offline migration helper, never at runtime.

#### Cardinality: one agent, one runtime

An `Agent` selects **exactly one** runtime backend type and owns one runtime
configuration block: `runtime.acp`, `runtime.http`, or `runtime.builtin`. It is
not a fan-out container over several backends. AgentRoute ingress resolves this
one Agent and its one runtime; it is not a way to aggregate multiple runtimes
under one identity.

This is deliberate, for three reasons:

- **Attribution stays unique.** AgentRoute supplies `agent_id` directly and the
  runtime manager carries it through native events.
- **The agent does not become an internal orchestrator.** Selecting among or
  dispatching across several real backends is internal-loop behavior, which is an
  explicit non-goal (see [3](#3-explicit-non-goal)).
- **Lifecycle semantics stay clean.** An ACP session is pinned to an instance in
  the Agent-owned pool and cannot be freely moved across Agent identities.

Therefore:

- **Multiple real agents** are modeled as multiple `Agent` objects. Coordinating
  them is a layer *above* Agent Gateway. An upper-layer workbench owns the
  Project/Team/business-Task model and a durable engine such as Temporal owns
  the Workflow execution. Its Worker invokes one Agent through AgentRoute for
  each AI Activity. A→B handoff is a Business Workflow edge, not a
  gateway-owned Agent DAG. See
  [Gateway Request Pipeline And External Orchestration](request-pipeline.md).
- **One logical agent over interchangeable backends** (failover / load balance /
  A-B) is a different need and is out of scope here; see Open Questions.

#### Cardinality: one Agent, one runtime owner

`agent_id` is the sole owner key for ACP pools, instances, sessions, pending
permissions, transcripts, native recovery operations, and usage. There is no
second shareable runtime object and therefore no service-to-Agent cardinality
rule or ambiguous shared-service mode. Multiple Agents may carry identical ACP
configuration values, but they still materialize isolated pools keyed by their
different Agent IDs.

An ACP config update changes the runtime fingerprint: in-flight work drains,
old instances accept no new turns, and subsequent turns use the new config.
Deleting the Agent or changing away from `runtime.type = acp` retires its pool
and fails pending work closed, so no runtime survives without an owning Agent.

#### Identity and resource enforcement

An `Agent` is a management-plane identity, not an automatically trusted
data-plane principal.
End-user requests still authenticate with a `VirtualKey` against a route; the
agent object does not appear in the request path. Therefore:

- `resources` is a *management view* of what the agent is allowed to use,
  assembled and validated at the admin layer. It is not enforced inline on the
  callback data-plane request path.
- Data-plane enforcement continues to come from VirtualKey + route policy.
  Binding a `VirtualKey` to an agent means the operator has scoped that key to
  the agent's routes/services; the gateway does not introduce a separate
  per-request "agent principal" check yet.
- A dedicated agent-as-principal model (where the request path resolves an agent
  identity and enforces `resources` directly) is deferred until there is a
  concrete isolation requirement; see Open Questions.

AgentRoute resolves `agent_id` on the request path for runtime selection,
attribution, common policy, and capabilities. That relationship
does not by itself make all external ACP/HTTP `resources` references enforced
entitlements; scoped callback identity and resource enforcement remain a
separate design decision.

### 5.2 Agent Workspace

An `AgentWorkspace` is a read model for the UI. It aggregates the things an
operator needs on one agent detail page.

It is not a stored object. It is assembled from:

- the `Agent` object
- the Agent-owned runtime config
- AgentRoutes that target the Agent
- runtime pooled instances and in-flight turns keyed by `agent_id`
- pending ACP permissions
- session and transcript **references** (counts + links), not full content
- LLM/MCP resources linked by policy
- metrics events and interaction traces filtered by
  agent/route/run/session/trace

The workspace is a **summary/index**, not a content aggregator. It returns
summaries, counts, runtime state, and links/references that let the frontend call
the dedicated Agent capability endpoints (`GET /<agent-route>/sessions`,
`GET /<agent-route>/sessions/{id}/transcript`) when the operator drills in. It must
not eagerly pull session transcripts: doing so would make one workspace call
unbounded in size and would entangle pagination, permissions, and performance
into a single endpoint. Transcripts and full session lists stay behind their own
paginated endpoints; the workspace only points at them.

The workspace is keyed off `runtime.type`: an `http`-runtime
agent has no gateway-owned pooled instances, sessions, transcripts, or ACP
permissions, so its workspace degrades to the runtime-agnostic parts (the
Agent object, linked resources, tasks, and metrics/interaction traces). Do not
hard-code ACP fields as required in the workspace shape.

### 5.3 External Business Tasks

An Agent Gateway turn is an execution primitive, not a durable business Task.
The gateway accepts one request through AgentRoute, dispatches it through the
Agent's selected runtime backend, streams its common events, and exposes exact
run cancellation and inspection where supported.

An upper-layer product may represent that turn as an AI Task or durable
Workflow Activity. That product owns Project membership, assignment,
scheduling, approval, retry policy, and durable state. Its Workflow Worker
calls `POST /<agent-route>/turn` using scoped gateway credentials and persists
the mapping between its business task id and the returned gateway run and
interaction ids. Agent Gateway does not expose a second AgentTask object or
gateway-owned Task state machine.

### 5.4 Runtime Backends

A runtime backend is the turn-first seam between a stable Agent identity and
one native execution runtime. It is selected by `agent.runtime.type` and is
shared by direct AgentRoute callers and upper-layer Workflow Workers.

The required contract lives in `pkg/agent/runtime`:

```go
type Backend interface {
    RuntimeType() string
    Capabilities(context.Context, Agent) (Capabilities, error)
    ServeTurn(context.Context, Agent, TurnRequest, EventSink) error
}
```

`TurnRequest.Options` is not a flat union of backend fields. It uses the
versioned `v1` envelope: northbound input
contains an optional strict `runtime` JSON object, while trusted gateway-only
execution metadata is carried separately and is never decoded from AgentRoute
JSON. The selected backend strictly decodes its runtime object and rejects
unknown or foreign options with `unsupported_option`.

Optional capabilities are narrow interfaces such as `SessionLister`,
`TranscriptLoader`, `PermissionResolver`, `RunCanceller`, `RuntimeInspector`,
and `HealthChecker`. Unsupported capabilities fail closed; a backend never
silently emulates them or falls through to another runtime.

There is deliberately no task-first `StartTask` SPI. `ServeTurn` is the stable
data-plane operation for interactive callers and external Workflow Activities.
The external engine owns durable state, scheduling, retry, human approval, and
handoff; the backend owns one Agent turn and its native capability behavior.

#### Runtime categories and adapters

The classification axis is **who owns the agent's lifecycle, and whether a
separate process exists**, which yields three Agent runtime categories. Each
runtime executes behind an `agentruntime.Backend`. HTTP behavior is defined in
[HTTP Agent Runtime](http-agent-runtime.md): A2A Protocol 1.0
JSON-RPC is the first southbound dialect under `runtime.http.protocol`,
reached through a shared `pkg/a2a` protocol package, the translating
`HTTPBackend` translation path, and the governed `protocol a2a` JSON-RPC proxy
path.

- **`acp`** — the gateway owns the agent's external process lifecycle. Its
  adapter translates the Agent-owned `runtime.acp` block into
  `hostconfig.Config` and invokes the pool with `agent_id` as owner, reusing
  sessions, scope rebind, permission flow, and transcript. A turn ending does
  not tear down the process; the pool governs it by `IdleTTL`.
- **`http`** — the agent service owns its lifecycle. Its Path B adapter
  resolves `runtime.http.card_url`, then dispatches to the selected Card
  interface over the dialect selected by `runtime.http.protocol` (A2A
  Protocol 1.0 JSON-RPC first; see
  [HTTP Agent Runtime](http-agent-runtime.md)). The adapter lives in
  `pkg/gateway` and calls `pkg/a2a`; it does not own durable remote-task
  state. A remote stateful agent still fits here; its session is an id
  passed over HTTP, not a process owned by the gateway.
- **`builtin`** — there is no separate process. The adapter invokes the
  in-process ADK host (see [5.7](#57-builtin-runtime-adk-hosted-agents)),
  materializing or reusing the definition graph and translating Runner events
  into the common envelope.

#### No bespoke external-process backend

There is intentionally no bespoke "native, non-ACP, gateway-managed lifecycle"
backend **for external executables**. That combination is contradictory: needing
gateway-managed lifecycle for a separate process *is* what ACP is for, so the
answer is to wrap as ACP rather than reinvent it. Concretely:

- needs gateway-managed lifecycle for an executable (pool/sessions/permission)
  → wrap as `acp`
- does not need it (remote / self-managed / stateless) → `http`
- is not an executable at all, but a declarative definition the gateway can
  host → `builtin`

#### SPI extension point

The backend registry rejects duplicate runtime types at startup. An Agent whose
runtime backend is not linked remains manageable but is not executable and
fails with `runtime_not_executable`. A later runtime category can add another
adapter behind this registry without adding another route family or changing
the AgentRoute contract.

#### Executor contract

Every backend must accept the runtime-neutral turn identity, emit the ordered
common event envelope, report exactly one terminal result, and expose
capabilities honestly. An external Workflow Activity may retry a turn only
when the adapter can propagate or enforce a stable caller-supplied logical
execution key; otherwise the Worker must select non-retryable/at-most-once
behavior.
Cancellation, permission, session, transcript, health, and inspection behavior
are exposed only when the corresponding optional capability is implemented.

The common run registry owns exact-run control identity. Active entries hold
backend cancellation bindings; completed entries become process-local,
10-minute terminal tombstones capped at 1,024 per Agent. Repeated cancellation
of a retained terminal run returns its terminal result without re-invoking the
backend. A `run_id` cannot be reused while its tombstone is retained; external
Activity retries keep the stable logical execution key separate and allocate a distinct
per-attempt `run_id`. Durable business history, when needed, belongs to the
external Workflow engine and upper-layer projection.

The common permission broker owns pending identity, expiry, atomic claim, and
audit. A broker record contains an unguessable opaque backend token; ACP waiter
state and builtin checkpoint/calls/transcript/trace state stay in backend-owned
stores and are resolved through the selected adapter. Decision, expiry,
cancellation, Agent deletion/runtime switch, adapter failure, and process
shutdown consume the common claim once and clean up fail-closed. A backend
store is never an independently claimable permission registry. After claim,
the broker retains only bounded-lifetime owner/runtime routing metadata so
concurrent ACP route, ACP Admin, and Agent Admin decisions still converge on
the common one-shot result instead of falling through to native waiter state.

Expiry scheduling is broker-owned and invokes the same atomic claim path as an
operator decision. Backend continuation stores do not sweep by wall clock.
Common permission listing exposes only allowlisted action ids/display names
and ACP option ids/kinds/display names;
it never contains native payloads, tool arguments, checkpoints, transcripts,
or trace-link data. Gateway shutdown closes the broker first, rejects late
publications, drains pending continuations, and then tears down the runtimes.

ACP advertises `resume_mode=active_stream` and delivers a claimed decision to
its live waiter. Builtin advertises `resume_mode=new_stream`: an Admin decision
stores a validated, decided continuation without running it, and a later
AgentRoute `POST /turn` consumes it while owning the continuation SSE stream.
A decision submitted on builtin `POST /turn` claims and consumes in that same
request. The Admin endpoint never starts a builtin continuation in the
background because the gateway has no caller-independent business execution
owner or headless event sink.

### 5.5 Agent Policy

Agent policy is external governance. It should control the resources and
operator boundaries around the agent, not the internal reasoning algorithm.

Runtime-agnostic policy areas (live under `policy`):

- max agent depth
- budget and quota
- retention and transcript visibility

Schedule policy belongs to the upper-layer workbench and its durable engine.

Runtime-specific config areas (for `acp`, owned by `runtime.acp`; see
[5.1](#51-agent)):

- permission mode and approval routing
- cwd and allowed roots for ACP-backed agents

These are surfaced directly in the workspace subject to secret redaction and
updated only through Agent CRUD. The ACP runtime manager receives a validated
protocol-owned copy and never becomes a second configuration authority.

Resource-scoping references (live under `resources` and `routes`):

- exposed routes
- allowed VirtualKeys
- allowed MCP services and tools
- allowed LLM providers and models

See [5.1](#51-agent) for why runtime-specific governance is kept out of the
generic `policy` block.

### 5.6 Agent Attribution

Usage and interaction events carry a nullable, durable `agent_id`. AgentRoute
ingress stamps the target Agent directly. Nested LLM and MCP resource traffic
may resolve an Agent through an immutable resource-route index owned by the
Agent manager; the hot path never reads the config store.

Lower protocol packages do not import `pkg/agent`. They consume the neutral
`AgentAttributor` interface exposed by the observability layer. Attribution is
written only when ownership is unambiguous. Per-Agent queries prefer the
durable tag and use owned resource-route ids only to recover historical or
nested LLM/MCP events. Retained ACP `service_id` columns are protocol history,
not active ownership keys.

Cross-Agent handoff origin belongs to the upper-layer Business Workflow. The
gateway must not infer it from a gateway-owned graph or trust an unverified id
from turn JSON.

### 5.7 Builtin Runtime (ADK-Hosted Agents)

Builtin is the in-process runtime behind the shared `Agent` model. The gateway
materializes a persisted `runtime.builtin` definition into an eino ADK graph;
eino owns reasoning and topology execution while the gateway owns definition,
lifecycle, governance, resources, and observation.

Models resolve through the Agent's LLM routes, tools resolve through declared
MCP services, and ingress uses the same `AgentRoute.agent_id` relationship as
ACP and HTTP. Builtin-specific sessions, interactive tool permissions, and
force/graceful cancellation are runtime capabilities rather than a separate
Task model. See [Builtin Agent Runtime](builtin-agent-runtime.md).

## 6. Admin API Boundary

The `/admin/agents` endpoints are the product-level API for Agent management
and UI aggregation. They include AgentRoute and common runtime capabilities;
ACP service CRUD is not a product surface.

These endpoints are management-plane APIs. They are not the primary data-plane
entrypoint for end-user chat or task execution. End users and business apps
call unified AgentRoute endpoints:

```text
POST /<agent-route>/turn
POST /<agent-route>/permission
GET  /<agent-route>/sessions
GET  /<agent-route>/sessions/{session_id}/transcript
```

ACP-specific optional fields and capabilities continue through the adapter. Likewise,
agents continue to access LLM and MCP resources through the existing LLM API
and MCP route surfaces. `/admin/agents` coordinates and observes those
surfaces; it does not replace them.


### 6.1 External Workflow Integration Surface

Agent Gateway does not add durable Business Workflow
Definition/Run/Schedule endpoints.
An upper-layer workbench starts and queries its own Temporal (or equivalent)
Workflows. Its Workers use the existing gateway surfaces:

- AgentRoute for one Agent Activity and its common event stream;
- exact-run cancellation and capability APIs where advertised;
- LLM and MCP routes for separately governed resource Activities;
- interaction and metrics APIs for correlation and usage projection.

The Worker authenticates every call with a scoped gateway identity. A Temporal
Workflow id is correlation data, not authorization. The upper layer persists
the mapping between Project/Task/Workflow ids and gateway run, interaction, and
trace ids.

### 6.2 Multi-Agent And Human Workflow Boundary

Multi-Agent handoff, human approval, scheduling, retry across process failure,
and long-running history belong to the external Workflow. Each AI node invokes
exactly one managed Agent through its AgentRoute. Agent Gateway neither stores
the business graph nor exposes `/admin/agent-workflows`, `/admin/workflow-runs`,
or schedule APIs.

Gateway-native permissions remain runtime capabilities inside one live turn;
they are not Project approval Tasks. The complete boundary and reference
Temporal topology are in
[Gateway Request Pipeline And External Orchestration](request-pipeline.md).


## 7. Open design questions

The following choices are intentionally unresolved and require separate design
work before they become product commitments:

- which shared budget model is enforced first: tokens, cost, turns, or a
  combination;
- which authenticated correlation envelope external Workflow Workers use
  without allowing callers to spoof principals, budgets, or traces;
- which backends can enforce a stable external Activity execution key and what
  retry guidance capability discovery exposes;
- whether memory becomes an Agent resource and, if so, whether its first
  contract is enforcement or observation;
- whether external ACP and HTTP Agents receive callback credentials that
  enforce declared resources directly, rather than relying only on VirtualKey
  and route policy;
- whether a logical Agent may select among interchangeable runtime backends.
  This is cleanest for stateless HTTP targets and requires an explicit session
  affinity model for ACP;
- the builtin-specific constraints recorded in
  [Builtin Agent Runtime](builtin-agent-runtime.md#11-current-constraints-and-open-design-questions).
