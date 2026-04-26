# caddy-agent-gateway

`caddy-agent-gateway` is a Caddy-native AI gateway for LLM and agent workloads. It is built as a custom Caddy binary and provides:

- OpenAI-compatible and Anthropic-compatible HTTP APIs
- route-based dispatch to upstream providers
- static Caddyfile configuration plus SQLite-backed dynamic configuration
- admin APIs for providers, routes, virtual keys, upstream credentials, CLI auth, and Caddy server management
- early MCP, memory, metrics, and agent endpoint scaffolding

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

- `cmd/` - custom Caddy entrypoint and module imports
- `gateway/` - `agent_gateway` app, runtime managers, route selection, provider resolution, virtual key validation
- `api/` - `agent_route_dispatcher` and protocol handlers
- `admin/` - `agent_gateway_admin`, Admin API routes, session auth, Caddy management proxy
- `llm/provider/` - provider interface and built-in provider implementations
- `cliauth/` - CLI login authenticators and manager
- `llm/credentialmgr/` - upstream credential registration and scheduling state
- `configstore/sqlite/` - SQLite-backed persisted configuration
- `llm/mcp/`, `llm/memory/`, `llm/agent/` - early integration scaffolding

## Build

```bash
go build -o caddy-agent-gateway ./cmd/main.go
```

or:

```bash
make build
```

The binary includes Caddy standard modules, the gateway app, the admin handler, LLM API handlers, built-in providers, and CLI authenticators.

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

		virtualkey test-key {
			tag local-test
			name "Local test key"
			allowed_route openai-chat
		}

		route openai-chat {
			llm_api openai
			path_prefix /
			require_virtual_key
			allowed_model gpt-4.1
			target provider openai-main
		}
	}
}

http://127.0.0.1:8082 {
	agent_route_dispatcher {
		llm_api openai
		llm_api anthropic
	}
}
```

Run the gateway:

```bash
OPENAI_API_KEY=sk-... ./caddy-agent-gateway run --config ./Caddyfile
```

Call the OpenAI-compatible endpoint:

```bash
curl http://127.0.0.1:8082/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer test-key' \
  -d '{
    "model": "gpt-4.1",
    "messages": [{"role": "user", "content": "hello"}]
  }'
```

The VirtualKey may be sent as either `x-api-key: <key>` or `Authorization: Bearer <key>`.

The Python OpenAI SDK example uses `http://127.0.0.1:8082/v1`, the `test-key` VirtualKey, and `gpt-4.1` by default:

```bash
python3 -m pip install openai
python3 examples/test_openai_client.py
python3 examples/test_openai_client.py --stream
```

Override example defaults with `AGENT_GATEWAY_BASE_URL`, `AGENT_GATEWAY_API_KEY`, `AGENT_GATEWAY_MODEL`, or CLI flags.

## Admin API Setup

Admin routes are mounted with `agent_gateway_admin`. Protected Admin API routes require:

1. an `admin_user`
2. an `admin_password_hash` bcrypt hash
3. a session token from `POST /admin/auth/login`

Example:

```caddy
http://127.0.0.1:8081 {
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
./caddy-agent-gateway hash-password --plaintext 'your-password'
```

Log in:

```bash
TOKEN=$(
  curl -s -X POST http://127.0.0.1:8081/admin/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"your-password"}' |
    jq -r '.token'
)
```

Admin sessions are in memory. Restarting the service invalidates existing tokens.

## Caddyfile Configuration

The gateway is configured in the global `agent_gateway` block:

```caddy
{
	agent_gateway {
		config_store sqlite { ... }
		provider <provider-id> { ... }
		authenticator <name> { ... }
		virtualkey <key> { ... }
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

If `path` is omitted, the store defaults to Caddy's app data directory under `caddy-agent-gateway/configstore.db`.

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
	allowed_model gpt-4.1 gpt-4.1-mini
	target provider openai-main 1
}
```

Supported route subdirectives:

- `llm_api <openai|anthropic>`
- `host <host>`
- `path_prefix <prefix>`
- `method <method> [more-methods...]`
- `require_virtual_key [true|false]`
- `allowed_model <model> [more-models...]`
- `target provider <provider-id> [weight]`

Static Caddyfile targets are weighted targets. The Go route model and Admin API also contain fields for failover, conditional targets, selection strategy, retry, fallback, quota, and rate limits, but not every field is exposed in Caddyfile syntax.

### Virtual Keys

```caddy
virtualkey test-key {
	tag local-test
	name "Local test key"
	description "Used by local examples"
	disabled false
	allowed_route openai-chat
	status_message "active"
	expires_at 2027-01-01T00:00:00Z
}
```

Supported subdirectives:

- `tag <tag>`
- `name <display-name>`
- `description <text>`
- `disabled [true|false]`
- `allowed_route <route-id> [more-route-ids...]`
- `status_message <text>`
- `expires_at <rfc3339>`

If `allowed_route` is omitted, the key can be used on any route that requires virtual key authentication.

## Runtime Request Flow

1. `agent_route_dispatcher` receives the HTTP request.
2. The dispatcher finds the best matching route by host, path prefix, and method.
3. The matched route's `llm_api` selects the protocol handler.
4. The route manager lists static routes plus persisted routes from SQLite, caching persisted routes as it loads them.
5. If required, the virtual key is extracted from `x-api-key` or `Authorization: Bearer`.
6. The protocol handler converts the request into the internal provider request.
7. The route target selector chooses an upstream provider.
8. The provider sends the upstream request and the protocol handler translates the response.

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

Static providers, routes, virtual keys, and authenticators are loaded during provisioning. Persisted provider, route, credential, and virtual key records can be changed through the Admin API without rebuilding the Caddy binary.

Static records are exposed through Admin API list/read responses with source/read-only metadata where applicable. Attempts to mutate static providers or routes return conflict errors.

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
- `POST /admin/llm_api_handler_types/{llm_api_handler_type}/enable`
- `POST /admin/llm_api_handler_types/{llm_api_handler_type}/disable`
- `GET /admin/providers`
- `POST /admin/providers`
- `GET /admin/providers/{id}`
- `PUT /admin/providers/{id}`
- `POST /admin/providers/{id}/enable`
- `POST /admin/providers/{id}/disable`
- `DELETE /admin/providers/{id}`

Create a provider:

```bash
curl -X POST http://127.0.0.1:8081/admin/providers \
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
curl -X POST http://127.0.0.1:8081/admin/routes \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "chat-prod",
    "llm_api": "openai",
    "match": {
      "path_prefix": "/",
      "methods": ["POST"]
    },
    "targets": [
      {
        "provider_id": "openrouter-main",
        "mode": "weighted",
        "weight": 1
      }
    ],
    "policy": {
      "auth": {
        "require_virtual_key": true
      },
      "allowed_models": ["openai/gpt-4o-mini"]
    }
  }'
```

### Virtual Keys

- `GET /admin/virtual_keys`
- `POST /admin/virtual_keys`
- `GET /admin/virtual_keys/{key}`
- `PUT /admin/virtual_keys/{key}`
- `POST /admin/virtual_keys/{key}/enable`
- `POST /admin/virtual_keys/{key}/disable`
- `DELETE /admin/virtual_keys/{key}`

Create a virtual key. The key value is generated by the gateway and returned in the response:

```bash
curl -X POST http://127.0.0.1:8081/admin/virtual_keys \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "tag": "demo-user",
    "name": "demo key",
    "allowed_route_ids": ["chat-prod"]
  }'
```

### Upstream Credentials

- `GET /admin/credentials`
- `POST /admin/credentials`
- `GET /admin/credentials/{credential_id}`
- `PUT /admin/credentials/{credential_id}`
- `DELETE /admin/credentials/{credential_id}`

Create an API-key credential:

```bash
curl -X POST http://127.0.0.1:8081/admin/credentials \
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

### CLI Auth

- `GET /admin/cliauth/authenticators`
- `GET /admin/cliauth/authenticators/{authenticator_name}`
- `PUT /admin/cliauth/authenticators/{authenticator_name}`
- `POST /admin/cliauth/authenticators/{authenticator_name}/enable`
- `POST /admin/cliauth/authenticators/{authenticator_name}/disable`
- `POST /admin/cliauth/authenticators/{authenticator_name}/login`
- `GET /admin/cliauth/logins/{login_id}`

CLI auth login runs asynchronously on the server. The login endpoint returns `202 Accepted`; poll the status endpoint for completion.
Authenticator config set through the admin API is runtime-only. Disabling an authenticator or restarting the server resets it to factory defaults.
The enable endpoint requires a JSON body with `config`. Use `{"config":{}}` to keep factory defaults. The runtime authenticator is recreated from its factory defaults, then the provided config is applied.

Examples:

```sh
curl -X POST http://localhost:2019/admin/cliauth/authenticators/codex/enable \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  --data '{"config":{}}'
```

```sh
curl -X POST http://localhost:2019/admin/cliauth/authenticators/codex/enable \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  --data '{"config":{"callback_port":9002,"no_browser":true,"device_flow":true}}'
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

## Caddy Server Management

The gateway admin handler does not expose Caddy server management endpoints.
Run the standalone `caddymgr` service for `/admin/caddy/*` operations and point
the Web UI at that service. `caddymgr` keeps its own frontend session and proxies
non-Caddy `/admin/*` calls back to this gateway, so Caddy reloads do not force
the frontend to log in again.

## Current Limits

- LLM routing is the primary working path.
- OpenAI chat completions and Anthropic messages are implemented for normal and streaming requests.
- Anthropic token counting returns `501`.
- OpenAI embeddings are not fully wired through the API handler.
- MCP, memory, metrics, and agent Admin API routes are placeholders.
- Memory backends and embedding adapters contain interfaces and stubs, but are not production-ready request-path features.
- Caddy server management is handled by the standalone `caddymgr` service, not this gateway module.

## Useful Commands

```bash
go test ./...
go test ./admin ./gateway ./api
go test ./llm/provider/...
```

```bash
./caddy-agent-gateway adapt --config ./Caddyfile
./caddy-agent-gateway run --config ./Caddyfile
```
