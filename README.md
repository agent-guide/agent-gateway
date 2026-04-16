# caddy-agent-gateway

`caddy-agent-gateway` is a Caddy-native AI gateway for agent and LLM workloads. It runs as a custom Caddy build, exposes compatible LLM HTTP APIs, centralizes provider access and credential management, and persists gateway configuration in SQLite by default.

The current codebase is already usable as a route-oriented LLM gateway. It also contains the early runtime scaffolding for MCP, memory, and agent orchestration, but those parts are not fully wired into request execution yet.

## What Exists Today

- A Caddy app module named `agent_gateway`
- HTTP handlers:
  - `llm_api openai`
  - `llm_api anthropic`
  - `agent_gateway_admin`
- Provider modules under `llm.providers.*`
- Authenticator modules under `llm.authenticators.*`
- SQLite-backed config storage for providers, routes, credentials, and local API keys
- Route-based target selection with weighted targets and route policy defaults
- Admin API CRUD for providers, routes, local API keys, and stored credentials
- Async CLI login flows for Codex/OpenAI, Claude, and Gemini authenticators

## Current Module Layout

- `gateway/`
  - Owns the `agent_gateway` Caddy app
  - Loads providers, authenticators, config store, and static routes
  - Builds runtime dependencies such as route loading, provider resolution, and local API key lookup
- `api/`
  - Registers `llm_api`
  - Includes the parent `http.handlers.llm_api` middleware plus OpenAI-compatible and Anthropic-compatible child handlers
- `admin/`
  - Registers `agent_gateway_admin`
  - Exposes operational endpoints under `/admin/*`
- `llm/provider/`
  - Shared provider interfaces and provider implementations
  - Implemented providers: `openai`, `anthropic`, `gemini`, `ollama`, `openrouter`, `zhipu`
- `llm/cliauth/`
  - Credential manager and provider-specific authenticators
  - Implemented authenticators: `codex`, `claude`, `gemini`
- `configstore/sqlite/`
  - Default persisted config backend
- `llm/mcp/`, `llm/memory/`, `llm/agent/`
  - Early interfaces and partial implementations for future runtime integration
- `web/`
  - Separate web UI work-in-progress, not yet the primary control plane

## Build

Build the custom Caddy binary directly:

```bash
go build -o caddy-agent-gateway ./cmd/main.go
```

Or use the existing Make target:

```bash
make build
```

The binary links the gateway app, admin handler, API handlers, and the built-in provider modules.

## Quick Start

Minimal `Caddyfile`:

```caddy
{
    admin localhost:2019

    agent_gateway {
        provider openai {
            api_key {$OPENAI_API_KEY}
            default_model gpt-4.1
        }

        config_store sqlite {
            path ./data/configstore.db
        }

        route openai-chat {
            require_local_api_key
            allowed_model gpt-4.1
            allowed_model gpt-4.1-mini
            target openai
        }
    }
}

:8082 {
    route /v1/* {
        llm_api openai {
            llm_route_id openai-chat
        }
    }

    route /admin/* {
        agent_gateway_admin
    }
}
```

Run it:

```bash
./caddy-agent-gateway run --config ./Caddyfile
```

Test the OpenAI-compatible endpoint with Python:

```bash
python3 -m pip install openai
OPENAI_API_KEY=sk-... ./caddy-agent-gateway run --config ./Caddyfile
python3 examples/test_openai_client.py
python3 examples/test_openai_client.py --stream
```

The Python example sends the gateway-local key as `Authorization: Bearer test-key`.
Override defaults with `AGENT_GATEWAY_BASE_URL`, `AGENT_GATEWAY_API_KEY`, or `AGENT_GATEWAY_MODEL`.

## Caddyfile Model

The runtime is centered on the global `agent_gateway` block.

```caddy
{
    agent_gateway {
        provider <name> { ... }
        config_store sqlite { ... }
        authenticator <name> { ... }
        localapikey <key> { ... }
        route <route-id> { ... }
    }
}
```

### Supported Global Subdirectives

- `provider <name> { ... }`
  - Loads a provider module from `llm.providers.<name>`
- `config_store sqlite { ... }`
  - Configures the default SQLite-backed config store
- `authenticator <name> { ... }`
  - Loads an authenticator from `llm.authenticators.<name>`
- `localapikey <key> { ... }`
  - Declares a static local API key and syncs it into the configured local API key store during startup
- `route <route-id> { ... }`
  - Declares a static gateway route

### Current Static Route Syntax

Static routes currently support these subdirectives:

- `require_local_api_key [true|false]`
- `allowed_model <model> [more-models...]`
- `target <provider> [weight]`

`target` entries are currently parsed as weighted targets. More advanced target conditions and policies exist in Go types and Admin API payloads, but the Caddyfile parser does not expose all of them yet.

### Static Local API Key Syntax

Static local API keys currently support these subdirectives:

- `user_id <user>`
- `name <display-name>`
- `description <text>`
- `disabled [true|false]`
- `allowed_route <route-id> [more-route-ids...]`
- `status_message <text>`
- `expires_at <rfc3339>`

Example:

```caddy
{
    agent_gateway {
        localapikey key1 {
            user_id admin
            name "Primary local key"
            allowed_route openai-chat
        }
    }
}
```

## Request Flow

For a normal API call:

1. The HTTP handler selected by `llm_api` receives the request.
2. The handler resolves `llm_route_id`.
3. The gateway loads the route definition from the config store when available, otherwise from static app config.
4. If the route requires a local API key, the gateway validates the caller key.
5. The gateway resolves the target provider.
6. The compatible API handler converts the request into the internal provider request format.
7. The provider executes the upstream call and returns the translated response.

This means route and provider definitions managed through the Admin API can take effect without rebuilding the whole Caddy config.

## Providers

Built-in providers:

- `openai`
- `anthropic`
- `gemini`
- `ollama`
- `openrouter`
- `zhipu`

All providers implement the shared `provider.Provider` interface. Some providers also implement optional capabilities such as embeddings.

Custom providers can be added by shipping a Caddy module under `llm.providers.<name>` that implements the shared provider interfaces and is linked into the final Caddy build.

## Authentication and Credentials

There are two different credential layers in the project:

- Upstream provider credentials
  - Managed by the auth manager
  - Used when the gateway talks to OpenAI, Anthropic, Gemini, and other providers
- Local gateway API keys
  - Stored as `LocalAPIKey`
  - Used by agent clients to authenticate to the gateway itself

Built-in authenticators:

- `codex`
- `claude`
- `gemini`

If no `authenticator` block is declared, no CLI login flow is enabled.

## Admin API

The admin surface is mounted through `agent_gateway_admin` and currently includes:

- `GET /admin/health`
- Provider CRUD:
  - `GET /admin/providers`
  - `POST /admin/providers`
  - `GET /admin/providers/{id}`
  - `PUT /admin/providers/{id}`
  - `DELETE /admin/providers/{id}`
- Route CRUD:
  - `GET /admin/routes`
  - `POST /admin/routes`
  - `GET /admin/routes/{id}`
  - `PUT /admin/routes/{id}`
  - `DELETE /admin/routes/{id}`
- Local API key CRUD:
  - `GET /admin/local_api_keys`
  - `POST /admin/local_api_keys`
  - `GET /admin/local_api_keys/{key}`
  - `PUT /admin/local_api_keys/{key}`
  - `DELETE /admin/local_api_keys/{key}`
- CLI auth credentials:
  - `GET /admin/cliauth/credentials`
  - `GET /admin/cliauth/credentials/{credential_id}`
  - `DELETE /admin/cliauth/credentials/{credential_id}`
- CLI auth authenticators:
  - `GET /admin/cliauth/authenticators`
  - `POST /admin/cliauth/authenticators/{authenticator_name}/enable`
  - `POST /admin/cliauth/authenticators/{authenticator_name}/disable`
  - `POST /admin/cliauth/authenticators/{authenticator_name}/login`
  - `GET /admin/cliauth/authenticators/{authenticator_name}/login/status`

The route table also includes MCP, memory, agent, and metrics endpoints, but those handlers currently return `501 not implemented`.

## Admin API Examples

Login and capture a session token:

```bash
TOKEN=$(
  curl -s -X POST http://localhost:8082/admin/auth/login \
    -H 'Content-Type: application/json' \
    -d '{
      "username": "admin",
      "password": "your-password"
    }' | jq -r '.token'
)
```

Create a provider record:

```bash
curl -X POST http://localhost:8082/admin/providers \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "openrouter",
    "provider_name": "openrouter",
    "base_url": "https://openrouter.ai/api/v1",
    "default_model": "openai/gpt-4o-mini"
  }'
```

List providers:

```bash
curl http://localhost:8082/admin/providers \
  -H "Authorization: Bearer $TOKEN"
```

Admin sessions are stored in memory. If the service restarts, previously issued
tokens become invalid and callers must log in again to obtain a fresh token.

## Caddy Admin API

The backend also exposes authenticated Caddy management endpoints under `/admin/caddy/`.

All endpoints in this section:

- require `Authorization: Bearer <token>`
- return JSON
- return errors in the shape `{ "error": "..." }`
- return `503` with `{"error":"caddy manager not configured"}` when the admin handler was not provisioned with a Caddy manager

These endpoints are implemented in:

- `admin/routes.go`
- `admin/caddy_handlers.go`
- `admin/caddymgr/types.go`
- `admin/caddymgr/manager.go`

### Data Shapes

#### Server request

Used by `POST /admin/caddy/servers` and `PUT /admin/caddy/servers/{id}`.

```json
{
  "id": "public",
  "listen": [":443"],
  "tls": {
    "auto": true,
    "cert_file": "/path/to/cert.pem",
    "key_file": "/path/to/key.pem"
  }
}
```

Notes:

- `id` is required on create
- `listen` must contain at least one address
- on update, the backend uses the path parameter as the server ID
- `tls.auto=true` enables automatic HTTPS via ACME
- `cert_file` and `key_file` are accepted in the payload shape, but the current manager does not translate manual certificate configuration into Caddy's `tls` app

#### Server response

```json
{
  "id": "public",
  "listen": [":443"],
  "routes": [
    {
      "id": "llm-api",
      "order": 0,
      "match": {
        "paths": ["/v1/*"],
        "hosts": ["api.example.com"]
      },
      "handlers": [
        {
          "type": "openai",
          "route_id": "default"
        }
      ]
    }
  ],
  "readonly": true,
  "source": "system",
  "public_url": "https://127.0.0.1:443/"
}
```

Notes:

- `readonly=true` means the server is treated as system-managed and cannot be mutated through these endpoints
- `source` is currently set to `"system"` only for readonly servers
- `public_url` is currently populated only for readonly servers

#### Route request

Used by `POST /admin/caddy/servers/{id}/routes` and `PUT /admin/caddy/servers/{id}/routes/{routeId}`.

```json
{
  "id": "llm-api",
  "order": 0,
  "match": {
    "paths": ["/v1/*"],
    "hosts": ["api.example.com"]
  },
  "handler": {
    "type": "openai",
    "route_id": "default",
    "upstream": "127.0.0.1:8080",
    "root": "/srv/www"
  }
}
```

Notes:

- `id` is required on create
- on update, the backend uses the `{routeId}` path parameter as the route ID
- `order` controls insert position only on create; update keeps the existing position
- handler-specific fields:
  - `type: "openai"` or `"anthropic"` uses `route_id`
  - `type: "reverse_proxy"` uses `upstream`
  - `type: "file_server"` uses `root`
  - `type: "admin"` maps to the Caddy handler `agent_gateway_admin`

#### Route response

```json
{
  "id": "llm-api",
  "order": 0,
  "match": {
    "paths": ["/v1/*"],
    "hosts": ["api.example.com"]
  },
  "handlers": [
    {
      "type": "openai",
      "route_id": "default"
    }
  ]
}
```

Notes:

- `handlers` contains all handlers found in the underlying Caddy route
- Web-UI-managed routes currently have exactly one handler
- Caddyfile-defined routes may surface multiple handlers
- routes defined in the Caddyfile may have an empty `id`; those are effectively read-only from this API

### Server Endpoints

#### `GET /admin/caddy/servers`

Lists all HTTP servers currently registered in Caddy.

Response:

```json
{
  "items": [
    {
      "id": "public",
      "listen": [":443"],
      "routes": [],
      "readonly": true,
      "source": "system",
      "public_url": "https://127.0.0.1:443/"
    }
  ]
}
```

#### `POST /admin/caddy/servers`

Creates a new HTTP server.

Success:

- `201 Created` with a `ServerResponse` body

Common failures:

- `400` for invalid JSON
- `500` for validation or Caddy admin errors such as missing `id`, empty `listen`, duplicate server ID, or unreachable Caddy admin API

Example:

```bash
curl -X POST http://localhost:8082/admin/caddy/servers \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "public",
    "listen": [":443"],
    "tls": { "auto": true }
  }'
```

#### `GET /admin/caddy/servers/{id}`

Returns a single server by ID.

Success:

- `200 OK` with a `ServerResponse` body

Common failures:

- `404` when the server does not exist

#### `PUT /admin/caddy/servers/{id}`

Updates `listen` and `tls` on an existing server while preserving current routes.

Success:

- `200 OK` with the updated `ServerResponse`

Common failures:

- `400` for invalid JSON
- `403` when the target server is readonly
- `404` when the server does not exist
- `500` for validation or Caddy admin errors

Example:

```bash
curl -X PUT http://localhost:8082/admin/caddy/servers/public \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "listen": [":443", ":8443"],
    "tls": { "auto": true }
  }'
```

#### `DELETE /admin/caddy/servers/{id}`

Deletes a server.

Success:

```json
{
  "status": "deleted",
  "id": "public"
}
```

Common failures:

- `403` when the target server is readonly
- `404` when the server does not exist

### Route Endpoints

#### `GET /admin/caddy/servers/{id}/routes`

Lists all routes for the specified server.

Response:

```json
{
  "items": [
    {
      "id": "llm-api",
      "order": 0,
      "match": {
        "paths": ["/v1/*"]
      },
      "handlers": [
        {
          "type": "openai",
          "route_id": "default"
        }
      ]
    }
  ]
}
```

Notes:

- returns both Web-UI-managed routes and routes discovered from the Caddyfile
- Caddyfile routes may have empty `id`

Common failures:

- `404` when the server does not exist

#### `POST /admin/caddy/servers/{id}/routes`

Adds a route to a server.

Success:

- `201 Created` with the newly added `RouteResponse`

Common failures:

- `400` for invalid JSON
- `403` when the target server is readonly
- `404` when the server does not exist
- `500` for validation or Caddy admin errors such as empty route ID or duplicate route ID

Example:

```bash
curl -X POST http://localhost:8082/admin/caddy/servers/public/routes \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "llm-api",
    "order": 0,
    "match": {
      "paths": ["/v1/*"],
      "hosts": ["api.example.com"]
    },
    "handler": {
      "type": "openai",
      "route_id": "default"
    }
  }'
```

#### `PUT /admin/caddy/servers/{id}/routes/{routeId}`

Updates an existing route in place.

Success:

- `200 OK` with the updated `RouteResponse`

Common failures:

- `400` for invalid JSON
- `403` when the target server is readonly
- `404` when the route does not exist
- `500` for Caddy admin errors

Example:

```bash
curl -X PUT http://localhost:8082/admin/caddy/servers/public/routes/llm-api \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "order": 0,
    "match": {
      "paths": ["/v2/*"]
    },
    "handler": {
      "type": "reverse_proxy",
      "upstream": "127.0.0.1:8080"
    }
  }'
```

#### `DELETE /admin/caddy/servers/{id}/routes/{routeId}`

Deletes a route from a server.

Success:

```json
{
  "status": "deleted",
  "id": "llm-api"
}
```

Common failures:

- `403` when the target server is readonly
- `404` when the route does not exist

Create a route record:

```bash
curl -X POST http://localhost:8082/admin/routes \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "chat-prod",
    "name": "chat-prod",
    "targets": [
      { "provider_ref": "openrouter", "mode": "weighted", "weight": 1 }
    ],
    "policy": {
      "auth": { "require_local_api_key": true },
      "allowed_models": ["gpt-4o-mini"]
    }
  }'
```

Create a local API key:

```bash
curl -X POST http://localhost:8082/admin/local_api_keys \
  -H 'Content-Type: application/json' \
  -d '{
    "key": "lk-demo",
    "name": "demo key",
    "allowed_route_ids": ["chat-prod"]
  }'
```

Call the gateway:

```bash
curl http://localhost:8082/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'x-api-key: lk-demo' \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "hello"}]
  }'
```

## Dynamic vs Static Configuration

There are two configuration sources:

- Static Caddyfile config under `agent_gateway`
- Persisted config in the SQLite config store

At runtime:

- Static providers are loaded during app provisioning.
- Persisted provider records can be resolved dynamically by ID.
- Static routes are registered at startup.
- If a handler specifies `route_id`, the gateway attempts to reload the latest persisted route definition for that ID on each request.

That design lets operators keep the Caddy app stable while changing route and provider records through the Admin API.

## Current Limits

These parts are real but incomplete:

- MCP transport packages and manager scaffolding exist, but Admin API integration is not implemented
- Memory interfaces and adapters exist, but request-path integration is partial
- Agent orchestration exists as an early loop around provider calls, but tool execution and memory integration are still TODOs
- The web dashboard exists as a separate Next.js app, but it is not yet the canonical operational surface

## Roadmap Direction

The repository is trending toward a fuller AI gateway that can:

- expose stable, provider-agnostic APIs for agent runtimes,
- centralize both gateway-side and upstream-side auth,
- integrate MCP and memory into gateway-managed execution,
- support richer routing policy and provider failover,
- and expose a complete operational control plane over Admin API and web UI.
