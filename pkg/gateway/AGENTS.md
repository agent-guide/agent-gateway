# pkg/gateway — AGENTS.md

Scope: the runtime gateway core, route-model packages, and configuration Bundle package under
`pkg/gateway/`. Paths are repository-root relative. The global rules in the
root `AGENTS.md` (change policy, terminology, build/test) apply here.

## Runtime core

Provider and route resolvers use `internal/configresolve` for generic config
decoding, fingerprinting, and versioned materialization. Keep domain-specific
validation and resolution in the Gateway packages.

Important files:

- `agentgateway.go`: runtime route, VirtualKey, and provider resolution
- `providerresolver.go`: static and dynamic provider resolution

Dynamic provider configs are cached after first load and invalidated through
the manager's create/update/delete paths. Do not put config-store reads back in
the per-request provider resolution hot path.

`AgentGateway` is the main runtime object. It resolves routes, validates VirtualKeys, and selects providers. It does not own the HTTP protocol details.

`RoutedProvider` selects managed credentials and invokes the credential
manager's request-time external refresh hook before attaching a
`oauth_token` to the provider context. Provider-specific login and token
refresh behavior are external to this repository and owned by `agw-auth`.
Its enriched `ExecuteChat` / `ExecuteStreamChat` methods return the one
`ResolvedExecution` that served the request, including the candidate's
provider, client/upstream model, protocol dialect/features, and credential
attribution. The ordinary provider methods are compatibility adapters. Never
mutate the caller's `ChatRequest.Model`; rewrite only an attempt-local clone.
Protocol response coordinators use the enriched path so mode selection reads
the served candidate and one lifecycle owner writes attribution plus usage.
The protocol handler attaches its immutable `ProtocolRequirementSet` to both
the route request and `ChatRequest.ProtocolState`; `RoutedProvider` asserts they
agree but never inspects message extras to derive stronger requirements.
Candidate filtering returns typed `RequirementGap` values and is generic set
inclusion over registered feature IDs.

Runtime route matching uses the immutable manager match table. Bootstrap and
route manager create/update/delete/refresh keep that snapshot populated; do not
reintroduce per-request config-store `List` calls for matching.

`controlplane.go` composes manager mutation guards. Provider/Route/model and
credential ownership writes serialize under its process-local lock; Agent
mutations acquire their write lock first, then coordination, then any route
write lock. Never acquire the Agent mutation lock under coordination. Direct
composed-manager calls have the same reference checks as Admin/Bundle.
Provider deletion blocks all LLM route and Provider-credential references,
including disabled entries, and deletes its ProviderModels in one SQLite
transaction with readback before cache retirement. Unsupported transactions
fail explicitly. Agent Resources declarations are retained as management links;
HTTP credential revocation remains allowed and fails closed at live lookup.

Provider unconfirmed commits/cache publication failures record a pending
operation. GetConfig, ResolveProvider and conflicting mutations fail closed;
Recover rereads authoritative Provider/model facts and verifies the stored
Provider ID matches the requested key before clearing it. Shipping
Agent backends receive `RuntimeControls.CheckExecution`; preserve this gate when
extracting adapters. It rejects pending Agent writes and old captured definition
versions. HTTPRuntimeManager applies the same pending gate to native proxy and
translated execution. No gate reads ConfigStore on a request path.

`routecore.ValidateRouteConfigs` rejects equal-score intersections across all
kinds and static/dynamic routes. Disabled routes reserve space. A2A's effective
match accepts all methods while scoring uses declared methods. Route mutations
serialize validation, persistence and publication; Refresh validates a complete
candidate before replacing its snapshot. Admin List reads do not publish partial
generations, and Get reads only published cached state after initial loading.
Runtime ties return `ErrAmbiguousRoute` with stable diagnostics; never resolve
them by ID sorting. Startup errors direct operators to offline configuration
recovery. `InitStaticRoutes` callers must handle its error.

## `bundle/`

Package `bundle` owns the GatewayBundle schema, YAML encoding/decoding,
environment expansion and local validation. CLI apply/export execution belongs
in `pkg/distribution/ctlcmd`; standalone startup consumes the shared schema.
Keep Bundle loading outside the request path and the gateway runtime core
independent of this configuration entry package.

## Route id convention (`llmroute`, `mcproute`, `agentroute`)

Route ids must be slash-free so they are addressable as a single Admin API path
segment (`/admin/<kind>/routes/{id}`). `Normalize` auto-generates the
deterministic `<kind>:<target>:<path-slug>` when `id` is empty (slug = path
prefix lowercased, non-alphanumeric runs collapsed to `-`, `/` → `root`), and
`routecore.ValidateRouteID` rejects slash-bearing ids at create/validate time.
The id is fully predictable, so other objects (e.g. `allowed_route_ids`) can
reference it before apply; two routes whose paths slugify to the same value
collide and surface as a duplicate-id error, at which point set an explicit id.

Agent manager bootstrap wires resource-route validation for Agent Create/Update:
LLM and MCP references must exist and match their declared kind. This runs under
the Agent mutation lock shared by guarded Admin LLM/MCP route deletion, keeping
reference validation and deletion serialized.

## `llmroute/`

Defines the route model used by static config, the Admin API, and runtime resolution.

Important types:

- `LLMRoute`
- `RouteMatchPolicy` and `RouteAuthPolicy`
- `RouteModelPolicy`
- `modelcatalog.LogicalModel`, `ModelCandidate`, `FallbackPolicy`

The shared persisted envelope is `routecore.RouteConfig` for all kinds.
There is no generic route-level timeout, retry, quota, or rate-limit policy.
VirtualKeys own admission limits; Provider network settings own upstream
timeouts/retries; independent LogicalModels own candidate fallback.

Current route modes:

- `type: models`: `model_targets` explicitly references ProviderModels or LogicalModels under unique route-visible names, with optional `default_model`; no inline candidates or scheduling

Expanded LLM routes use a concrete `*RouteModelPolicy` target, never an interface.
Policy JSON decoding owns discriminator and strict-field validation; only the
shared `routecore.RouteConfig` envelope retains raw target JSON.

All LLM routes expose explicit model references; no Provider-only target or
request-time upstream-name bypass remains. Reject removed fields at raw,
typed, static and dynamic boundaries. The offline converter requires an explicit
model allowlist and creates concrete model records in the same transaction.

Caddyfile/Caddy JSON and startup Bundles support both model resource families.
Static definitions are read-only through Admin and remain snapshot-owned;
static/dynamic identity conflicts fail publication. Provider defaults never
choose a route model. Request resolution always uses model snapshots.

LLM route JSON rejects unknown fields, including nested match/auth fields; use `match_policy`. An explicit empty match remains a catch-all.

The route model uses `protocol` and `require_virtual_key`. Do not reintroduce the old `local API key` naming in new code or docs.

## `modelcatalog/`

This package owns ProviderModel discovery/facts and independent LogicalModel scheduling.
ProviderModels retain compound `(provider_id, upstream_model)` identities; LogicalModels
have stable IDs and candidate ProviderModel references. Both publish defensive snapshots;
request resolution never reads ConfigStore or invokes discovery. Failed Refresh retains
the last validated generation. Composed mutations share the control-plane coordinator.
Route references block model deletion; logical candidates also block ProviderModel and
Provider deletion, including disabled definitions. Old inline route definitions must use
the offline `extract-models` repair; do not add runtime compatibility aliases.

Important types:

- `ProviderModel`
- `ProviderModelSnapshot`
- `LogicalModel`, `ModelCandidate`, `SelectionStrategy`, `FallbackPolicy`

## `mcproute/`

Defines the MCP route config expansion and runtime route model.

Important types:

- `MCPRoute`
- `RouteMatch`
- `RouteAuthPolicy`

Current shape:

- persisted/static MCP routes use `routecore.RouteConfig`
- `MCPRoute` is the single typed form for Admin and runtime, with direct `connector_id` access
- prefer `*MCPRoute` at runtime rather than copying `MCPRoute` values
- route ids follow the shared convention above with the `mcp:<connector_id>:<path-slug>` shape

## `agentroute/`

Defines the unified `kind=agent` ingress route model described by
[`docs/design/agents-control-plane.md`](../../docs/design/agents-control-plane.md): an AgentRoute targets a stable
`agent_id`, and the resolved Agent's `runtime.type` selects the execution
backend, so a runtime change never changes the route id, URL, or VirtualKey
allowlist. `Normalize`/`ToConfig` accept `protocol = agent` or `protocol = a2a`;
the latter is Path A of
[`docs/design/http-agent-runtime.md`](../../docs/design/http-agent-runtime.md)
on the same kind and does not change that ownership rule. Route ids follow the shared convention with the
`agent:<agent_id>:<path-slug>` shape. `AgentRouteResolver.CreateConfig`/
`UpdateConfig` validate target existence through the optional `AgentLookup`
(wired to `agent.Manager.HasAgent`). Native A2A routes additionally require a
trusted host, GET+POST reachability, and a ready shared HTTP proxy snapshot;
ordinary `protocol=agent` routes may still target disabled or currently
non-executable Agents and fail at dispatch with their normalized runtime error.
Its public surfaces are `/admin/agents/routes`, bundle `agentRoutes`,
CLI `agent-route`, and dispatcher `EnableAgent`/`agent`.

## `virtualkey/`

This package owns VirtualKey extraction, validation, and storage-facing helpers.

Use this terminology consistently:

- `VirtualKey`
- `VirtualKeyManager`
- `VirtualKeyStore`

Current shape:

- `VirtualKey.ID` is required and is the management/storage primary key
- `VirtualKey.Key` is the bearer key value used at request time and is generated by the gateway
- Caddyfile and standalone static bundle config do not support static virtual keys; create them through the Admin API
- optional `rate_limits` policies enforce request token buckets through the domain-owned LimiterBackend for
  LLM, MCP, and unified agent ingress. Admission runs centrally after VirtualKey
  validation and interaction-span setup, before protocol dispatch. Limiter
  defaults to process-local memory. Immutable per-key/dimension generations fence
  update/delete/reset; unchanged dimensions retain tokens. Alternate backends are
  selected through the schema-1 `online.agentguide.community.limiter` extension.
  Failures default to HTTP 503; explicit fail-open is degraded, exhaustion is 429.
  Context budgets default to 100 ms (1–1000 ms); cancellation never fails open,
  and uncertain operations are never retried. Prepared backend handles must
  preserve generation-scoped quota state across unrelated extension refreshes. Internal builtin LLM/MCP calls do not consume
  ingress buckets unless they re-enter the dispatcher.

The gateway accepts a VirtualKey from either:

- `Authorization: Bearer <key>`
- `x-api-key: <key>`

## Extension assembly

Caddy and standalone supply an immutable linked registry to Bootstrap. The
`extensions` manager owns reference-preserving ConfigStore envelopes and
prepares full dependency generations before persistence/publication. Requests
using module resources retain a `Lease` until completion; never read ConfigStore
or resolve factories in a request path. Read `pkg/extension/AGENTS.md` before
changing this wiring.

Run virtualkey/extension/dispatcher/Admin race tests and the independent consumer
limiter suite when changing admission. Counters label only dimension/outcome and
bounded backend type, never identities. Backend switching resets without transfer.

Common HTTP Agent session bindings include the authenticated route/VirtualKey
scope. Cross-client and unknown supplied sessions fail session_not_found before
remote execution; trusted Admin/unscoped runtime tests retain inspection semantics.

ACP data-plane clients must prove session/permission ownership in the validated
VirtualKey/route/runtime scope. Namespace thread keys before native dispatch.
Preserve the native session-list contract and filter results by proven ownership;
never replace it with an identifier-only list or disable pagination. Ownership
eviction/restart fails closed; Admin inspection retains its existing authority.

Both hosts construct credential/scheduler and usage handles through
`internal/serverassembly`. Its lifecycle calls Gateway.CloseContext before
draining usage and closing storage. Preserve bounded extension teardown errors
in CloseContext; Reset/Close remain convenience calls that discard errors.

Concrete ACP, builtin and HTTP adapters and HTTPRuntimeManager live in
`pkg/agent/runtimebackend/`, which must not depend on this package. Read its nested
AGENTS before changes, including cross-cutting Admin/dispatcher adapters.

Typed LLMRoute, MCPRoute and AgentRouteConfig embed target-free RouteMetadata;
never reintroduce raw JSON plus writable expanded target fields. Get/Match read
one published table. Kind resolver caches use full-content fingerprints and
return defensive metadata/target copies before normalization. Manager mutations
invalidate affected caches even when bypassing resolver CRUD wrappers. Match-table
publication and invalidators are bounded in-memory work; invalidators must not
perform I/O or reenter mutations. Materializer epochs fence late cache builds.

Gateway exposes the single common RouteConfigManager; route-kind resolvers
share that manager rather than retaining duplicate per-kind manager getters.

Connector deletion checks both Agent connector declarations and MCP publishing
route targets under shared coordination. Agent writes require existing connectors
and valid explicit HTTP tool selections. References from disabled objects and
pending retained Agent generations remain protected. Resource CRUD and Bundle
apply are independent, sequential updates; no group publication/recovery exists.

The simplified Connector mutation contract additionally requires all execution
edits/deletion to reject references or active/queued calls/discovery/initialization,
with atomic admission/exclusion. Display edits keep sessions intact; explicit
disablement/credential revocation remain possible. Preserve the same guards in direct manager calls, Refresh, Admin and Bundle. Do not introduce group transactions or old/new session drain.

LLM data-plane model listing returns only route-visible target names from the published policy; it never invokes upstream discovery and requires no default model.

LLM routes must use one model target kind. ProviderModel targets share one Provider
and use real upstream model names; omitted target names derive from upstream_model,
and differing aliases are rejected. LogicalModel routes own the exposed logical
references; cross-provider selection and aliases belong to LogicalModels. Never
mix target kinds. Caddyfile concrete syntax is `target model <provider-id> <upstream-model>`.
