# agent-gateway

`agent-gateway` is an AI gateway for LLM and agent workloads. It supports both a custom Caddy-based binary (`agw`) and a standalone daemon (`agwd`), and provides:

- OpenAI-compatible and Anthropic-compatible HTTP APIs
- route-based dispatch to logical models or direct upstream providers
- static Caddyfile configuration in `agw`, plus SQLite-backed dynamic configuration shared by both runtimes
- admin APIs for providers, model catalog, routes, virtual keys, upstream credentials, and CLI auth
- early MCP, memory, metrics, and agent endpoint scaffolding

Repository change policy:

- backward compatibility is not preserved by default
- legacy aliases, deprecated names, and old API-visible IDs are only kept when a change explicitly requires compatibility

Go module path:

- `github.com/agent-guide/agent-gateway`

The request path today is centered on LLM routing. MCP, memory, metrics, and agent Admin API routes are registered, but they currently return `501 not implemented`.

## Current Modules

- Caddy app: `agent_gateway`
- HTTP handlers:
  - `agent_route_dispatcher`
  - `agent_gateway_admin`
- Dispatcher LLM APIs:
  - `openai`
  - `anthropic`
- Provider modules:
  - `openai`
  - `anthropic`
  - `gemini`
  - `ollama`
  - `openrouter`
  - `deepseek`
  - `zhipu`
- CLI auth authenticators:
  - `codex`
  - `claude`
  - `gemini`
- Config store:
  - `sqlite`

## Repository Layout

- `cmd/` - thin entrypoints for `agw`, `agwd`, and `agwctl`
- `pkg/gateway/` - runtime managers, route selection, provider resolution, virtual key validation
- `caddy/gateway/` - `agent_gateway` Caddy app adapter and Caddyfile parsing
- `pkg/dispatcher/` - runtime dispatcher and protocol handlers, independent of Caddy
- `caddy/dispatcher/` - `agent_route_dispatcher` Caddy adapter and Caddyfile parsing
- `pkg/admin/` - runtime Admin API handler, routes, and session auth
- `caddy/admin/` - `agent_gateway_admin` Caddy adapter
- `pkg/llm/provider/` - provider interface and built-in provider implementations
- `caddy/provider/` - Caddy provider module adapters
- `pkg/cliauth/` - CLI login authenticators and manager
- `pkg/llm/credentialmgr/` - upstream credential registration and scheduling state
- `pkg/configstore/` - generic config store interfaces, schema primitives, backend factory, and registration
- `pkg/configstore/schema/` - store names and built-in schemas for persisted config object families
- `pkg/configstore/sqlite/` - SQLite JSON persisted configuration backend
- `caddy/configstore/sqlite/` - SQLite config store backend Caddy adapter
- `standalone/server/` - standalone HTTP server assembly used by `agwd`
- `pkg/mcp/` - early MCP transport and client scaffolding
- `pkg/llm/memory/`, `pkg/llm/agent/` - early memory and agent runtime scaffolding

## Architecture Docs

- [docs/DESIGN.md](docs/DESIGN.md) - current architecture overview
- [docs/configstore-design.md](docs/configstore-design.md) - ConfigStore architecture and technical specification
- [docs/gateway-bundle-yaml-design.md](docs/gateway-bundle-yaml-design.md) - gateway bundle YAML proposal

## Build

```bash
go build -o agw ./cmd/agw
```

or:

```bash
make build
```

The `agw` binary includes Caddy standard modules, the gateway app adapter, the admin handler, LLM API handlers, built-in providers, and CLI authenticators. `make build` also builds the standalone daemon as `agwd` and the management CLI as `agwctl`.

## Binary Names

- `agw`: the main gateway runtime binary
- `agwd`: the standalone gateway daemon without a Caddyfile runtime
- `agwctl`: the management CLI for gateway admin, Caddy admin, and CLI auth operations

## Management CLI

`agwctl` is the management CLI for the gateway Admin API, direct Caddy admin API operations, and local CLI auth login flows.

Important distinction:

- `agwctl gateway credential ...` manages remote gateway credentials through the Admin API, including `api_key` and `cliauth_token` sources
- `agwctl cliauth ...` runs local login flows; the login usage itself shows supported authenticator names
- `agwctl gateway cliauth ...` inspects remote gateway CLI auth authenticators and manages refresher state through the Admin API
- `agwctl gateway apply/export ...` manages remote CLI auth authenticator config as part of the gateway bundle

Show available commands:

```bash
./agwctl --help
```

List gateway routes through the gateway Admin API:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  route list \
  --admin-user admin \
  --admin-password your-password
```

List Caddy HTTP servers through the Caddy admin API directly, not through the gateway Admin API:

```bash
./agwctl caddy --addr http://127.0.0.1:2019 server list
```

Start a local CLI auth login flow and list gateway-stored CLI auth credentials:

```bash
./agwctl cliauth login --authenticator codex
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  credential list \
  --source cliauth_token
```

List remote gateway CLI auth authenticators and refresher status:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  cliauth authenticators list

./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  cliauth refresher status
```

Validate a gateway bundle YAML file locally:

```bash
./agwctl gateway validate -f ./examples/gateway.bundle.minimal.yaml
```

Apply a gateway bundle YAML file through the Admin API:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  apply -f ./examples/gateway.bundle.minimal.yaml
```

Export remote gateway objects as bundle YAML:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  export -f ./gateway.bundle.yaml
```

Recommended workflow for configuration objects:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  export -f ./gateway.bundle.yaml

./agwctl gateway validate -f ./gateway.bundle.yaml

./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  apply -f ./gateway.bundle.yaml
```

Configuration objects no longer use per-object JSON `create` / `update` / `upsert` commands as the recommended CLI path. Use gateway bundle YAML for:

Bundle YAML examples for batch workflows:

- `examples/gateway.bundle.minimal.yaml`
- `examples/gateway.bundle.logical-model.yaml`
- `examples/gateway.bundle.cliauth-authenticators.yaml`

Static route restriction:

- Caddyfile routes and `agwd --static-config` routes only support direct-provider targets
- logical-model routes remain supported through the Admin API and `agwctl gateway apply`

For `agwctl gateway apply/export`, `virtualKeys` are declared by `id`. The gateway generates the actual `key` value when the virtual key is created in the config store.

- `providers`
- `managedModels`
- `routes`
- `virtualKeys`
- `cliAuthAuthenticators`
- `credentials`

Common command patterns:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  apply -f ./examples/gateway.bundle.minimal.yaml

./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  apply -f ./examples/gateway.bundle.cliauth-authenticators.yaml

./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  cliauth authenticators get codex
```

## Quick Start

Create a minimal `Caddyfile`:

```caddy
{
	admin localhost:2019

	agent_gateway {
		config_store sqlite {
			path ./data/configstore.db
		}

		provider openai-main {
			provider_type openai
			api_key {$OPENAI_API_KEY}
			default_model gpt-4.1
		}

		route openai-chat {
			llm_api openai
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
	}
}
```

Run the gateway:

```bash
OPENAI_API_KEY=sk-... ./agw run --config ./Caddyfile
```

After startup, create a virtual key through the Admin API, set `AGW_API_KEY` to the generated key value, then call the OpenAI-compatible endpoint:

```bash
AGW_API_KEY=$(
  curl -s -X POST http://localhost:8019/admin/virtual_keys \
    -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d '{"id":"test-key","allowed_route_ids":["openai-chat"]}' |
    jq -r '.key'
)

curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $AGW_API_KEY" \
  -d '{
    "model": "gpt-4.1",
    "messages": [{"role": "user", "content": "hello"}]
  }'
```

The VirtualKey may be sent as either `x-api-key: <key>` or `Authorization: Bearer <key>`.

The Python OpenAI SDK example uses `http://127.0.0.1:8080/v1` and `gpt-4.1` by default:

```bash
python3 -m pip install openai
python3 examples/test_openai_client.py
python3 examples/test_openai_client.py --stream
python3 examples/test_openai_client.py --api responses
python3 examples/test_openai_client.py --api responses --stream
```

Override example defaults with `AGW_BASE_URL`, `AGW_API_KEY`, `AGW_MODEL`, `AGW_OPENAI_API`, or CLI flags.

## Admin API Setup

Admin routes are mounted with `agent_gateway_admin`. Protected Admin API routes require:

1. an `admin_user`
2. an `admin_password_hash` bcrypt hash
3. a session token from `POST /admin/auth/login`

Example:

```caddy
http://localhost:8019 {
	route /admin/* {
		agent_gateway_admin {
			admin_user admin
			admin_password_hash <bcrypt-hash>
		}
	}
}
```

Generate the bcrypt hash with Caddy:

```bash
./agw hash-password --plaintext 'your-password'
```

Log in:

```bash
TOKEN=$(
  curl -s -X POST http://localhost:8019/admin/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"your-password"}' |
    jq -r '.token'
)
```

Admin sessions are in memory. Restarting the service invalidates existing tokens.

## Caddyfile Configuration

This section applies to the `agw` runtime. If you run `agwd`, use `--config-store` and optional `--static-config` bundle YAML instead of a Caddyfile.

The gateway is configured in the global `agent_gateway` block:

```caddy
{
	agent_gateway {
		config_store sqlite { ... }
		provider <provider-id> { ... }
		route <route-id> { ... }
	}
}
```

### Config Store

```caddy
config_store sqlite {
	path ./data/configstore.db
}
```

If `path` is omitted in `agw`, the store defaults to Caddy's app data directory under `agent-gateway/configstore.db`.

### Providers

Common provider settings:

```caddy
provider openai-main {
	provider_type openai
	api_key {$OPENAI_API_KEY}
	base_url https://api.openai.com/v1
	default_model gpt-4.1
	request_timeout_seconds 120
	max_retries 3
	retry_delay_seconds 1
	max_idle_connections 100
	max_idle_connections_per_host 20
	idle_keep_alive_timeout_seconds 90
	proxy_url http://127.0.0.1:7890
	header X-Custom value
	option organization org_...
}
```

Provider-specific notes:

- `openai` defaults to `https://api.openai.com/v1`.
- `deepseek` defaults to `https://api.deepseek.com` and uses eino-ext's DeepSeek model implementation.
- `deepseek` accepts `option path <path>`, `option response_format_type <text|json_object>`, and DeepSeek chat tuning options such as `max_tokens`, `temperature`, `top_p`, `presence_penalty`, `frequency_penalty`, `log_probs`, and `top_log_probs`.
- `zhipu` defaults to `https://open.bigmodel.cn/api/paas/v4` and speaks through Zhipu BigModel's OpenAI-compatible API.
- `zhipu` accepts `option thinking_type <disabled|enabled|none>`; the provider default is `disabled` to keep standard OpenAI clients receiving visible `message.content`.
- `ollama` can be used without an API key.
- `option` values are parsed as strings in the Caddyfile.

### Routes

Static route syntax:

```caddy
route openai-chat {
	llm_api openai
	host api.example.com
	path_prefix /v1
	method POST
	require_virtual_key
	target provider openai-main
}
```

Supported route subdirectives:

- `llm_api <openai|anthropic>`
- `host <host>`
- `path_prefix <prefix>`
- `method <method> [more-methods...]`
- `require_virtual_key [true|false]`
- `target provider <provider-id>`

Static Caddyfile routes only support direct-provider mode. The request `model` is forwarded upstream as the provider model name.

### Virtual Keys

`agw` does not support static `virtualkey` declarations in the Caddyfile.

If a route sets `require_virtual_key`, create virtual keys through the Admin API after startup. The gateway persists them in the config store and generates the bearer `key` value at creation time.

## Runtime Request Flow

1. `agent_route_dispatcher` receives the HTTP request.
2. The dispatcher finds the best matching route by host, path prefix, and method.
3. The matched route's `llm_api` selects the protocol handler.
4. The route manager lists static routes plus persisted routes from SQLite, caching persisted routes as it loads them.
5. If required, the virtual key is extracted from `x-api-key` or `Authorization: Bearer`.
6. The protocol handler converts the request into the internal provider request.
7. If the route configures `target_policy.provider_target.provider_id`, the route runs in direct-provider mode. The request is forwarded to that provider and the request model is treated as the upstream model name.
8. Otherwise the route runs in logical-model mode, the model catalog resolves the logical model to one concrete provider/model binding, and the gateway rewrites the upstream request model.
9. The provider sends the upstream request and the protocol handler translates the response.

Supported request endpoints today:

- OpenAI-compatible:
  - `POST /v1/chat/completions`
  - `/v1/models` and `/v1/embeddings` are recognized by the path matcher, but the serving path is not fully implemented for those APIs yet.
- Anthropic-compatible:
  - `POST /v1/messages`
  - `POST /v1/messages/count_tokens` returns `501 not implemented`.

## Dynamic Configuration

Configuration comes from two places:

- static Caddyfile config under `agent_gateway`
- persisted SQLite records managed through the Admin API
- optional standalone static bundle YAML loaded with `agwd --static-config`

Static providers and routes are loaded during startup. Persisted provider, managed model, route, credential, and virtual key records can be changed through the Admin API without rebuilding the binaries.

Model catalog Admin API families:

- `GET /admin/models/providers/{provider_id}/discovered`
- `POST /admin/models/providers/{provider_id}/refresh`
- `GET /admin/models/managed`
- `PUT /admin/models/managed/{provider_id}/{upstream_model}`
- `GET /admin/models/logical`

Static records are exposed through Admin API list/read responses with source/read-only metadata where applicable. Attempts to mutate static providers or routes return conflict errors.

For the standalone daemon, static bundle YAML uses the same read-only semantics as Caddyfile-owned objects:

`--static-config` does not support `virtualKeys`. Create virtual keys through the Admin API after startup.
`--static-config` routes must use `target_policy.provider_target.provider_id`. Logical-model route policies are rejected in static startup config.

```bash
./agwd --config-store ./data/configstore.db \
  --static-config ./examples/gateway.static.minimal.yaml
```

## Admin API

All endpoints below are under the path where `agent_gateway_admin` is mounted. Except for health and login, they require `Authorization: Bearer $TOKEN`.

### Public and Session

- `GET /admin/health`
- `POST /admin/auth/login`
- `POST /admin/auth/logout`
- `GET /admin/auth/me`

### Providers

- `GET /admin/provider_types`
- `POST /admin/provider_types/{provider_type}/enable`
- `POST /admin/provider_types/{provider_type}/disable`
- `GET /admin/llm_api_handler_types`
- `GET /admin/providers`
- `POST /admin/providers`
- `GET /admin/providers/{id}`
- `PUT /admin/providers/{id}`
- `POST /admin/providers/{id}/enable`
- `POST /admin/providers/{id}/disable`
- `DELETE /admin/providers/{id}`

Create a provider:

```bash
curl -X POST http://localhost:8019/admin/providers \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "openrouter-main",
    "provider_type": "openrouter",
    "api_key": "sk-or-...",
    "base_url": "https://openrouter.ai/api/v1",
    "default_model": "openai/gpt-4o-mini",
    "network": {
      "request_timeout_seconds": 120,
      "max_retries": 3,
      "max_idle_connections": 100,
      "max_idle_connections_per_host": 20,
      "idle_keep_alive_timeout_seconds": 90
    }
  }'
```

### Routes

- `GET /admin/routes`
- `POST /admin/routes`
- `GET /admin/routes/{id}`
- `PUT /admin/routes/{id}`
- `POST /admin/routes/{id}/enable`
- `POST /admin/routes/{id}/disable`
- `DELETE /admin/routes/{id}`

Create a route:

```bash
curl -X POST http://localhost:8019/admin/routes \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "chat-prod",
    "llm_api": "openai",
    "match": {
      "path_prefix": "/",
      "methods": ["POST"]
    },
    "target_policy": {
      "provider_target": {
        "provider_id": "openrouter-main"
      }
    },
    "auth_policy": {
      "require_virtual_key": true
    }
  }'
```

`llm_api` and `target_policy` are required.

`created_at` and `updated_at` are server-managed fields. Omit them from `POST /admin/routes` and `PUT /admin/routes/{id}` request bodies.

When `target_policy.provider_target.provider_id` is present, the route is resolved in direct-provider mode.

Logical-model routes that use `target_policy.model_targets` remain supported through dynamic route management and config-store bundle workflows such as `agwctl gateway apply`, but they are not accepted in Caddyfile routes or `agwd --static-config`.

### Virtual Keys

- `GET /admin/virtual_keys`
- `POST /admin/virtual_keys`
- `GET /admin/virtual_keys/{id}`
- `PUT /admin/virtual_keys/{id}`
- `POST /admin/virtual_keys/{id}/enable`
- `POST /admin/virtual_keys/{id}/disable`
- `DELETE /admin/virtual_keys/{id}`

Create a virtual key. The key value is generated by the gateway and returned in the response:

```bash
curl -X POST http://localhost:8019/admin/virtual_keys \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "demo-key",
    "tag": "demo-user",
    "allowed_route_ids": ["chat-prod"]
  }'
```

Example response:

```json
{
  "id": "demo-key",
  "key": "vk-...",
  "tag": "demo-user",
  "allowed_route_ids": ["chat-prod"],
  "created_at": "2026-05-13T03:00:00Z",
  "updated_at": "2026-05-13T03:00:00Z",
  "source": "store",
  "read_only": false
}
```

The `id` is the stable management identifier. The `key` is the bearer credential value clients must send on requests.
`created_at` and `updated_at` are server-managed fields. Omit them from `POST /admin/virtual_keys` and `PUT /admin/virtual_keys/{id}` request bodies.

For statically configured virtual keys, the same `GET /admin/virtual_keys/{id}` endpoint returns the generated `key` value after startup.

### Upstream Credentials

- `GET /admin/credentials`
- `POST /admin/credentials`
- `GET /admin/credentials/{credential_id}`
- `PUT /admin/credentials/{credential_id}`
- `DELETE /admin/credentials/{credential_id}`

Create an API-key credential:

```bash
curl -X POST http://localhost:8019/admin/credentials \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "provider_id": "openai-main",
    "label": "primary",
    "attributes": {
      "api_key": "sk-...",
      "base_url": "https://api.openai.com/v1",
      "priority": "10"
    }
  }'
```

Credential `created_at` and `updated_at` values are server-managed response fields. Do not send them in `POST /admin/credentials` or `PUT /admin/credentials/{credential_id}` request bodies.

### CLI Auth

- `GET /admin/cliauth/authenticators`
- `GET /admin/cliauth/authenticators/{authenticator_name}`
- `PUT /admin/cliauth/authenticators/{authenticator_name}`
- `POST /admin/cliauth/authenticators/{authenticator_name}/login`
- `GET /admin/cliauth/logins/{login_id}`

CLI auth login runs asynchronously on the server. The login endpoint returns `202 Accepted`; poll the status endpoint for completion.
Authenticator config set through the admin API is runtime-only. Disabling an authenticator or restarting the server resets it to factory defaults.
The `PUT` update endpoint accepts `enabled` and `config`. Use `{"enabled":true,"config":{}}` to keep factory defaults while enabling or refreshing the runtime authenticator config. The runtime authenticator is recreated from its factory defaults, then the provided config is applied.

Examples:

```sh
curl -X PUT http://localhost:8019/admin/cliauth/authenticators/codex \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  --data '{"enabled":true,"config":{}}'
```

```sh
curl -X PUT http://localhost:8019/admin/cliauth/authenticators/codex \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  --data '{"enabled":true,"config":{"callback_port":9002,"no_browser":true,"device_flow":true}}'
```

### Registered but Not Implemented

These endpoints currently return `501 not implemented`:

- MCP:
  - `GET /admin/mcp/clients`
  - `POST /admin/mcp/clients`
  - `GET /admin/mcp/clients/{id}`
  - `PUT /admin/mcp/clients/{id}`
  - `DELETE /admin/mcp/clients/{id}`
  - `GET /admin/mcp/clients/{id}/tools`
- Memory:
  - `GET /admin/memory/config`
  - `PUT /admin/memory/config`
  - `GET /admin/memory/search`
- Agents:
  - `GET /admin/agents`
  - `POST /admin/agents`
  - `GET /admin/agents/{id}`
  - `PUT /admin/agents/{id}`
  - `DELETE /admin/agents/{id}`
- Metrics:
  - `GET /admin/metrics`

## Caddy Integration Notes

The gateway admin handler does not expose Caddy server management endpoints.
If you need `/admin/caddy/*` operations for a Caddy-managed deployment, run the standalone `caddymgr` service and point the Web UI at that service. `caddymgr` keeps its own frontend session and proxies non-Caddy `/admin/*` calls back to this gateway, so Caddy reloads do not force the frontend to log in again.
Similarly, `agwctl caddy ...` talks to the Caddy admin API directly and does not use the gateway Admin API route table.

## Current Limits

- LLM routing is the primary working path.
- OpenAI chat completions and Anthropic messages are implemented for normal and streaming requests.
- Anthropic token counting returns `501`.
- OpenAI embeddings are not fully wired through the API handler.
- MCP, memory, metrics, and agent Admin API routes are placeholders.
- Memory backends and embedding adapters contain interfaces and stubs, but are not production-ready request-path features.
- Caddy server management is handled by the standalone `caddymgr` service, not by the gateway Admin API.

## Useful Commands

```bash
go test ./...
go test ./pkg/admin ./pkg/gateway ./pkg/dispatcher/...
go test ./pkg/llm/provider/... ./caddy/provider/...
```

```bash
./agw adapt --config ./Caddyfile
./agw run --config ./Caddyfile
```
