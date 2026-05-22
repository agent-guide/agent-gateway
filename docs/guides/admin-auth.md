# Admin API Auth

This guide covers how the gateway Admin API is exposed, how login works, and how to use the resulting session token.

## Admin Handler

The Admin API is mounted with `agent_gateway_admin`.

Example:

```caddy
http://localhost:2019 {
	route /admin/* {
		agent_gateway_admin {
			admin_user admin
			admin_password_hash <bcrypt-hash>
		}
	}
}
```

Protected Admin API routes require:

1. an `admin_user`
2. an `admin_password_hash`
3. a session token from `POST /admin/auth/login`

Generate the bcrypt hash with Caddy:

```bash
./agw hash-password --plaintext 'your-password'
```

## Login

```bash
TOKEN=$(
  curl -s -X POST http://localhost:2019/admin/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"your-password"}' |
    jq -r '.token'
)
```

The login endpoint returns a session token used as:

```bash
-H "Authorization: Bearer $TOKEN"
```

## Session Lifecycle

- Admin sessions are stored in memory
- restarting the service invalidates existing tokens
- except for health and login, Admin API routes require a valid bearer session token

Useful session endpoints:

- `GET /admin/health`
- `POST /admin/auth/login`
- `POST /admin/auth/logout`
- `GET /admin/auth/me`

## First Authenticated Call

Create a VirtualKey after login:

```bash
curl -X POST http://localhost:2019/admin/virtual_keys \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "demo-key",
    "allowed_route_ids": ["openai-chat"]
  }'
```

Example response:

```json
{
  "id": "demo-key",
  "key": "vk-...",
  "allowed_route_ids": ["openai-chat"]
}
```

The `id` is the management identifier. The `key` is the bearer value that clients send on request traffic.

## Related Docs

- [../getting-started/quickstart-llm.md](../getting-started/quickstart-llm.md)
- [../reference/admin-api-reference.md](../reference/admin-api-reference.md)
