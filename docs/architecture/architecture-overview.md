# Architecture Overview

## 1. Scope

This document describes the current architecture of `agent-gateway` as it
exists in the repository today. Proposed capabilities are documented
separately under `docs/design/`.

## 2. Design Goals

The project is built around four practical goals:

- Reuse Caddy's module system and config model where they fit, while keeping the core runtime reusable by the standalone daemon
- Expose familiar LLM-compatible HTTP APIs to agent clients
- Centralize provider configuration, upstream credentials, and gateway-side API keys
- Keep LLM, MCP, and Agent protocol concerns separated behind shared routing,
  credential, and observability foundations

The current Go module path is `github.com/agent-guide/agent-gateway`.

Related extension design notes live in `docs/` when a topic needs more detail
than this architecture overview. The ConfigStore architecture and technical
specification is documented in
[configstore-architecture.md](configstore-architecture.md). The gateway bundle
YAML contract is documented in
[../design/gateway-bundle-yaml.md](../design/gateway-bundle-yaml.md). Protected
external SPI, reusable distribution entry points, and their designation gates
are defined by the
[Enterprise Extension Contract](../design/enterprise-extension-contract.md);
no current package is protected merely because it is exported.

## 3. Top-Level Architecture

The request path runs through four layers; memory remains a reserved subsystem:

```text
Client
  |
  v
HTTP handlers
  - Caddy adapters: http.handlers.agent_route_dispatcher, http.handlers.agent_gateway_admin
  - Standalone server: net/http handlers assembled by standalone/server
Dispatcher / protocol modules
  - agent_route_dispatcher.llm_apis.openai
  - agent_route_dispatcher.llm_apis.anthropic
  - agent_route_dispatcher.llm_apis.cc
  - agent_route_dispatcher with MCP enabled
  - agent_route_dispatcher with unified Agent ingress enabled
  |
  v
Shared gateway runtime
  - provider loading and resolution
  - credential loading and refresh-strategy registration
  - config store loading
  - llmroute, mcproute, and agentroute registries
  - virtual key lookup
  - credential manager
  - MCP runtime registry
  - Agent-owned ACP runtime/process manager
  - agent manager (Agent CRUD + immutable definition snapshot)
  - builtin ADK host (in-process materialization of builtin-runtime agents)
  - usage event pipeline (typed llm/mcp/acp/builtin/a2a events, spans, optional OTLP export)
  |
  v
External systems
  - upstream LLM providers (OpenAI / Anthropic / Gemini / DeepSeek / Qwen / Zhipu / OpenRouter / Ollama / Codex / Claude Code)
  - upstream MCP services
  - local ACP agent or adapter processes (codex, opencode)
  - remote HTTP agents registered by Agent Card URL and reached through common
    `/turn` translation or governed native A2A Protocol 1.0 JSON-RPC
  - SQLite config database and usage event tables
  - optional OpenTelemetry collector (metrics.otlp span export)
```

Builtin-runtime agents live inside the shared runtime layer (the builtin ADK
host), not in the external systems row: their inner model and tool calls
resolve through the same route and service registries as any other traffic, so
no external agent process is involved. See
[Builtin Agent Runtime](../design/builtin-agent-runtime.md) for the
authoritative runtime design.

## 4. Main Components

### 4.1 `caddy/gateway/`, `standalone/server/`, And `pkg/gateway/`: Runtime Assembly And Backbone

The `caddy/gateway.App` type is the root Caddy app module with module ID `agent_gateway`. The standalone daemon in `standalone/server/` assembles the same core runtime services without depending on a Caddy app lifecycle.

The thin `cmd/agw`, `cmd/agwd`, and `cmd/agwctl` mains retain edition-specific
imports and delegate to public entries under `pkg/distribution/{caddycmd,daemoncmd,ctlcmd}`.
Both server mains supply `distribution.CommunityRuntime()` metadata, while CLI
supplies its validation projection. These snapshots describe explicit linking;
validated registries and runtime manifest queries are still planned.
Daemon and CLI constructors create fresh Cobra trees without loading dotenv.
Their process runners load `.env.local` before `.env` with shell values taking
precedence. Daemon signal handling is installed only when serving. Caddy delegates
its command construction, signal handling and process exit to upstream Caddy.
CLI command implementations and instance state live in `pkg/distribution/ctlcmd`.
Both server assemblies accept public `distribution.MetricsConfig` values and
adapt them to internal usage configuration during bootstrap.


Its responsibilities are:

- Provision the configured config store
- Load static provider configs and instantiate runtime providers through the provider registry
- Initialize the credential manager
- Configure the external request-time credential refresh command
- Restore persisted credentials from storage
- Build route loading and provider resolution dependencies
- Construct the shared `pkg/gateway.AgentGateway` runtime used by HTTP handlers

The app owns both:

- statically configured routes from the Caddyfile
- dynamically persisted route and provider records from the config store

Static route limitation:

- Caddyfile routes only support direct-provider targets
- logical-model routes are configured through the Admin API and config-store-backed workflows

This is the key design choice in the project: transport adapters are intentionally thin, runtime assembly is allowed to differ between `agw` and `agwd`, and `pkg/gateway` owns the reusable gateway services.

### 4.2 `caddy/dispatcher/` And `pkg/dispatcher/`: Compatible LLM Ingress

The `caddy/dispatcher/` package registers the `agent_route_dispatcher` Caddyfile directive. It adapts the reusable `pkg/dispatcher` runtime and accepts dispatcher-local LLM API protocol modules:

```caddy
agent_route_dispatcher {
    llm_api openai
    llm_api anthropic
    llm_api cc
    mcp
    agent
}
```

The HTTP handler is `http.handlers.agent_route_dispatcher`, and it loads LLM protocol handlers from:

- `agent_route_dispatcher.llm_apis.openai`
- `agent_route_dispatcher.llm_apis.anthropic`
- `agent_route_dispatcher.llm_apis.cc`

The `anthropic` and `cc` handlers are thin sibling ingress profiles over the
shared `pkg/dispatcher/llmapi/anthropicmsg` Messages core. The core owns parsing,
conversion, response lifecycle, and SSE content-block ordering. The `cc`
profile's declared ingress difference is its local `count_tokens` estimate
shim; provider authentication and Claude Code fingerprint shaping remain in the
provider layer.

MCP handling is enabled with the dispatcher-local `mcp` option instead of a
separate HTTP handler module. Unified Agent handling is enabled with `agent`;
it dispatches `protocol=agent` turns to the target Agent runtime backend and
handles `protocol=a2a` routes through the native HTTP Agent proxy path.

The runtime dispatcher in `pkg/dispatcher` does not define route policy inline. Instead, it asks the shared gateway route manager to match the HTTP request against `AgentRoute.match`, strips the matched route path prefix, selects the route's `protocol`, and resolves the matched route and target provider.

This separation is deliberate:

- API compatibility stays transport-focused
- route policy stays centralized
- provider selection can evolve independently from HTTP parsing

### 4.3 `caddy/admin/` And `pkg/admin/`: Operational Control Surface

The `caddy/admin/` package registers `agent_gateway_admin` with module ID `http.handlers.agent_gateway_admin`, and delegates request handling to the reusable `pkg/admin` runtime package.

Today it exposes working endpoints for:

- health
- provider CRUD
- LLM route CRUD
- MCP service CRUD
- MCP route CRUD
- unified Agent route CRUD
- virtual key CRUD
- credential list/get/delete
- MCP service discovery and execution endpoints
- MCP dispatcher runtime inspection endpoints
- ACP runtime inspection and operator escape-hatch endpoints
- metrics summary, event, timeseries, breakdown, and Prometheus exposition endpoints

The `/admin/agents` family provides CRUD, unified AgentRoute CRUD,
workspace/activity/usage/interactions/resources/health, runtime capabilities,
exact-run list/cancel, one-shot permission list/decision, and capability-gated
session/transcript reads. ACP, builtin, and HTTP runtimes enter through
`kind=agent`, `protocol=agent` routes; HTTP Agents may additionally use
`protocol=a2a`.

This makes the admin package the control-plane entrypoint for LLM, MCP, ACP,
agents, and metrics inspection. The reserved memory family returns `501 Not
Implemented`. Consumer runtime APIs scoped by route and VirtualKey, such as
turns, permission decisions, session listing, and transcript replay, stay
under the dispatcher route prefix rather than under `/admin/acp`.

### 4.4 `pkg/llm/provider/`: Provider Abstraction

Providers implement a shared interface:

```go
type Provider interface {
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    StreamChat(ctx context.Context, req *ChatRequest) (*schema.StreamReader[*schema.Message], error)
    ListModels(ctx context.Context) ([]ModelInfo, error)
    Capabilities() ProviderCapabilities
    Config() ProviderConfig
}
```

Important characteristics:

- the interface is intentionally small
- chat and stream are first-class
- model listing is supported
- embeddings are optional through `EmbeddingProvider`
- providers expose capability metadata and runtime config
- logical-model resolution filters managed-model capabilities for generic
  request needs such as client tools, and performs generic set inclusion over
  AST-derived protocol requirements and provider-type atomic features before
  credential selection
- protocol-native request/history/response state crosses provider and eino
  boundaries through scoped `ProtocolState` envelopes. Registered dialect
  codecs fold transport-only response bodies and SSE events into replayable
  history, while the served candidate's atomic features select validated native
  response relay without mutating the caller's logical model

Built-in providers:

- `openai`
- `anthropic`
- `claudecode`
- `codex`
- `gemini`
- `ollama`
- `openrouter`
- `deepseek`
- `zhipu`
- `qwen`
- `grok`

The provider layer uses shared helpers for HTTP client construction, auth/header injection, and OpenAI-compatible behavior. The design keeps provider implementations narrow while still allowing provider-specific behavior.

### 4.5 Credential Lifecycle

`pkg/credential` owns registration, persistence, selection state, expiry
detection, and the generic external refresh-command transport. Agent Gateway
contains no provider-specific token refresh implementation.

Interactive OAuth login is deliberately outside the gateway binary. The
separate `agw-auth` tool performs browser, PKCE, callback, or device flows and
registers the resulting `oauth_token` through `/admin/credentials`. Before
an upstream request, the gateway invokes the configured external refresh argv
when a selected credential is close to expiry and persists the returned token.

Upstream provider credentials and gateway caller VirtualKeys remain separate concerns.

### 4.6 `pkg/configstore/` And `caddy/configstore/`: Persistent Control Data

The default config store backend is `agent_gateway.config_store_backends.sqlite`.

It persists:

- provider configs
- route definitions
- virtual keys
- upstream provider credentials
- managed model overlays
- MCP service definitions
- MCP route definitions
- Agent definitions (including inline ACP runtime config)
- unified Agent route definitions

SQLite is the only storage backend that is provisioned end-to-end today.

The runtime storage API is schema-bound. `ConfigStoreBackend.Register(name, schema)` validates a schema, prepares storage, creates a schema-bound generic `ConfigStore`, and caches it. `ConfigStoreBackend.Get(name)` returns the cached store. The gateway registers the canonical schemas for providers, credentials, routes, virtual keys, managed models, MCP services, and Agents during startup. Generic store interfaces and schema primitives live under `pkg/configstore/`; built-in business schemas live under `pkg/configstore/schema/`.

The config store is important for one reason beyond persistence: it allows some route and provider updates to take effect dynamically without rewriting the entire Caddy config.

### 4.7 `pkg/mcp/`, `pkg/acp/`, `pkg/a2a/`, `pkg/agent/`, `pkg/llm/memory/`

Current status:

- `pkg/mcp/`
  - protocol types, transport clients, service runtime, and runtime registry are active
  - `pkg/mcp/service` manages `mcp_services`, discovery, execution, and session reuse
  - `pkg/mcp/runtime` tracks in-flight requests and progress for the MCP dispatcher
  - `streamable_http`, `stdio`, and legacy `sse` are integrated upstream transports
- `pkg/acp/`
  - Agent-owned runtime config, turn request/event types, agent SPI, stdio JSON-RPC transport, activity tracking, and Admin/dispatcher integration are active
  - first-version service config allows only `codex` and `opencode`
  - `opencode` uses the fixed `opencode acp --cwd <cwd>` stdio process shape
  - `codex` uses the fixed external ACP adapter binary `codex-acp` by default; it does not launch `codex acp`
  - the runtime driver handles `initialize`, `session/new`, `session/load`, `session/prompt`, full `session/update` parsing (`pkg/acp/host/acpupdate`: text, reasoning, tool calls, plan, usage, available commands, session info, mode, config options), route-scoped and Admin `session/list` and transcript replay (`session/load` over a transient connection) after ACP capability checking, model selection and `config_overrides` via `session/set_config_option`, and spec-correct fail-closed permission replies with an off-loop timeout
  - each pooled instance caches the latest session metadata (config options, slash commands, title, mode, usage) from a lifetime updates subscription; the cache is replayed as snapshot events at every turn start and exposed through the runtime Admin inspection
  - runtime hardening: `PATH` preflight, stderr capture, a setup-handshake timeout, an idle janitor, dead-instance eviction, `fresh_session`, scope rebind (a session-addressed turn adopts the thread's live instance instead of spawning a second process), and `CloseScope`/`CloseThread` teardown
  - permission modes `deny`/`auto_approve`/`interactive`: interactive requests follow the runtime capability's advertised continuation mode and common Agent permission controls
  - verified end to end against the real `opencode acp` and `codex-acp`
    binaries; automatic crash retry and an in-repository Codex app-server
    bridge are not supported
- `pkg/agent/`
  - the external agent control plane: the `Agent` model, `agents` store, unified `AgentRoute.agent_id` ingress, and runtime-neutral capability APIs
  - `pkg/agent/builtin/` is the in-process eino ADK host for `runtime.type = "builtin"` agents and executes behind the same AgentRoute contract as ACP
  - composes the protocol subsystems and observes them; the protocol packages do not depend on it
- `pkg/a2a/`
  - A2A Protocol 1.0 Card, JSON-RPC, client, and governed proxy primitives
  - used by the HTTP Agent translating backend and native A2A ingress; see
    [http-agent-architecture.md](http-agent-architecture.md)
- `pkg/llm/memory/`
  - interfaces exist
  - SQLite and Mem0-related code exists
  - not active in normal request execution

Architecturally, MCP and ACP are active native runtime subsystems, and `pkg/agent` is the active external control plane that composes them (it does not own an agent's internal reasoning loop). Memory is still an extension subsystem.

## 5. Configuration Model

### 5.1 Static App Configuration

Static configuration lives in the global `agent_gateway` Caddyfile block:

```caddy
{
    agent_gateway {
        provider_types {
            openai
        }

        provider openai-main {
            provider_type openai
            ...
        }
        config_store sqlite { ... }
        route chat { ... }
    }
}
```

The parser currently supports:

- `provider <provider-id>`
- `config_store <name>`
- `route <id>`

Static route parsing is intentionally small right now. Supported route subdirectives are:

- `require_virtual_key`
- `target provider <provider-id> [weight]`

The Go route model is richer than the current static config grammar. That mismatch is intentional: the runtime and Admin API support logical-model routes, while Caddyfile and standalone startup config only accept direct-provider routes to keep static bootstrap simpler.

### 5.2 Dynamic Persisted Configuration

The config store also holds:

- provider records keyed by ID and tag
- LLM route objects keyed by ID
- MCP route objects keyed by ID
- MCP service objects keyed by ID
- virtual key objects keyed by key string

When an API handler receives a request for a given `route_id`, the runtime can reload the latest stored route definition for that ID. Provider references can also resolve through persisted provider config.

This produces a hybrid model:

- Caddy owns the long-lived process and module graph
- the config store owns mutable operational records

That is one of the core architectural decisions in the project.

## 6. Request Routing Design

### 6.1 Route Object

The primary routing configuration is `pkg/gateway/llmroute.AgentRoute`.

Important fields include:

- `ID`
- `Match`
- `Protocol`
- `TargetPolicy`
- `Policy`
- timestamps and disabled state

The richer route model already supports ideas such as:

- logical-model and direct-provider routing
- route-level auth
- allowed model restrictions
- timeout, retry, fallback, quota, and rate-limit policy
- caller-specific policy overrides through `VirtualKey`

Only part of this model is enforced today, but the shape of the runtime data model is already defined.

Current runtime resolution treats `TargetPolicy.ProviderTarget.ProviderID` as the direct-provider switch. If that field is set, the route resolves in direct-provider mode; otherwise it resolves through `TargetPolicy.ModelTargets`.

### 6.2 Selection and Resolution

At startup, the runtime assembly layer builds:

- a route loader
- a provider resolver
- a virtual key store binding

Provider resolution currently combines:

- statically provisioned provider instances from the active runtime assembly
- dynamically decoded provider configs from the config store

This allows the request path to resolve a named target provider without hard-coding the source of truth to either the Caddyfile or the database alone.

Provider config `options.compact` is the current compatibility-mode selector. Supported values are `cc`, `codex`, and `none`; providers ignore compact modes they do not support.

## 7. Data Flows

### 7.1 LLM API Request

The standard request path is:

```text
HTTP request
  -> agent_route_dispatcher
  -> match AgentRoute by host/path prefix/method
  -> validate virtual key if required
  -> apply the VirtualKey's in-memory route-kind rate limit if configured
  -> strip matched path prefix
  -> select route protocol handler
  -> resolve target provider
  -> convert request into provider.Chat/StreamChat input
  -> call upstream provider
  -> translate provider response back to dialect response
```

The important design property here is that compatible ingress is separated from route policy and from provider implementation.

### 7.2 MCP Request

The MCP request path is now:

```text
HTTP request
  -> agent_route_dispatcher with mcp enabled
  -> match MCPRoute by host/path prefix/method
  -> validate virtual key if required
  -> apply the VirtualKey's in-memory MCP rate limit if configured
  -> decode JSON-RPC request
  -> register in-flight request in pkg/mcp/runtime
  -> resolve target MCP service
  -> initialize or reuse upstream Streamable HTTP session
  -> invoke discovery or execution method on upstream MCP service
  -> map notifications/cancelled and notifications/progress into runtime state
  -> translate upstream result into JSON-RPC response
```

### 7.3 Admin Mutation

For a route or provider change:

```text
HTTP admin request
  -> agent_gateway_admin
  -> config store CRUD
  -> later request path reloads latest stored record
```

This is why the project can support operational changes without treating the Caddyfile as the only mutable state.

### 7.4 External CLI Login

```text
agw-auth login
  -> resolve the target provider through the Gateway Admin API
  -> run the interactive provider OAuth flow locally
  -> POST the resulting oauth_token to /admin/credentials
  -> Gateway selects and refreshes that credential on model requests
```

The Gateway has no authenticator configuration or login-session endpoints.

## 8. Current Implementation Boundaries

The current implementation includes:

- Caddy app provisioning
- standalone server assembly
- provider module loading
- request-time OAuth credential refresh
- SQLite config persistence
- provider CRUD
- route CRUD
- MCP service CRUD
- MCP route CRUD
- virtual key CRUD
- credential inspection and deletion
- OpenAI-compatible and Anthropic-compatible ingress handlers
- MCP dispatcher, upstream discovery, upstream execution, and runtime inspection
- unified Agent control-plane CRUD and AgentRoute dispatch across ACP, builtin,
  and HTTP runtimes
- translated HTTP Agent `/turn` execution and governed native A2A ingress
- SQLite-backed usage metrics summaries and recent interaction event inspection,
  including unified AgentRoute dimensions and typed ACP/builtin/A2A persistence;
  Agent ingress carries direct `agent_id`, `run_id`, and `runtime_type`
- bounded-label Prometheus counters grouped by `route_kind` and `runtime_type`
- OTLP span export of usage events to an OpenTelemetry collector (opt-in via the `metrics.otlp` config)

The following surfaces are reserved or intentionally unsupported:

- memory admin APIs
- full upstream progress relay back to MCP clients
- full memory retrieval and writeback in request path
- the Gateway Request Pipeline for synchronous, request-bound LLM/MCP/transform
  composition is not implemented. Durable Project/Team/Agent workflows,
  scheduling, human approval, and multi-Agent DAGs belong to an upper-layer
  workbench and an external engine such as Temporal; its Workers call the
  gateway data plane. See
  [Gateway Request Pipeline And External Orchestration](../design/request-pipeline.md).
- richer static Caddyfile route syntax for all route fields

## 9. Extension Points

The codebase is designed to be extended in a few stable ways:

### 9.1 New Provider

Implement `provider.Provider` in `pkg/llm/provider/<name>`. If the provider should also be available in the gateway binaries, link the runtime package from `cmd/agw/main.go` and `cmd/agwd/main.go`.

This is the most mature extension path in the project today.

### 9.2 New OAuth Credential Source

Add both interactive login and provider-specific refresh support to the
external `agw-auth` project. The login result declares `refresh_name` for the
external tool's internal dispatch plus expiry and `refresh_expiry_delta`
metadata for request-time renewal.

### 9.3 New Config Store

Add a store creator factory through `pkg/configstore.RegisterConfigStoreFactory(...)`. If the backend should be available in Caddy config, add a Caddy adapter under `agent_gateway.config_store_backends.<name>`.

A backend-specific creator should implement `pkg/configstore.ConfigStoreCreator`. The shared `pkg/configstore.Backend` implements `pkg/configstore.ConfigStoreBackend`: `Register(name, schema)` validates and caches a schema-bound store from the creator, and `Get(name)` returns the cached store.

This path exists architecturally, but SQLite is the only end-to-end store currently exercised by the main runtime.

### 9.4 Agent Runtime Composition

The `pkg/agent` control plane composes the LLM, MCP, ACP, HTTP, builtin, and
metrics subsystems. It manages agent identities and runtime-specific
configuration, governs declared resources, and observes sessions, usage, and
call chains. It does not own an Agent's internal reasoning loop. See
[Agent Control Plane](../design/agents-control-plane.md).

The execution boundary for ACP and builtin turns is one
turn-first `agentruntime.Backend` layer registered by `AgentGateway`. The
gateway-owned adapters execute through one run sequencer behind a unified
`AgentRoute.agent_id` relationship. There is no unbound ACP ingress or
runtime-specific public route family. HTTP Agents execute through the same
`protocol agent` `/turn` ingress and a Path B backend translating to A2A
Protocol 1.0 JSON-RPC via `pkg/a2a`. The same HTTP Agents can expose native
governed A2A JSON-RPC through `protocol a2a` Path A routes, including a
gateway-owned Agent Card, VirtualKey admission, credential replacement, and
bounded JSON/SSE forwarding. See [HTTP Agent Runtime](../design/http-agent-runtime.md).
Upper-layer Workflow Workers call the same AgentRoute/turn boundary while
their external engine owns durable business state, retry, scheduling,
approval, and DAG semantics. Gateway Request Pipelines deliberately exclude
an `agent` step. See
[HTTP Agent Runtime Architecture](http-agent-architecture.md) and
[Gateway Request Pipeline And External Orchestration](../design/request-pipeline.md).

ACP service is no longer a first-class product/config object.
An ACP Agent owns its execution config under `Agent.runtime.acp`, and
`agent_id` directly owns the ACP process pool, sessions, permissions, runtime
diagnostics, and attribution. Native `/admin/acp/runtime` diagnostics remain
Agent-keyed.

## Linked module configuration

Distribution entries freeze `Assembly.Modules` and separately linked
`Assembly.Factories` into an immutable extension registry. All three binaries
expose `manifest`; the CLI projects validation roles, and both servers expose
`GET /admin/manifest` beneath the Admin mount. Caddy receives process-owned
assembly from its executable entry; standalone passes it explicitly.

`pkg/extension.Manager` combines static envelopes with the schema-bound
`extensions` store on the existing backend. Mutations validate and prepare one
complete dependency generation, persist the target envelope, and publish in
memory. Leases retain old resources until drain or the five-second retirement
budget. Config, manifests and Admin responses preserve secret-reference tokens.
Only preparation receives resolved values. `pkg/admin` owns envelope CRUD and
assembles immutable `Factory.AdminRoutes` under each literal module namespace.
Prepared resources implement `AdminContributor` and receive only cloned config,
logger, attribution access and owner-scoped event publication bound to the
original trusted request identity. The handler holds
a generation lease, linking request cancellation to resource retirement. All
Admin routes share full-body 4 MiB checks (413), reject compression (415), and
use generic JSON panic responses. Outer deployment authentication, CORS and
logging apply equally to contributed routes in Caddy and standalone mode.
Module health uses the same extension manager; shutdown drains/cancels handlers
before resource closure in reverse dependency order.


## Trusted attribution and public events

Both runtime assemblies inject exact `AttributeKeys`, an optional trusted
`AttributionProducer`, and linked-module `EventSinks` through `distribution.Assembly`.
They are code-owned callbacks and values, excluded from manifests/configuration.
A fresh sink factory runs for each server lifecycle. Unknown or duplicate sink
owners fail assembly; failed initialization disposes previously created sinks.

`pkg/attribution` copies bounded subject, tenant and correlation values into and
out of request context. A validated VirtualKey establishes `virtualkey:<id>` as
the default subject and supplies no tenant. The dispatcher stamps selected
route/Agent and normalized trace/span/parent identities; runtime dimension changes
update run and child correlation while retaining trusted identity. Raw client
identity/tenant headers have no effect. Producer errors, panics and invalid optional
values fall back to core correlation rather than exposing their diagnostics.

`pkg/event` contains only v1 `limiter.admission` and `extension.mutation` DTOs.
Extension PUT/DELETE success follows persistence/publication; failure contains
an enumerated reason. Configured limiter admission emits allow/deny/degraded, with bounded unavailable
reasons and actual backend owner IDs. Policy/backend resets emit `state_reset`.
Prompts, arbitrary diagnostics, configs, credentials and secret-reference tokens
have no DTO field. Validation includes the 16 KiB encoded JSON bound.

The existing observability pipeline publishes those DTOs through one bounded
transport worker per external sink; internal SQLite/Prometheus/OTLP usage stays
on the existing accounting path. Slow/failing external sinks are isolated. Queue
full drops the newest event; accepted events are FIFO, without retries or replay.
Flush inserts a barrier; Close rejects new events, drains and flushes/closes once
with a shared five-second maximum budget. That deadline cancels active callbacks;
a stuck callback is unhealthy and cannot hold process teardown past the deadline.
No goroutine is created per production write. Metrics include rejection counts and
sink health in registration order, along with combined drop/failure counters.
Standalone and Caddy report sink shutdown failures through their execution lifecycle.


### VirtualKey admission backends

The dispatcher performs one context-bound admission after VirtualKey validation
and interaction setup, before LLM/MCP/Agent protocol dispatch. The domain-owned
`pkg/gateway/virtualkey/limiter` SPI receives value policies and opaque per-key,
per-dimension generations, never bearer values. Memory is the Community default.
The schema-1 `online.agentguide.community.limiter` envelope selects a linked,
configured `LimiterProvider` dependency; preparation resolves its backend type
and validates health before activation. Both Caddy and standalone use this wiring.

Admission uses a current generation lease and prepared connection handle. Key
metadata writes, unchanged dimensions and unrelated extension refreshes preserve
quota identity; changed policies, key deletion/reset and backend authority/config
changes retire it. StatefulResource reconciles only unpublished candidate identity,
with monotonic Snapshot.Sequence fencing. New authorities start full buckets;
quota storage survives connection-handle replacement. No request reads storage,
discovers a module, resolves its factory or retries uncertain consumption.

The default 100 ms budget accepts 1–1000 ms and is capped by the request deadline.
Exhaustion is 429 with rounded-up Retry-After; failures/timeouts are bounded
503 limiter_unavailable. Explicit open mode allows degraded traffic; caller and
retirement cancellation, retired-policy errors or shutdown cannot bypass a hard limit. Unlimited
classes skip backend calls. Admission counters use only dimension, outcome and
bounded backend type; external event sinks receive sanitized admission/reset DTOs
through the existing pipeline. Gateway shutdown stops/cancels admissions and
shares its five-second deadline with extension resource retirement.
