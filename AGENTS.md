# AGENTS.md

## Purpose

This repository builds a custom Caddy binary that acts as an AI gateway for LLM, MCP, and ACP traffic.
The current primary LLM path is:

1. `agent_gateway` app loads providers, routes, virtual keys, and credentials, and configures the external credential refresh command
2. `agent_route_dispatcher` matches an incoming HTTP request to a route
3. the route's `protocol` selects the protocol adapter (`openai` or `anthropic`)
4. the gateway validates the VirtualKey
5. model exposure routes resolve an explicit ProviderModel or independent LogicalModel reference to one concrete `(provider_id, upstream_model)` binding
6. the credential manager invokes the configured external refresh command for an expiring `oauth_token` credential when required
7. the selected provider executes `Generate` or `Stream`

MCP is active through `agent_route_dispatcher` with MCP enabled. ACP, builtin, and HTTP execution enter through unified `kind=agent` routes (`pkg/gateway/agentroute`, dispatcher `agent` enablement, `POST /<agent-route>/turn` SSE); the target Agent's `runtime.type` selects the registered backend. ACP execution config is owned inline by `Agent.runtime.acp`, builtin definitions are materialized by the in-process eino ADK host, and HTTP Agents support both translated common turns and governed native A2A Protocol 1.0 JSON-RPC ingress. The agent control plane is exposed through `/admin/agents` and `/admin/agents/routes`. Memory is not implemented; `/admin/memory/...` is reserved and returns `501 Not Implemented`.

Future gateway Pipeline support is limited to synchronous, request-bound
`llm`/`mcp`/`transform` composition. Durable Project/Team workflows, Agent
handoffs, schedules, human Tasks, and restart recovery belong to an upper-layer
workbench and an external engine such as Temporal; its Workers invoke the
gateway through normal AgentRoute/LLM/MCP data-plane APIs. Agent Gateway does
not own a durable business Workflow state machine.

## Product Site

`website/` holds the static product marketing site for https://agentguide.online
(plain HTML + one shared stylesheet `website/assets/site.css`, no build step).
It is not part of the gateway binary or its tests. Pages: `index.html`,
`platform.html`, `agents.html`, `solutions.html`, `observability.html`,
`why.html`, each with a Chinese translation under `website/zh/` (same file
names, `lang="zh-CN"`, `hreflang` alternates, nav language switcher). A content
change to any page must be applied to both language versions. Keep marketing
claims in sync with actual capabilities; features that are not implemented yet
must be labeled as roadmap on the pages.

## Documentation Ownership

- `docs/design/` records durable decisions, contracts, invariants, tradeoffs,
  and rejected alternatives; it may declare a capability `Implemented` or
  `Proposed` but must not retain completed implementation phases
- `docs/architecture/` describes how the checked-in system currently runs
- `docs/plans/` contains only active, time-bound execution plans and deletes
  them after permanent documentation describes the landed result
- `docs/releases/` records user-visible changes between published tags and is
  the source for hosted release descriptions
- `docs/guides/` is task-oriented and `docs/reference/` is lookup-oriented;
  neither should carry roadmap or implementation status

## Change Policy

- by default, changes in this repository do not preserve backward compatibility
- do not keep legacy aliases, deprecated field names, old route shapes, old module IDs, old CLI flags, or old API-visible IDs unless the change request explicitly requires compatibility
- when renaming or reshaping behavior, update the code, tests, `README.md`, `docs/architecture/architecture-overview.md`, `examples/Caddyfile.example`, this file, and the nearest nested `AGENTS.md` to describe only the current behavior unless compatibility is explicitly required
- Reusable command implementation rules and the exact designated surface list live in
  `pkg/distribution/AGENTS.md`.
- protected Enterprise SPI and reusable distribution entry points follow the
  compatibility policy in
  [`docs/design/enterprise-extension-contract.md`](docs/design/enterprise-extension-contract.md)

## Build & Run

Offline configuration inspection and explicit transactional repairs use
`scripts/repair-architecture-config`; see
`docs/guides/architecture-config-recovery.md`. Recovery reads raw records without
normal startup decoding. Keep its checks aligned with route and Agent contracts;
strict validation changes must retain a tested offline recovery path.

```bash
# Build the main gateway binary, standalone daemon, and management CLI
make build

# Or build only the gateway binary
go build -o agw ./cmd/agw

# Or build only the standalone daemon
go build -o agwd ./cmd/agwd

# Or build only the management CLI
go build -o agwctl ./cmd/agwctl

# Run with a Caddyfile
./agw run --config ./examples/Caddyfile.example

# Format
go fmt ./...

# Static analysis
go vet ./...

# Tests
go test ./...
go test ./path/to/package -run TestName -v
```

Notes:

- `make build` builds `agw` from `cmd/agw/main.go`, `agwd` from `cmd/agwd/main.go`, and `agwctl` from `cmd/agwctl/main.go`.
- The three mains own explicit linked-module imports and delegate to `pkg/distribution/caddycmd`, `daemoncmd`, and `ctlcmd`. Command construction must keep flag state local; dotenv, signals, and process exit belong to execution.
- Both hosts use `internal/runtimeassembly` for credential/scheduler and usage
  construction. Their shared lifecycle retires Gateway resources, drains usage,
  then closes storage; shutdown errors remain inspectable.
- Both server mains pass `distribution.CommunityRuntime()`; CLI passes `CommunityValidation()`. These supply validated manifest metadata, with explicitly injected attribution producers/allowlists and per-lifecycle public sink factories excluded from serialization. Keep declarations aligned with main imports.
- The resulting binary is a standard Caddy binary with custom modules compiled in, so normal Caddy subcommands such as `run`, `reload`, `validate`, and `hash-password` work.

## Core Modules

### Caddy app

- Module ID: `agent_gateway`
- Package: `caddy/gateway/`
- Main entry: `caddy/gateway/app.go`

Bootstraps the config store, loads static providers from the Caddyfile,
creates the shared credential manager and external request-time refresh transport, and creates the
runtime `AgentGateway`. Caddyfile parsing lives in `caddy/gateway/caddyfile.go`.

### HTTP middleware

- Module ID: `http.handlers.agent_route_dispatcher`
- Package: `caddy/dispatcher/`
- Main entry: `caddy/dispatcher/dispatcher.go`

Resolves the matching route, selects the route `protocol`, rewrites the path
by removing the route `path_prefix`, validates the VirtualKey, prepares the
provider payload, resolves the logical model or direct provider target
(rewriting the provider-facing model for logical-model routes), and invokes the
LLM protocol handler. The runtime `RoutedProvider` selects and, when required,
refreshes an OAuth-backed credential before provider execution. With `mcp` or
`agent` enabled the dispatcher also
resolves those route kinds and dispatches to `pkg/connector` or the
registered `pkg/gateway/runtimebackend` ACP/builtin adapters, tracks in-flight turns, and
stamps Agent identity on the interaction span. Subsystem detail:
`pkg/gateway/AGENTS.md`, `pkg/acp/AGENTS.md`, `pkg/agent/AGENTS.md`.

### Protocol handler modules

- Module ID: `agent_route_dispatcher.llm_apis.openai`
  - Runtime package: `pkg/dispatcher/llmapi/openai/`
  - Caddy adapter: `caddy/dispatcher/llmapi/openai/`
- Module ID: `agent_route_dispatcher.llm_apis.anthropic`
  - Runtime package: `pkg/dispatcher/llmapi/anthropic/`
  - Caddy adapter: `caddy/dispatcher/llmapi/anthropic/`
- Module ID: `agent_route_dispatcher.llm_apis.cc`
  - Runtime package: `pkg/dispatcher/llmapi/cc/`
  - Caddy adapter: `caddy/dispatcher/llmapi/cc/`

The `anthropic` and `cc` runtime packages are thin ingress profiles over the
shared Anthropic Messages core in `pkg/dispatcher/llmapi/anthropicmsg/`. That
core owns parsing, conversion, response lifecycle, and SSE block state.
Protocol-native state uses the scoped, dialect-neutral
`provider.ProtocolState`; same-dialect responses can select validated native
body or stream relay from the `ResolvedExecution` returned by the enriched
`RoutedProvider` contract.

Responsibilities:

- parse wire-format requests
- convert HTTP payloads into `provider.ChatRequest`
- convert provider responses back to protocol-specific JSON or SSE

Responses function namespaces use reversible execution-local flattening in
Responses-via-Chat; native Responses defaults to preservation. Native endpoint
providers `openai`, `grok`, and `codex` accept
`options.responses_tool_namespaces` (`native` or `flatten`). Never deduplicate
same-name tools across namespaces. Rewrite choice/history and restore ordinary,
SSE and final response identities, including raw JSON. Flattening requires
explicit input history, never `previous_response_id`; custom/deferred/search
semantics require a native path. See `pkg/llm/AGENTS.md`.

The `cc` handler is the Claude Code CLI-compatible Anthropic Messages profile.
Its currently declared ingress difference is the local `count_tokens` estimate
shim. Keep future Claude Code CLI-specific ingress validation/defaults in this
profile rather than forking the shared Messages lifecycle.

These modules are not standalone `http.handlers.*` modules. They are loaded by `agent_route_dispatcher`.

### Admin API

- Module ID: `http.handlers.agent_gateway_admin`
- Package: `caddy/admin/`

Exposes the Admin API families listed under "Admin API Notes" below;
authentication is delegated to the HTTP mount layer such as Caddy
`basic_auth`, mTLS, a reverse proxy authenticator, or standalone
`--admin-basic-auth-hash`.

## Key Packages

Deep per-subsystem rules live in nested `AGENTS.md` files next to the code.
Read the applicable subsystem file before changing either its runtime package
or a cross-cutting adapter for it (for example `caddy/admin`,
`caddy/dispatcher`, `standalone/server`, `pkg/gatewaybundle`, or `pkg/distribution/ctlcmd`):

- `pkg/gateway/` — runtime route/VirtualKey/provider resolution (`AgentGateway`) and the public route-model packages (`llmroute`, `modelcatalog`, `mcproute`, `agentroute`, `virtualkey`) → `pkg/gateway/AGENTS.md`
- `pkg/acp/` — native ACP runtime (`codex`, `opencode`): pooling, sessions, permissions, transcripts → `pkg/acp/AGENTS.md`
- `pkg/credential/` — cross-cutting credential model, persistence, scheduling, expiry detection, and external refresh transport → `pkg/credential/AGENTS.md`
- `pkg/llm/` — provider interface/registry, built-in providers, and the `einomodel` eino bridge → `pkg/llm/AGENTS.md`
- `pkg/connector/` — Connector management and shared admission, `httpapi/` and `mcp/` execution engines, `runtime/` common tool contracts and builtin bridge → `pkg/connector/AGENTS.md`
- `pkg/mcp/` — MCP protocol primitives, `transport/` wire transports and `tracking/` ingress request tracking → `pkg/mcp/AGENTS.md`
- `pkg/a2a/` — A2A Protocol 1.0 JSON-RPC (Path B translating client/backend and Path A governed native proxy are implemented) → `pkg/a2a/AGENTS.md`
- `pkg/configstore/` — generic config store/backends (persisted backend: `sqlite`; stores `providers`, `credentials`, `routes`, `connectors`, `agents`, `virtual_keys`, `provider_models`, `logical_models`, `extensions`) → `pkg/configstore/AGENTS.md`
- `pkg/agent/` — agent control plane (`pkg/agent/runtime` contracts, `Agent` model, route/service → agent index) → `pkg/agent/AGENTS.md`; builtin eino ADK host → `pkg/agent/builtin/AGENTS.md`
- `internal/observability/` — usage events, event pipeline, OTLP export, `einotap` → `internal/observability/AGENTS.md`
- `pkg/dispatcher/llmapi/` — LLM protocol handler runtimes; see "Protocol handler modules" above
- `pkg/gateway/virtualkey/limiter/` — protected backend SPI (v0.7.0), generation
  fences, strict selection and reusable suites → its nested `AGENTS.md`
- `pkg/extension/` — linked registry, bounded envelopes and prepared generations → `pkg/extension/AGENTS.md`
- `pkg/attribution/` and `pkg/event/` — trusted bounded context and sanitized public sink transport → their nested `AGENTS.md` files
- `pkg/admin/` — thin Admin transport adapters and typed metrics query handling → `pkg/admin/AGENTS.md`
- `caddy/gateway/` — Caddy app wiring and global `agent_gateway` Caddyfile parsing; see "Caddy app" above

Cross-cutting invariants:

- LLM routes are shared Agent dependencies; never enforce exclusive Agent ownership.
  Agent usage follows trusted execution identity, never LLM route references.
  Direct LLM traffic without that identity remains route-level usage.
  Admin LLM and MCP route deletion must reject remaining Agent references, holding the
  Agent mutation lock through deletion. Agent writes validate resource route
  existence and kind under that lock; write-time route fallback is MCP-only.

- composed managers share the control-plane coordinator for reference checks
  and writes, including direct callers and Bundle. Lock order is Agent writeMu,
  coordinator, Route writeMu, then local store/cache locks. Provider deletion
  blocks LLM routes and Provider credentials and atomically removes its
  ProviderModels; no sequential fallback or secret cascade is permitted.

- builtin tool dependencies, including custom factories, use Connector Resolver/Lease; observation must preserve full outcomes and execution identity. Internal MCP catalogs require bounded complete pagination; ingress keeps single-page semantics.
- dependency direction: `pkg/agent` composes the LLM/MCP/ACP/metrics surfaces; the lower protocol packages (`pkg/llm`, `pkg/mcp`, `pkg/acp`, `pkg/a2a`) must not depend on `pkg/agent`
- no config-store reads in per-request hot paths (provider resolution, route matching); manager snapshots are refreshed on mutation
- route publication rejects equal-score intersecting matches across all kinds
  and static/dynamic sources, including disabled routes; native A2A ignores
  methods for effective matching but retains declared-method specificity.
  Refresh retains the last validated generation on failure. Existing conflicts
  require explicit offline repair, never ID sorting or static precedence.
- factory registration (providers and builtin custom agents) requires blank imports in the binaries that link them; see each subtree file for the exact rule
- gateway request pipelines never add an `agent` step, durable execution/event store,
  scheduler, human-task model, or Temporal dependency to lower runtime packages

The common persisted route envelope and manager are `routecore.RouteConfig`
and `RouteConfigManager`; `agentroute.AgentRouteConfig` is specific to Agent
ingress. This Go naming does not change route kinds, IDs or URLs.

## Runtime Request Flow

```text
HTTP request
  -> http.handlers.agent_route_dispatcher
  -> AgentGateway.ResolveRoute(...)
  -> pick route.protocol
  -> rewrite path using route.match.path_prefix
  -> AgentGateway.ResolveVirtualKey(...)
  -> protocol handler PrepareLLMApiRequest(...)
  -> if execution is local: serve without provider/candidate/credential resolution
  -> otherwise: filter candidates by generic capabilities and AST-derived atomic protocol features
  -> if route uses model targets: resolve the requested route model name to one concrete binding and rewrite request model
  -> concrete targets resolve their registered ProviderModel binding
  -> select a credential and invoke the external refresher when close to expiry
  -> resolve provider instance
  -> provider.Chat(...) or provider.StreamChat(...)
  -> protocol handler writes JSON or SSE response
```

Key detail: provider resolution happens after protocol parsing only for
`execution=provider`; the request `model` means route target name in model-target
mode; no request may bypass explicit model exposure.

## Caddyfile Shape

The main config lives in the global `agent_gateway` block.
`examples/Caddyfile.example` is the working reference config.

Important current directives:

- internal extensions are independent of Caddyfile and use ConfigStore as their sole configuration source; manage them through Admin API or Bundle apply. Caddyfile/Caddy JSON and standalone static Bundles reject extension configuration
- `online.agentguide.community.limiter` schema 1 selects a linked backend using
  `backend_module_id`, `backend_type`, `timeout_ms` (1–1000, default 100) and
  `failure_mode` (default `closed`). Defaults use memory; alternate providers must
  be configured dependencies. Exhaustion is 429, failure is bounded 503, explicit
  open mode is degraded; cancellation and retired-policy errors never fail open,
  and consumes are not retried
- limiter generations preserve unchanged dimensions and unrelated refresh state;
  key policy/backend changes retire state. Quota storage survives connection-handle
  refresh; request admission always uses the current prepared handle
- `provider_types` is startup-only provider type availability; when omitted all registered provider types are enabled
- providers use `provider_type <name>`
- LLM `type: models` routes expose named `provider-model` references (Provider ID + upstream model) or `logical-model` references (`logical_model_id`). ProviderModels and LogicalModels are independent catalog resources; scheduling and fallback belong to LogicalModel. Model registration never implicitly expands route exposure. Caddyfile/Caddy JSON and standalone static Bundles support model declarations and references. Static models are read-only through Admin; static/dynamic identity conflicts fail loading. Old inline logical definitions require offline `extract-models` repair.
- LLM routes use `protocol <openai|anthropic|cc>`, MCP routes use `protocol mcp`, and Agent ingress routes use `protocol agent` or `protocol a2a` (HTTP Agent native Path A; see `docs/design/http-agent-runtime.md`)
- `agent_route_dispatcher` uses `llm_api <name>`, `mcp`, and `agent`
- dispatcher `max_llm_request_body_bytes` (Caddyfile/Caddy JSON) and standalone
  `--max-llm-request-body-bytes` bound the received LLM JSON body. Omitted/zero
  uses 16 MiB; negative values fail validation. Keep the host settings aligned,
  enforce unknown lengths while reading, and return 413 before upstream work.
  This transport setting includes `count_tokens` and is independent of other
  protocol/Admin limits, Provider network settings and VirtualKey admission.
- auth uses `virtualkey`, not `local_api_key`
- Provider-only targets (`direct-provider`, `provider_target`, outer `provider_id`) are rejected. Offline `convert-target` requires an explicit `data.models` allowlist and transactionally creates ProviderModels plus route references; no model is inferred from Provider defaults. Static model input reaches both hosts via `StaticProviderModels` and `StaticLogicalModels`.
- Agent `policy.budget` must be absent or null: non-null budgets are unsupported
  and rejected on writes and stored loading/Refresh. Agent depth remains a
  builtin-host check, independent of the process-level dispatcher depth limit.

Expanded LLM routes use `TargetPolicy *llmroute.RouteModelPolicy`; the shared
`routecore.RouteConfig` keeps raw target JSON for all route kinds.
LLM route JSON writes reject unknown fields, including nested match/auth fields; use `match_policy`. An empty match is a catch-all.

## Admin API Notes

Implemented families:

- LLM: `/admin/llm/providers/...`, `/admin/llm/provider_types` (read-only listing), `/admin/llm/api_handler_types`, `/admin/llm/routes/...`, `/admin/llm/models/providers/{provider_id}/discovered`, `/admin/llm/models/providers/{provider_id}/refresh`, `/admin/llm/models/provider/...`, `/admin/llm/models/logical/...`
- `/admin/virtual_keys/...`, `/admin/credentials/...`
- Contributed Admin subroutes are declared by `Factory.AdminRoutes` and handled
  by prepared `AdminContributor` resources only under `/admin/extensions/<module_id>/...`;
  module roots and all other Community families are reserved. All Admin requests
  share a full-body 4 MiB boundary (413), reject compression (415), and inherit
  mount authentication, CORS, logging and generic JSON panic recovery. Module
  event publishers capture trusted request identity; Publish contexts cannot
  replace the event actor or correlation.
- `GET /admin/manifest` and `GET /admin/extensions`, `GET|PUT|DELETE /admin/extensions/{module_id}` (reference-preserving module configuration)
- MCP: `/admin/connectors/...`, `/admin/mcp/routes/...`, `/admin/mcp/runtime/...` (discovery, execution, dispatcher runtime inspection)
- ACP: `/admin/acp/runtime/...` (native diagnostics keyed by `agent_id`)
- builtin: `/admin/builtin/runtime/...`
- agents: `/admin/agents/...` plus unified route CRUD under `/admin/agents/routes`;
  per-Agent usage includes builtin turn/resume counts and latency alongside
  attributed LLM tokens, MCP calls and ACP operations
- Agent and Provider mutation inspection/recovery: `GET /admin/agents/{id}/mutation`,
  `POST /admin/agents/{id}/recover`, `GET /admin/llm/providers/{id}/mutation`, and
  `POST /admin/llm/providers/{id}/recover`. Unconfirmed outcomes block new
  execution and conflicting writes until verified; recovery never replays writes.
- metrics: summaries (with combined drop/failure, rejected-event and public sink health counters), per-protocol LLM/MCP/ACP timeseries and breakdowns, unified interaction summary/timeseries across all typed event tables, recent interaction events, and the Prometheus exposition endpoint `GET /admin/metrics/prometheus`

Stubbed families currently return `501 Not Implemented`:

- `/admin/memory/...` (reserved; memory is not implemented)

## Files To Check Before Large Changes

- `README.md`: user-facing setup and API examples
- `docs/architecture/architecture-overview.md`: current broader architecture
- `docs/design/enterprise-extension-contract.md`: protected external SPI,
  distribution assembly, and cross-repository compatibility rules
- `docs/guides/custom-distribution.md`: external assembly and validation workflow
- `docs/releases/v0.7.0.md`: exact designated surfaces and release gates
- `docs/releases/v0.7.0.md`: designated protected symbols and published
  dependency acceptance for the extension foundation
- `examples/Caddyfile.example`: working reference config
- `cmd/agw/main.go`: the definitive list of linked modules

If you change module IDs, route semantics, provider registration, or Admin API paths, update this file, the affected nested `AGENTS.md`, and `README.md` in the same change.

## HTTP API connectors

Connector domain rules live in `pkg/connector/AGENTS.md`. Connector `id` is
immutable and case-sensitive; `name` is mutable, non-unique display text. There
is no separate connector `slug`; updates reject differing body/path IDs.
Both hosts accept
`http_api_policy_file` (standalone `--http-api-policy-file`) and optional OTLP
`http_client_spans` (standalone `--metrics-otlp-http-client-spans`). HTTP API
credentials use exact `connector:<id>` scope with empty Provider fields. Static
connector definitions are rejected. Legacy `mcp_services` tables require explicit
stopped-service conversion before either host or a store-opening CLI can start.
Connector references from Agents and MCP routes prevent deletion, including
disabled references. Agent writes require the connector to exist. Each resource
updates independently; Bundle apply is sequential with partial success. Do not
add cross-resource atomic publication, group endpoints or group recovery.
Release references before deletion; keep single-resource mutation recovery.

Connector execution edits/deletion reject references or active/queued calls,
discovery and initialization. Name/description changes preserve sessions and
execution generations. Admission and mutation exclusion share the snapshot lock;
reference checks share application coordination, including Refresh. Preserve
bounded CloseContext cancellation/waiting and late-publication fencing.
Configuration snapshots use Go GC; no reference-counted
lease retirement, multiple active session generations or additional credential
readiness notification/index subsystem is required. Keep live revocation checks.

LLM data-plane model listing returns only route-visible target names from the published policy; it never invokes upstream discovery and requires no default model.

LLM routes must use one model target kind. ProviderModel targets share one Provider
and use real upstream model names; omitted target names derive from upstream_model,
and differing aliases are rejected. LogicalModel routes own the exposed logical
references; cross-provider selection and aliases belong to LogicalModels. Never
mix target kinds. Caddyfile concrete syntax is `target model <provider-id> <upstream-model>`.
