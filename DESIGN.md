# caddy-agent-gateway Design

## 1. Scope

This document describes the current architecture of `caddy-agent-gateway` as it exists in the repository today, plus the intended extension points that are already visible in the codebase.

It is not a pure future-state blueprint anymore. Where the implementation is partial, this document calls that out explicitly.

## 2. Design Goals

The project is built around four practical goals:

- Reuse Caddy's module system and config model instead of building another standalone gateway runtime
- Expose familiar LLM-compatible HTTP APIs to agent clients
- Centralize provider configuration, upstream credentials, and gateway-side API keys
- Leave room for richer agent runtime features such as MCP, memory, and orchestration without forcing them into every caller

## 3. Top-Level Architecture

The runtime is split into three active layers and several partially integrated subsystems:

```text
Client
  |
  v
HTTP handlers
  - http.handlers.agent_route_dispatcher
  - http.handlers.agent_gateway_admin
Dispatcher LLM API modules
  - agent_route_dispatcher.llm_apis.openai
  - agent_route_dispatcher.llm_apis.anthropic
  |
  v
agent_gateway Caddy app
  - provider loading
  - authenticator loading
  - config store loading
  - route registry / route loader
  - provider resolver
  - virtual key lookup
  - auth manager
  |
  v
External systems
  - OpenAI / Anthropic / Gemini / Ollama / OpenRouter
  - SQLite config database
  - future MCP / memory backends
```

## 4. Main Components

### 4.1 `gateway/`: Caddy App Backbone

The `gateway.App` type is the root Caddy app module with module ID `agent_gateway`.

Its responsibilities are:

- Provision the configured config store
- Load provider modules from `llm.providers.*`
- Load authenticator modules from `llm.authenticators.*`
- Initialize the credential manager
- Restore persisted credentials from storage
- Build route loading and provider resolution dependencies
- Construct the shared `AgentGateway` runtime used by HTTP handlers

The app owns both:

- statically configured routes from the Caddyfile
- dynamically persisted route and provider records from the config store

This is the key design choice in the project: the HTTP handlers are intentionally thin, while the app owns the reusable gateway services.

### 4.2 `api/`: Compatible LLM Ingress

The `api/` package registers the `agent_route_dispatcher` Caddyfile directive. That directive currently accepts dispatcher-local LLM API protocol modules:

```caddy
agent_route_dispatcher {
    llm_api openai
    llm_api anthropic
}
```

The HTTP handler is `http.handlers.agent_route_dispatcher`, and it loads protocol handlers from:

- `agent_route_dispatcher.llm_apis.openai`
- `agent_route_dispatcher.llm_apis.anthropic`

The dispatcher does not define route policy inline. Instead, it asks the shared gateway route manager to match the HTTP request against `AgentRoute.match`, strips the matched route path prefix, selects the route's `llm_api`, and resolves the matched route and target provider.

This separation is deliberate:

- API compatibility stays transport-focused
- route policy stays centralized
- provider selection can evolve independently from HTTP parsing

### 4.3 `admin/`: Operational Control Surface

The `admin/` package registers `agent_gateway_admin` with module ID `http.handlers.agent_gateway_admin`.

Today it exposes working endpoints for:

- health
- provider CRUD
- route CRUD
- virtual key CRUD
- credential list/get/delete
- async CLI login and login status

The same route table also defines MCP, memory, agent, and metrics endpoints, but those handlers currently return `501 not implemented`.

This means the admin package is already the control-plane entrypoint, but only part of the intended control plane is finished.

### 4.4 `llm/provider/`: Provider Abstraction

Providers implement a shared interface:

```go
type Provider interface {
    Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error)
    Stream(ctx context.Context, req *GenerateRequest) (*schema.StreamReader[*schema.Message], error)
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

### 4.5 `llm/cliauth/`: Credential Lifecycle

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

### 4.6 `configstore/`: Persistent Control Data

The default config store is `agent_gateway.config_stores.sqlite`.

It persists:

- provider configs
- route definitions
- virtual keys
- upstream provider credentials

SQLite is the only storage backend that is provisioned end-to-end today.

The config store is important for one reason beyond persistence: it allows some route and provider updates to take effect dynamically without rewriting the entire Caddy config.

### 4.7 `llm/mcp/`, `llm/memory/`, `llm/agent/`

These packages are present because the gateway is intended to grow beyond plain API proxying.

Current status:

- `llm/mcp/`
  - transport code exists for stdio, SSE, and WebSocket
  - manager/client abstractions exist
  - not yet integrated into the admin surface or request path
- `llm/memory/`
  - interfaces exist
  - SQLite and Mem0-related code exists
  - not yet fully active in normal request execution
- `llm/agent/`
  - an early orchestrator loop exists
  - memory retrieval and tool execution are still TODOs

Architecturally, these are extension subsystems, not the current center of gravity of the product.

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
- `allowed_model`
- `target provider <provider-id> [weight]`

The Go route model is richer than the current Caddyfile grammar. That mismatch is intentional for now: the data model has been opened up earlier than the human-facing config syntax.

### 5.2 Dynamic Persisted Configuration

The config store also holds:

- provider records keyed by ID and tag
- route objects keyed by ID
- virtual key objects keyed by key string

When an API handler receives a request for a given `route_id`, the runtime can reload the latest stored route definition for that ID. Provider references can also resolve through persisted provider config.

This produces a hybrid model:

- Caddy owns the long-lived process and module graph
- the config store owns mutable operational records

That is one of the core architectural decisions in the project.

## 6. Request Routing Design

### 6.1 Route Object

The primary routing configuration is `gateway/route.AgentRoute`.

Important fields include:

- `ID`, `Name`
- `Targets`
- `Policy`
- `Match`
- timestamps and disabled state

The richer route model already supports ideas such as:

- weighted and failover targets
- route-level auth
- allowed model restrictions
- timeout, retry, fallback, quota, and rate-limit policy
- caller-specific policy overrides through `VirtualKey`

Only part of this model is enforced today, but the shape of the runtime data model is already defined.

### 6.2 Selection and Resolution

At startup, the gateway app builds:

- a route loader
- a provider resolver
- a virtual key store binding

Provider resolution currently combines:

- statically provisioned provider instances from the Caddy app
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
  -> select route llm_api protocol handler
  -> validate virtual key if required
  -> resolve target provider
  -> convert request into provider.Generate/Stream input
  -> call upstream provider
  -> translate provider response back to dialect response
```

The important design property here is that compatible ingress is separated from route policy and from provider implementation.

### 7.2 Admin Mutation

For a route or provider change:

```text
HTTP admin request
  -> agent_gateway_admin
  -> config store CRUD
  -> later request path reloads latest stored record
```

This is why the project can support operational changes without treating the Caddyfile as the only mutable state.

### 7.3 CLI Login

CLI login flow:

```text
POST /admin/cliauth/authenticators/{authenticator_name}/enable
  -> create runtime authenticator from registered factory
  -> register authenticator in auth manager
POST /admin/cliauth/authenticators/{authenticator_name}/login
  -> lookup authenticator
  -> start async login goroutine
  -> authenticator.Login()
  -> auth manager RegisterCredential()
  -> persist credential
  -> poll /admin/cliauth/authenticators/{authenticator_name}/login/status
```

The login flow is async because the provider login step may require browser or human interaction.
Authenticators configured by Caddyfile are read-only and cannot be disabled through the admin API.

## 8. Current Implementation Boundaries

The following are implemented enough to be production-shape code, even if still early:

- Caddy app provisioning
- provider module loading
- authenticator module loading
- SQLite config persistence
- provider CRUD
- route CRUD
- virtual key CRUD
- credential inspection and deletion
- CLI login orchestration
- OpenAI-compatible and Anthropic-compatible ingress handlers

The following are partial or placeholder:

- MCP admin APIs
- memory admin APIs
- agent admin APIs
- metrics endpoint
- full MCP execution in request path
- full memory retrieval and writeback in request path
- complete agent orchestration loop
- richer static Caddyfile route syntax for all route fields

## 9. Extension Points

The codebase is designed to be extended in a few stable ways:

### 9.1 New Provider

Add a Caddy module under `llm.providers.<name>` that implements `provider.Provider`.

This is the most mature extension path in the project today.

### 9.2 New Authenticator

Add a Caddy module under `llm.authenticators.<name>` that implements the auth manager's authenticator contract.

This integrates naturally with the existing admin CLI login API.

### 9.3 New Config Store

Add a module under `agent_gateway.config_stores.<name>` that implements `configstore/intf.ConfigStorer`.

This path exists architecturally, but SQLite is the only end-to-end store currently exercised by the main runtime.

### 9.4 Future MCP / Memory Runtime Extensions

The MCP, memory, and agent packages are already structured as internal subsystem boundaries. The intended direction is:

- MCP tools become request-time tool execution sources
- memory becomes retrieval and persistence around model calls
- agent orchestration becomes an execution mode rather than a separate external service

Those boundaries are already visible in code, but they should still be treated as evolving.

## 10. Design Tradeoffs

### 10.1 Why a Caddy App Instead of a Standalone Gateway Server

Using a Caddy app gives the project:

- a mature module graph
- shared provisioning lifecycle
- established HTTP pipeline integration
- existing config loading and deployment patterns

The downside is that some gateway concepts must fit Caddy's lifecycle and config style, which is why the project uses both app-level modules and persisted operational records.

### 10.2 Why Hybrid Static + Dynamic Config

Only static config would make operational updates clumsy. Only dynamic config would weaken the value of Caddy's module graph and startup-time composition.

The hybrid model keeps:

- static infra wiring in the Caddyfile
- mutable provider and route records in SQLite

This is slightly more complex, but it matches how the gateway is meant to be operated.

### 10.3 Why Keep the Route Model Ahead of the Caddyfile Grammar

The repository already needs a richer route object for admin APIs and internal policy evaluation. Shipping the richer data model first allows the runtime and storage layers to settle before the public Caddyfile grammar is expanded.

That means some fields are representable in JSON and Go types before they are representable in the Caddyfile.

## 11. Near-Term Evolution

The most coherent next steps for the architecture are:

- finish the missing admin handlers for MCP, memory, agents, and metrics
- expand enforcement of route policy beyond the currently active subset
- integrate MCP and memory into the request path
- complete the agent orchestrator tool-call loop
- expand Caddyfile route syntax to cover more of the existing route data model
- decide how the separate web UI becomes a first-class operator surface

Until then, the project should be understood primarily as a route-based Caddy LLM gateway with a broader agent-runtime architecture under active construction.
