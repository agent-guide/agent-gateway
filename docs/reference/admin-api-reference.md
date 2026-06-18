# Admin API Reference

All endpoints below are under the path where `agent_gateway_admin` is mounted. Except for health and login, they require `Authorization: Bearer $TOKEN`.

## Public And Session

- `GET /admin/health`
- `POST /admin/auth/login`
- `POST /admin/auth/logout`
- `GET /admin/auth/me`

## Providers

- `GET /admin/provider_types`
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

Provider notes:

- `id` and `provider_type` are required
- `created_at` and `updated_at` are server-managed fields

## LLM Routes

- `GET /admin/llm/routes`
- `POST /admin/llm/routes`
- `GET /admin/llm/routes/{id}`
- `PUT /admin/llm/routes/{id}`
- `POST /admin/llm/routes/{id}/enable`
- `POST /admin/llm/routes/{id}/disable`
- `DELETE /admin/llm/routes/{id}`

Create a route:

```bash
curl -X POST http://localhost:8019/admin/llm/routes \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "chat-prod",
    "protocol": "openai",
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

Route notes:

- `protocol` and `target_policy` are required
- `created_at` and `updated_at` are server-managed fields
- when `target_policy.provider_target.provider_id` is present, the route runs in direct-provider mode
- logical-model routes using `target_policy.model_targets` remain supported through dynamic route management and bundle workflows, but not in Caddyfile routes or `agwd --static-config`

## VirtualKeys

- `GET /admin/virtual_keys`
- `POST /admin/virtual_keys`
- `GET /admin/virtual_keys/{id}`
- `PUT /admin/virtual_keys/{id}`
- `POST /admin/virtual_keys/{id}/enable`
- `POST /admin/virtual_keys/{id}/disable`
- `DELETE /admin/virtual_keys/{id}`

Create a VirtualKey:

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

VirtualKey notes:

- `id` is the stable management identifier
- `key` is the bearer credential value clients must send on requests
- `created_at` and `updated_at` are server-managed fields

## Upstream Credentials

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

Credential notes:

- `created_at` and `updated_at` are server-managed response fields
- do not send them in `POST` or `PUT` bodies

## Models

- `GET /admin/models/providers/{provider_id}/discovered`
- `POST /admin/models/providers/{provider_id}/refresh`
- `GET /admin/models/managed`
- `PUT /admin/models/managed/{provider_id}/{upstream_model}`
- `GET /admin/models/logical`

## CLI Auth

- `GET /admin/cliauth/authenticators`
- `GET /admin/cliauth/authenticators/{authenticator_name}`
- `PUT /admin/cliauth/authenticators/{authenticator_name}`
- `POST /admin/cliauth/authenticators/{authenticator_name}/login`
- `GET /admin/cliauth/logins/{login_id}`

CLI auth behavior:

- login runs asynchronously and returns `202 Accepted`
- poll the login status endpoint for completion
- the login request body must include `provider_id`
- authenticator config set through the admin API is runtime-only
- disabling an authenticator or restarting the server resets it to factory defaults

Examples:

```bash
curl -X PUT http://localhost:8019/admin/cliauth/authenticators/codex \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  --data '{"enabled":true,"config":{}}'
```

```bash
curl -X PUT http://localhost:8019/admin/cliauth/authenticators/codex \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  --data '{"enabled":true,"config":{"callback_port":9002,"no_browser":true,"device_flow":true}}'
```

```bash
curl -X POST http://localhost:8019/admin/cliauth/authenticators/codex/login \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  --data '{"provider_id":"openai-main","scope":"type:openai"}'
```

## MCP Admin Families

Implemented MCP admin families include:

- MCP services
- MCP routes
- MCP discovery and execution endpoints
- MCP dispatcher runtime inspection endpoints

## ACP Admin Families

Implemented ACP admin families include:

- `GET /admin/acp/services`
- `POST /admin/acp/services`
- `GET /admin/acp/services/{id}`
- `PUT /admin/acp/services/{id}`
- `DELETE /admin/acp/services/{id}`
- `GET /admin/acp/services/{id}/sessions`
- `GET /admin/acp/services/{id}/sessions/{session_id}/transcript`
- `GET /admin/acp/routes`
- `POST /admin/acp/routes`
- `GET /admin/acp/routes/{id}`
- `PUT /admin/acp/routes/{id}`
- `DELETE /admin/acp/routes/{id}`
- `GET /admin/acp/runtime`
- `GET /admin/acp/runtime/inflight`
- `DELETE /admin/acp/runtime/threads/{service_id}/{thread_id}`
- `POST /admin/acp/runtime/permissions/{request_id}`

The dispatcher also exposes consumer-facing route-scoped ACP session endpoints
under `/<acp-route>/sessions` and
`/<acp-route>/sessions/{session_id}/transcript`; those are not Admin API
families because they resolve the service from the matched route and use the
route's VirtualKey policy.

See [acp-api.md](acp-api.md) for request and response shapes.

## Stubbed Families

These families still contain `501 Not Implemented` endpoints:

- memory
- agents
- metrics

## Caddy Integration Notes

The gateway admin handler does not expose Caddy server management endpoints.

- if you need `/admin/caddy/*` operations for a Caddy-managed deployment, run the standalone `caddymgr` service and point the Web UI at that service
- `agwctl caddy ...` talks to the Caddy admin API directly and does not use the gateway Admin API route table

## Related Docs

- [../guides/admin-auth.md](../guides/admin-auth.md)
- [runtime-modes.md](runtime-modes.md)
- [caddyfile-reference.md](caddyfile-reference.md)
