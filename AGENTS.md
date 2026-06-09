# AGENTS.md

## Purpose

This repository builds a custom Caddy binary that acts as an AI gateway for LLM, MCP, and ACP traffic.
The current primary LLM path is:

1. `agent_gateway` app loads providers, routes, virtual keys, credentials, and CLI auth state
2. `agent_route_dispatcher` matches an incoming HTTP request to a route
3. the route's `protocol` selects the protocol adapter (`openai` or `anthropic`)
4. the gateway validates the VirtualKey
5. in logical-model routes, the model catalog resolves the logical model to one concrete `(provider_id, upstream_model)` binding
6. the selected provider executes `Generate` or `Stream`

MCP is also active now through `agent_route_dispatcher` with MCP enabled, `pkg/gateway/mcproute`, `pkg/mcp/service`, and MCP Admin APIs. ACP is being implemented natively through `pkg/acp`, `pkg/gateway/acproute`, dispatcher turn handling, and ACP Admin APIs. Memory, agent, and metrics areas still exist as earlier-stage subsystems.

## Change Policy

- by default, changes in this repository do not preserve backward compatibility
- do not keep legacy aliases, deprecated field names, old route shapes, old module IDs, old CLI flags, or old API-visible IDs unless the change request explicitly requires compatibility
- when renaming or reshaping behavior, update the code, tests, `README.md`, `docs/architecture/architecture-overview.md`, `Caddyfile.example`, and this file to describe only the current behavior unless compatibility is explicitly required

## Build & Run

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
./agw run --config ./Caddyfile.example

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
- The resulting binary is a standard Caddy binary with custom modules compiled in, so normal Caddy subcommands such as `run`, `reload`, `validate`, and `hash-password` work.

## Core Modules

### Caddy app

- Module ID: `agent_gateway`
- Package: `caddy/gateway/`
- Main entry: `caddy/gateway/app.go`

Responsibilities:

- initialize the config store
- load static providers from the Caddyfile
- create the shared credential manager and CLI auth refresher
- create the runtime `AgentGateway`

### HTTP middleware

- Module ID: `http.handlers.agent_route_dispatcher`
- Package: `caddy/dispatcher/`
- Main entry: `caddy/dispatcher/dispatcher.go`

Responsibilities:

- resolve the matching `AgentRoute`
- select the route's `protocol`
- rewrite the request path by removing the route `path_prefix`
- validate the VirtualKey
- prepare the provider request payload
- resolve the logical model or direct provider target
- rewrite the provider-facing request model when logical-model routing is used
- invoke the selected LLM protocol handler
- when `mcp` is configured, resolve `MCPRoute` requests, parse MCP JSON-RPC, and invoke `pkg/mcp/service`
- track in-flight MCP requests and progress through the shared runtime registry
- when `acp` is configured, resolve `ACPRoute` requests, parse the gateway ACP turn request, and invoke `pkg/acp/runtime`

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

Responsibilities:

- parse wire-format requests
- convert HTTP payloads into `provider.ChatRequest`
- convert provider responses back to protocol-specific JSON or SSE

The `cc` handler is the Claude Code CLI-compatible Anthropic Messages profile. Keep Claude Code CLI-specific protocol shims in this handler rather than in generic providers.

These modules are not standalone `http.handlers.*` modules. They are loaded by `agent_route_dispatcher`.

### Admin API

- Module ID: `http.handlers.agent_gateway_admin`
- Package: `caddy/admin/`

Responsibilities:

- session login with `POST /admin/auth/login`
- CRUD for providers, routes, virtual keys, and credentials
- CRUD for `mcp_services` and MCP routes
- CRUD for `acp_services` and ACP routes
- MCP discovery, execution, and dispatcher runtime inspection
- list startup-enabled provider types and LLM API handler types
- configure and trigger CLI auth authenticators
- start CLI auth logins bound to one `provider_id` and optional credential scope
- expose stubbed memory, agent, and metrics endpoints

## Key Packages

### `pkg/gateway/`

Important files:

- `agentgateway.go`: runtime route, VirtualKey, and provider resolution
- `providerresolver.go`: static and dynamic provider resolution

`AgentGateway` is the main runtime object. It resolves routes, validates VirtualKeys, and selects providers. It does not own the HTTP protocol details.

### `caddy/gateway/`

Important files:

- `app.go`: Caddy app wiring and runtime bootstrap
- `caddyfile.go`: global `agent_gateway` Caddyfile parsing

### `pkg/gateway/llmroute/`

Defines the route model used by static config, the Admin API, and runtime resolution.

Important types:

- `AgentRoute`
- `RouteMatch`
- `RouteTargetPolicy`
- `DirectProviderTarget`
- `RoutePolicy`
- `SelectionPolicy`
- `RetryPolicy`

Current route modes:

- model-target mode: `target_policy.model_targets` with optional `default_model`
- direct-provider mode: `target_policy.provider_target.provider_id`

Static config restriction:

- Caddyfile routes and standalone `--static-config` bundle `llmRoutes` only support direct-provider mode
- logical-model routes remain supported through the Admin API and config-store-backed bundle workflows

The route model uses `protocol` and `require_virtual_key`. Do not reintroduce the old `local API key` naming in new code or docs.

### `pkg/gateway/modelcatalog/`

This package owns provider model discovery, managed model overlays, and runtime validation of concrete route candidates.

Important types:

- `ManagedModel`
- `ProviderModelSnapshot`

### `pkg/gateway/mcproute/`

Defines the MCP route config expansion and runtime route model.

Important types:

- `MCPRouteConfig`
- `MCPRoute`
- `RouteMatch`
- `RouteAuthPolicy`

Current shape:

- persisted/static MCP routes use `routecore.AgentRouteConfig`
- `MCPRouteConfig` is the expanded config form used by admin and config-adjacent layers that need direct `service_id` access
- `MCPRoute` is the runtime object created by `MCPRouteResolver` and used by dispatcher/runtime code
- prefer `*MCPRoute` at runtime rather than copying `MCPRoute` values

### `pkg/gateway/acproute/`

Defines the ACP route config expansion and runtime route model.

Important types:

- `ACPRouteConfig`
- `ACPRoute`
- `RouteMatch`
- `RouteAuthPolicy`

Current shape:

- persisted/static ACP routes use `routecore.AgentRouteConfig`
- `ACPRouteConfig` is the expanded config form used by admin and config-adjacent layers that need direct `service_id` access
- `ACPRoute` is the runtime object created by `ACPRouteResolver` and used by dispatcher/runtime code

### `pkg/acp/`

Owns native ACP service config and runtime integration. Do not add a dependency on `github.com/beyond5959/ngent`; ACP runtime support is implemented in this repository.

Scope:

- supported agent types: `codex`, `opencode`
- service config store: `acp_services`
- dispatcher endpoint: `POST /<acp-route>/turn` with SSE events (`session`, `delta`, `reasoning`, `content`, `plan`, `tool_call`, `usage`, `available_commands`, `session_info`, `mode`, `config_options`, `done`, `error`)
- permission mode defaults to fail-closed `deny`; permission replies use the nested ACP outcome shape, run off the transport read loop, and time out fail-closed
- runtime shape: one stdio JSON-RPC driver plus thin agent adapters registered through `pkg/acp/agentspi`; `session/update` parsing lives in `pkg/acp/runtime/acpupdate`
- process shapes: `opencode` uses fixed `opencode acp --cwd <cwd>`; `codex` uses fixed external ACP adapter binary `codex-acp` by default
- model selection and `config_overrides` go through `session/set_config_option` (ACP has no `session/set_model`); opencode model selection sets its `model` config option
- pool lifecycle: idle janitor (`IdleTTL`), dead-instance eviction, `fresh_session`, setup-handshake timeout, `PATH` preflight, stderr capture, and `CloseScope`/`CloseThread`
- do not reintroduce a `model`/`modelId` field on `session/new`/`session/prompt`, and do not answer `session/request_permission` with a flat `approved`/`declined` outcome — both are non-conformant with the ACP v1 schema
- deferred: interactive permission workflow, `session/list`, transcript replay, codex stable-session rebinding, crash retry, and the codex app-server bridge (v2)

### `pkg/gateway/virtualkey/`

This package owns VirtualKey extraction, validation, and storage-facing helpers.

Use this terminology consistently:

- `VirtualKey`
- `VirtualKeyManager`
- `VirtualKeyStore`

Current shape:

- `VirtualKey.ID` is required and is the management/storage primary key
- `VirtualKey.Key` is the bearer key value used at request time and is generated by the gateway
- Caddyfile and standalone static bundle config do not support static virtual keys; create them through the Admin API

The gateway accepts a VirtualKey from either:

- `Authorization: Bearer <key>`
- `x-api-key: <key>`

### `pkg/llm/provider/`

This package defines the provider interface and provider registry.

Important files:

- `provider.go`: provider request and response types
- `registry.go`: provider factory registration

Provider config `api_key` values are provider-local fallback configuration.
They do not register as managed credentials and do not participate in credential scheduling.

Built-in provider runtime packages:

- `openai`
- `anthropic`
- `claudecode`
- `gemini`
- `ollama`
- `openrouter`
- `deepseek`
- `zhipu`

Provider registration rules:

- implement the `provider.Provider` interface
- register the factory with `provider.RegisterProviderFactory(...)`
- add a blank import for the runtime provider package in `cmd/agw/main.go` and `cmd/agwd/main.go` so the provider is linked into the binaries

### `pkg/cliauth/`

This is a `pkg` runtime package, not `llm/cliauth/`.

Important files:

- `authenticator.go`: `Authenticator` interface and factory registration
- `manager.go`: runtime authenticator registry and state
- `autorefresher.go`: background refresh scheduling
- `types.go`: credential and status types

Built-in authenticators currently registered via `pkg/cliauth/authenticator/`:

- `codex`
- `claudecode`
- `gemini`

Authenticator registration rules:

- implement the `cliauth.Authenticator` interface
- register the factory with `cliauth.RegisterAuthenticatorFactory(...)`
- ensure the package is included through the blank import of `pkg/cliauth/authenticator` in `cmd/agw/main.go`

### `pkg/llm/credentialmgr/`

This package manages persisted upstream credentials and selection state. It is separate from the provider registry and separate from `cliauth`, though `cliauth` integrates with it through an adapter.

### `pkg/configstore/`

Important packages:

- `pkg/configstore/`: generic store/backend interfaces, shared schema primitives, backend factory, and backend registration
- `pkg/configstore/schema/`: store names and built-in business schemas for persisted config object families
- `pkg/configstore/sqlite/`: SQLite JSON backend implementation
- `caddy/configstore/sqlite/`: SQLite backend Caddy adapter

The top-level storage interface is `ConfigStoreBackend`, which registers and returns schema-bound generic stores:

- `Register(name string, schema StoreSchema) error`
- `Get(name string) (ConfigStore, error)`

Current store names:

- `providers`
- `credentials`
- `routes`
- `mcp_services`
- `acp_services`
- `virtual_keys`
- `managed_models`

Current persisted backend:

- `sqlite`

## Runtime Request Flow

```text
HTTP request
  -> http.handlers.agent_route_dispatcher
  -> AgentGateway.ResolveRoute(...)
  -> pick route.protocol
  -> rewrite path using route.match.path_prefix
  -> AgentGateway.ResolveVirtualKey(...)
  -> protocol handler PrepareLLMApiRequest(...)
  -> if route uses model targets: resolve the requested route model name to one concrete binding and rewrite request model
  -> else: use route.target_policy.provider_target.provider_id
  -> resolve provider instance
  -> provider.Chat(...) or provider.StreamChat(...)
  -> protocol handler writes JSON or SSE response
```

Key detail: provider resolution still happens after protocol parsing, but the request `model` now means route target name in model-target mode and upstream model name in direct-provider mode.

## Caddyfile Shape

The main config lives in the global `agent_gateway` block.

Minimal example:

```caddy
{
    agent_gateway {
        config_store sqlite {
            path ./data/configstore.db
        }

        provider_types {
            openai
        }

        provider openai-main {
            provider_type openai
            api_key {$OPENAI_API_KEY}
            default_model gpt-4.1
        }

        route openai-chat {
            protocol openai
            path_prefix /
            require_virtual_key
            target provider openai-main
        }
    }
}

http://127.0.0.1:8080 {
    agent_route_dispatcher {
        llm_api openai
        llm_api anthropic
        llm_api cc
        mcp
        acp
    }
}
```

Important current directives:

- `provider_types` is startup-only provider type availability; when omitted all registered provider types are enabled
- providers use `provider_type <name>`
- LLM routes use `protocol <openai|anthropic|cc>`, MCP routes use `protocol mcp`, and ACP routes use `protocol acp`
- `agent_route_dispatcher` uses `llm_api <name>` for LLM protocol handlers, `mcp` to enable MCP protocol handling, and `acp` to enable ACP turn handling
- auth uses `virtualkey`, not `local_api_key`

## Admin API Notes

Implemented families:

- `/admin/auth/...`
- `/admin/providers/...`
- `/admin/provider_types` read-only listing
- `/admin/llm_api_handler_types`
- `/admin/llm/routes/...`
- `/admin/virtual_keys/...`
- `/admin/credentials/...`
- `/admin/models/providers/{provider_id}/discovered`
- `/admin/models/providers/{provider_id}/refresh`
- `/admin/models/managed/...`
- `/admin/models/logical/...`
- `/admin/cliauth/authenticators/...`
- `/admin/cliauth/refresher/...`
- `/admin/cliauth/logins/...`

Implemented MCP families:

- `/admin/mcp/services/...`
- `/admin/mcp/routes/...`
- `/admin/mcp/runtime/...`

Implemented ACP families:

- `/admin/acp/services/...`
- `/admin/acp/routes/...`
- `/admin/acp/runtime/...`

Stubbed families currently return `501 Not Implemented`:

- `/admin/memory/...`
- `/admin/agents/...`
- `/admin/metrics/...`

## Files To Check Before Large Changes

- `README.md`: user-facing setup and API examples
- `docs/architecture/architecture-overview.md`: broader architecture and roadmap
- `Caddyfile.example`: working reference config
- `cmd/agw/main.go`: the definitive list of linked modules

If you change module IDs, route semantics, provider registration, or Admin API paths, update this file and `README.md` in the same change.
