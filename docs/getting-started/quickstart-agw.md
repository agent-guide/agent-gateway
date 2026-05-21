# Quick Start With `agw`

This guide gets the Caddy-based gateway runtime running with one provider, one route, one VirtualKey, and one successful OpenAI-compatible request.

## Prerequisites

- Go toolchain installed
- `jq` installed for the shell examples
- one upstream API key such as `OPENAI_API_KEY`

## 1. Build

```bash
make build
```

This builds:

- `agw`
- `agwd`
- `agwctl`

## 2. Create A Minimal `Caddyfile`

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
			protocol openai
			path_prefix /
			require_virtual_key
			target provider openai-main
		}
	}
}

http://127.0.0.1:8080 {
	route /admin/* {
		agent_gateway_admin {
			admin_user admin
			admin_password_hash <bcrypt-hash>
		}
	}

	agent_route_dispatcher {
		llm_api openai
		llm_api anthropic
		mcp
	}
}
```

## 3. Generate The Admin Password Hash

```bash
./agw hash-password --plaintext 'your-password'
```

Replace `<bcrypt-hash>` in the `Caddyfile` with the generated value.

## 4. Run The Gateway

```bash
OPENAI_API_KEY=sk-... ./agw run --config ./Caddyfile
```

You can validate the config before startup:

```bash
./agw adapt --config ./Caddyfile
```

## 5. Log In To The Admin API

```bash
TOKEN=$(
  curl -s -X POST http://localhost:2019/admin/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"your-password"}' |
    jq -r '.token'
)
```

Admin sessions are in memory. Restarting the service invalidates existing tokens.

## 6. Create A VirtualKey

```bash
AGW_API_KEY=$(
  curl -s -X POST http://localhost:2019/admin/virtual_keys \
    -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d '{"id":"test-key","allowed_route_ids":["openai-chat"]}' |
    jq -r '.key'
)
```

The gateway generates the actual bearer `key` value when the VirtualKey is created.

## 7. Send Your First Request

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $AGW_API_KEY" \
  -d '{
    "model": "gpt-4.1",
    "messages": [{"role": "user", "content": "hello"}]
  }'
```

You can send the VirtualKey using either of these headers:

- `Authorization: Bearer <key>`
- `x-api-key: <key>`

## Next

- [../guides/admin-auth.md](../guides/admin-auth.md): Admin API auth and session behavior
- [../reference/runtime-modes.md](../reference/runtime-modes.md): `agw`, `agwd`, and `agwctl`
- [../reference/caddyfile-reference.md](../reference/caddyfile-reference.md): full Caddyfile config reference
