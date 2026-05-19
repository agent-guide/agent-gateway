# agent-gateway Design

## 1. Scope

This document describes the current architecture of `agent-gateway` as it exists in the repository today, plus the intended extension points that are already visible in the codebase.

It is not a pure future-state blueprint anymore. Where the implementation is partial, this document calls that out explicitly.

## 2. Design Goals

The project is built around four practical goals:

- Reuse Caddy's module system and config model where they fit, while keeping the core runtime reusable by the standalone daemon
- Expose familiar LLM-compatible HTTP APIs to agent clients
- Centralize provider configuration, upstream credentials, and gateway-side API keys
- Leave room for richer agent runtime features such as MCP, memory, and orchestration without forcing them into every caller

The current Go module path is `github.com/agent-guide/agent-gateway`.

Related extension design notes live in `docs/` when a topic needs more detail than this architecture overview. The ConfigStore architecture and technical specification is documented in [docs/configstore-design.md](configstore-design.md). The gateway bundle YAML proposal is documented in [docs/gateway-bundle-yaml-design.md](gateway-bundle-yaml-design.md).

## 3. Top-Level Architecture

The runtime is split into three active layers and several partially integrated subsystems:

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
  - agent_route_dispatcher with MCP enabled
  |
  v
Shared gateway runtime
  - provider loading and resolution
  - authenticator loading
  - config store loading
  - llmroute and mcproute registries
  - virtual key lookup
  - credential and auth managers
  - MCP runtime registry
  |
  v
External systems
  - OpenAI / Anthropic / Gemini / Ollama / OpenRouter
  - upstream MCP services
  - SQLite config database
  - future memory backends
```

## 4. Main Components

### 4.1 `caddy/gateway/`, `standalone/server/`, And `pkg/gateway/`: Runtime Assembly And Backbone

The `caddy/gateway.App` type is the root Caddy app module with module ID `agent_gateway`. The standalone daemon in `standalone/server/` assembles the same core runtime services without depending on a Caddy app lifecycle.

Its responsibilities are:

- Provision the configured config store
- Load provider modules from `llm.providers.*`
- Load authenticator modules from `llm.authenticators.*`
- Initialize the credential manager
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
    mcp
}
```

The HTTP handler is `http.handlers.agent_route_dispatcher`, and it loads LLM protocol handlers from:

- `agent_route_dispatcher.llm_apis.openai`
- `agent_route_dispatcher.llm_apis.anthropic`

MCP handling is enabled with the dispatcher-local `mcp` option instead of a separate HTTP handler module.

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
- virtual key CRUD
- credential list/get/delete
- async CLI login and login status
- MCP service discovery and execution endpoints
- MCP dispatcher runtime inspection endpoints

The same route table still defines memory, agent, and metrics endpoints that are not yet implemented.

This means the admin package is now the active control-plane entrypoint for both LLM and MCP, while memory, agent, and metrics remain future work.

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

Built-in providers:

- `openai`
- `anthropic`
- `gemini`
- `ollama`
- `openrouter`

The provider layer uses shared helpers for HTTP client construction, auth/header injection, and OpenAI-compatible behavior. The design keeps provider implementations narrow while still allowing provider-specific behavior.

### 4.5 `pkg/cliauth/`: Credential Lifecycle

Credential management is split into:

- `manager/`: registration, lookup, persistence, selection, refresh lifecycle
- `authenticator/`: provider-specific CLI login flows
- `credential/`: stored credential model and status types

Built-in authenticators are:

- `codex`
- `claude`
- `gemini`

The admin CLI login API triggers an authenticator asynchronously, then stores the resulting credential through the shared auth manager.

This is distinct from local gateway API keys. Upstream provider credentials and local gateway caller credentials are two separate concerns.

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

SQLite is the only storage backend that is provisioned end-to-end today.

The runtime storage API is schema-bound. `ConfigStoreBackend.Register(name, schema)` validates a schema, prepares storage, creates a schema-bound generic `ConfigStore`, and caches it. `ConfigStoreBackend.Get(name)` returns the cached store. The gateway registers the canonical schemas for providers, credentials, routes, virtual keys, and managed models during startup. Generic store interfaces and schema primitives live under `pkg/configstore/`; built-in business schemas live under `pkg/configstore/schema/`.

The config store is important for one reason beyond persistence: it allows some route and provider updates to take effect dynamically without rewriting the entire Caddy config.

### 4.7 `pkg/mcp/`, `pkg/llm/memory/`, `pkg/llm/agent/`

These packages are present because the gateway is intended to grow beyond plain API proxying.

Current status:

- `pkg/mcp/`
  - protocol types, transport clients, service runtime, and runtime registry are active
  - `pkg/mcp/service` manages `mcp_services`, discovery, execution, and session reuse
  - `pkg/mcp/runtime` tracks in-flight requests and progress for the MCP dispatcher
  - `streamable_http` is the active upstream transport path today
  - `stdio` and `sse` code exist but are not yet equally integrated
- `pkg/llm/memory/`
  - interfaces exist
  - SQLite and Mem0-related code exists
  - not yet fully active in normal request execution
- `pkg/llm/agent/`
  - an early orchestrator loop exists
  - memory retrieval and tool execution are still TODOs

Architecturally, MCP is now an active subsystem; memory and agent are still extension subsystems rather than center-of-gravity runtimes.

## 5. Configuration Model

### 5.1 Static App Configuration

Static configuration lives in the global `agent_gateway` Caddyfile block:

```caddy
{
    agent_gateway {
        provider openai-main {
            provider_type openai
            ...
        }
        config_store sqlite { ... }
        authenticator codex { ... }
        route chat { ... }
    }
}
```

The parser currently supports:

- `provider <provider-id>`
- `config_store <name>`
- `authenticator <name>`
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

## 7. Data Flows

### 7.1 LLM API Request

The standard request path is:

```text
HTTP request
  -> agent_route_dispatcher
  -> match AgentRoute by host/path prefix/method
  -> strip matched path prefix
  -> select route protocol handler
  -> validate virtual key if required
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

### 7.4 CLI Login

CLI login flow:

```text
POST /admin/cliauth/authenticators/{authenticator_name}/enable
  -> request body must include config; use {"config":{}} for factory defaults
  -> create runtime authenticator from registered factory
  -> register authenticator in auth manager
POST /admin/cliauth/authenticators/{authenticator_name}/login
  -> lookup authenticator
  -> allocate login_id
  -> start async login goroutine
  -> authenticator.Login()
  -> auth manager RegisterCredential()
  -> persist credential
  -> poll /admin/cliauth/logins/{login_id}
```

The login flow is async because the provider login step may require browser or human interaction.
Authenticators configured by Caddyfile are read-only and cannot be disabled through the admin API.

## 8. Current Implementation Boundaries

The following are implemented enough to be production-shape code, even if still early:

- Caddy app provisioning
- standalone server assembly
- provider module loading
- authenticator module loading
- SQLite config persistence
- provider CRUD
- route CRUD
- MCP service CRUD
- MCP route CRUD
- virtual key CRUD
- credential inspection and deletion
- CLI login orchestration
- OpenAI-compatible and Anthropic-compatible ingress handlers
- MCP dispatcher, upstream discovery, upstream execution, and runtime inspection

The following are partial or placeholder:

- memory admin APIs
- agent admin APIs
- metrics endpoint
- first-class non-HTTP MCP transports such as stdio in the active request path
- full upstream progress relay back to MCP clients
- full memory retrieval and writeback in request path
- complete agent orchestration loop
- richer static Caddyfile route syntax for all route fields

## 9. Extension Points

The codebase is designed to be extended in a few stable ways:

### 9.1 New Provider

Implement `provider.Provider` in `pkg/llm/provider/<name>`. If the provider should also be available in the `agw` binary, add the corresponding Caddy adapter under `caddy/provider/<name>` and link it from `cmd/agw/main.go`.

This is the most mature extension path in the project today.

### 9.2 New Authenticator

Implement the auth manager's authenticator contract and register its factory. If it should be available in the Caddy-based runtime, ensure the linked registration path remains included in `cmd/agw/main.go`.

This integrates naturally with the existing admin CLI login API.

### 9.3 New Config Store

Add a store creator factory through `pkg/configstore.RegisterConfigStoreFactory(...)`. If the backend should be available in Caddy config, add a Caddy adapter under `agent_gateway.config_store_backends.<name>`.

A backend-specific creator should implement `pkg/configstore.ConfigStoreCreator`. The shared `pkg/configstore.Backend` implements `pkg/configstore.ConfigStoreBackend`: `Register(name, schema)` validates and caches a schema-bound store from the creator, and `Get(name)` returns the cached store.

This path exists architecturally, but SQLite is the only end-to-end store currently exercised by the main runtime.

### 9.4 Future MCP / Memory Runtime Extensions

The MCP, memory, and agent packages are already structured as internal subsystem boundaries. The intended direction is:

- MCP expands from the current Streamable HTTP gateway path into broader transport coverage and richer runtime semantics
- memory becomes retrieval and persistence around model calls
- agent orchestration becomes an execution mode rather than a separate external service

Those boundaries are already visible in code, but they should still be treated as evolving.

## 10. Design Tradeoffs

### 10.1 Why Support Both a Caddy App and a Standalone Gateway Server

Using a Caddy app gives the project:

- a mature module graph
- shared provisioning lifecycle
- established HTTP pipeline integration
- existing config loading and deployment patterns

The standalone daemon avoids coupling everything to Caddy's lifecycle and makes it easier to run the gateway as a conventional service. The downside is that the project must maintain two assembly paths over the same runtime core.

### 10.2 Why Hybrid Static + Dynamic Config

Only static config would make operational updates clumsy. Only dynamic config would weaken the value of reproducible startup composition, especially in the Caddy-based runtime.

The hybrid model keeps:

- static infra wiring in the Caddyfile or standalone bundle
- mutable provider and route records in SQLite

This is slightly more complex, but it matches how the gateway is meant to be operated.

### 10.3 Why Keep the Route Model Ahead of the Caddyfile Grammar

The repository already needs a richer route object for admin APIs and internal policy evaluation. Shipping the richer data model first allows the runtime and storage layers to settle before the public Caddyfile grammar is expanded.

That means some fields are representable in JSON and Go types before they are representable in the Caddyfile.

## 11. Near-Term Evolution

The most coherent next steps for the architecture are:

- extend MCP runtime beyond the current Streamable HTTP and request-scoped cancellation model
- include MCP objects in bundle/export/apply flows
- finish the missing admin handlers for memory, agents, and metrics
- expand enforcement of route policy beyond the currently active subset
- integrate memory into the request path
- complete the agent orchestrator tool-call loop
- expand Caddyfile route syntax to cover more of the existing route data model
- decide how the separate web UI becomes a first-class operator surface

Until then, the project should be understood primarily as a route-based LLM gateway with both Caddy-based and standalone deployment modes, and with a broader agent-runtime architecture still under active construction.
