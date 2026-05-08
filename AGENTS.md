# AGENTS.md

## Purpose

This repository builds a custom Caddy binary that acts as an AI gateway for LLM traffic.
The current production path is:

1. `agent_gateway` app loads providers, routes, virtual keys, credentials, and CLI auth state
2. `agent_route_dispatcher` matches an incoming HTTP request to a route
3. the route's `llm_api` selects the protocol adapter (`openai` or `anthropic`)
4. the gateway validates the VirtualKey
5. in logical-model routes, the model catalog resolves the logical model to one concrete `(provider_id, upstream_model)` binding
6. the selected provider executes `Generate` or `Stream`

MCP, memory, agent, and metrics areas exist in the repo, but the main implemented runtime today is LLM routing plus Admin APIs.

## Build & Run

```bash
# Build the main gateway binary and the management CLI
make build

# Or build only the gateway binary
go build -o agw ./cmd/main.go

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

- `make build` builds both `agw` from `cmd/main.go` and `agwctl` from `cmd/agwctl`.
- The resulting binary is a standard Caddy binary with custom modules compiled in, so normal Caddy subcommands such as `run`, `reload`, `validate`, and `hash-password` work.

## Core Modules

### Caddy app

- Module ID: `agent_gateway`
- Package: `gateway/`
- Main entry: `gateway/app.go`

Responsibilities:

- initialize the config store
- load static providers from the Caddyfile
- create the shared credential manager and CLI auth refresher
- create the runtime `AgentGateway`

### HTTP middleware

- Module ID: `http.handlers.agent_route_dispatcher`
- Package: `dispatcher/`
- Main entry: `dispatcher/dispatcher.go`

Responsibilities:

- resolve the matching `AgentRoute`
- select the route's `llm_api`
- rewrite the request path by removing the route `path_prefix`
- validate the VirtualKey
- prepare the provider request payload
- resolve the logical model or direct provider target
- rewrite the provider-facing request model when logical-model routing is used
- invoke the selected protocol handler

### Protocol handler modules

- Module ID: `agent_route_dispatcher.llm_apis.openai`
  - Package: `dispatcher/llmapi/openai/`
- Module ID: `agent_route_dispatcher.llm_apis.anthropic`
  - Package: `dispatcher/llmapi/anthropic/`

Responsibilities:

- parse wire-format requests
- convert HTTP payloads into `provider.ChatRequest`
- convert provider responses back to protocol-specific JSON or SSE

These modules are not standalone `http.handlers.*` modules. They are loaded by `agent_route_dispatcher`.

### Admin API

- Module ID: `http.handlers.agent_gateway_admin`
- Package: `admin/`

Responsibilities:

- session login with `POST /admin/auth/login`
- CRUD for providers, routes, virtual keys, and credentials
- enable or disable provider types and LLM API handler types
- configure and trigger CLI auth authenticators
- expose stubbed MCP, memory, agent, and metrics endpoints

## Key Packages

### `gateway/`

Important files:

- `app.go`: Caddy app wiring
- `agentgateway.go`: runtime route, VirtualKey, and provider resolution
- `providerresolver.go`: static and dynamic provider resolution
- `caddyfile.go`: global `agent_gateway` Caddyfile parsing

`AgentGateway` is the main runtime object. It resolves routes, validates VirtualKeys, and selects providers. It does not own the HTTP protocol details.

### `gateway/route/`

Defines the route model used by both Caddyfile config and the Admin API.

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

The route model uses `llm_api` and `require_virtual_key`. Do not reintroduce the old `local API key` naming in new code or docs.

### `gateway/modelcatalog/`

This package owns provider model discovery, managed model overlays, and runtime validation of concrete route candidates.

Important types:

- `ManagedModel`
- `ProviderModelSnapshot`

### `gateway/virtualkey/`

This package owns VirtualKey extraction, validation, and storage-facing helpers.

Use this terminology consistently:

- `VirtualKey`
- `VirtualKeyManager`
- `VirtualKeyStore`

The gateway accepts a VirtualKey from either:

- `Authorization: Bearer <key>`
- `x-api-key: <key>`

### `llm/provider/`

This package defines the provider interface and provider registry.

Important files:

- `provider.go`: provider request and response types
- `registry.go`: provider factory registration
- `module.go`: common Caddy module parsing helpers
- `authmanaged.go`: provider wrapper that injects managed credentials

Built-in provider packages currently compiled by `cmd/main.go`:

- `openai`
- `anthropic`
- `gemini`
- `ollama`
- `openrouter`
- `deepseek`
- `zhipu`

Provider registration rules:

- implement the `provider.Provider` interface
- register the factory with `provider.RegisterProviderFactory(...)`
- register the Caddy module under `llm.providers.<name>`
- add a blank import in `cmd/main.go` so the provider is linked into the binary

### `pkg/cliauth/`

This is a `pkg` runtime package, not `llm/cliauth/`.

Important files:

- `authenticator.go`: `Authenticator` interface and factory registration
- `manager.go`: runtime authenticator registry and state
- `autorefresher.go`: background refresh scheduling
- `types.go`: credential and status types

Built-in authenticators currently registered via `pkg/cliauth/authenticator/`:

- `codex`
- `claude`
- `gemini`

Authenticator registration rules:

- implement the `cliauth.Authenticator` interface
- register the factory with `cliauth.RegisterAuthenticatorFactory(...)`
- ensure the package is included through the blank import of `pkg/cliauth/authenticator` in `cmd/main.go`

### `pkg/llm/credentialmgr/`

This package manages persisted upstream credentials and selection state. It is separate from the provider registry and separate from `cliauth`, though `cliauth` integrates with it through an adapter.

### `configstore/`

Important packages:

- `pkg/configstore/intf/`: storage interfaces
- `configstore/sqlite/`: SQLite implementation

The top-level storage interface is `ConfigStorer`, which vends:

- `CredentialStorer`
- `ProviderConfigStorer`
- `VirtualKeyStorer`
- `RouteStorer`
- `ModelStorer`

Current persisted backend:

- `sqlite`

## Runtime Request Flow

```text
HTTP request
  -> http.handlers.agent_route_dispatcher
  -> AgentGateway.ResolveRoute(...)
  -> pick route.llm_api
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

        provider openai-main {
            provider_type openai
            api_key {$OPENAI_API_KEY}
            default_model gpt-4.1
        }

        virtualkey test-key {
            tag local-test
            allowed_route openai-chat
        }

        route openai-chat {
            llm_api openai
            path_prefix /
            require_virtual_key
            target model chat-default openai-main gpt-4.1 weight 100 default
        }
    }
}

http://127.0.0.1:8080 {
    agent_route_dispatcher {
        llm_api openai
        llm_api anthropic
    }
}
```

Important current directives:

- providers use `provider_type <name>`
- routes use `llm_api <openai|anthropic>`
- auth uses `virtualkey`, not `local_api_key`

## Admin API Notes

Implemented families:

- `/admin/auth/...`
- `/admin/providers/...`
- `/admin/provider_types/...`
- `/admin/llm_api_handler_types/...`
- `/admin/routes/...`
- `/admin/virtual_keys/...`
- `/admin/credentials/...`
- `/admin/models/providers/{provider_id}/discovered`
- `/admin/models/providers/{provider_id}/refresh`
- `/admin/models/managed/...`
- `/admin/models/logical/...`
- `/admin/cliauth/authenticators/...`
- `/admin/cliauth/refresher/...`
- `/admin/cliauth/logins/...`

Stubbed families currently return `501 Not Implemented`:

- `/admin/mcp/...`
- `/admin/memory/...`
- `/admin/agents/...`
- `/admin/metrics/...`

## Files To Check Before Large Changes

- `README.md`: user-facing setup and API examples
- `DESIGN.md`: broader architecture and roadmap
- `Caddyfile.example`: working reference config
- `cmd/main.go`: the definitive list of linked modules

If you change module IDs, route semantics, provider registration, or Admin API paths, update this file and `README.md` in the same change.
