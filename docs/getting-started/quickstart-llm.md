# Quick Start: LLM Gateway

This guide runs the Caddy-based gateway with one provider, one route, and one successful OpenAI-compatible request. Two paths are shown: static Caddyfile config and fully dynamic config via `agwctl`.

## Prerequisites

- Go toolchain installed
- One upstream API key such as `OPENAI_API_KEY`
- `jq` installed (Option 2 only)

## 1. Build

```bash
make build
```

This produces three binaries: `agw`, `agwd`, and `agwctl`.

---

## Option 1: Static Caddyfile

Declare the provider and route in the Caddyfile with `require_virtual_key false`. No VirtualKey is needed to call the gateway.

### 2a. Create the Caddyfile

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
			require_virtual_key false
			target provider openai-main
		}
	}
}

http://localhost:8019 {
	route /admin/* {
		agent_gateway_admin {
			admin_user admin
			admin_password_hash <bcrypt-hash>
		}
	}
}

http://127.0.0.1:8080 {
	agent_route_dispatcher {
		llm_api openai
		llm_api anthropic
		mcp
	}
}
```

### 3a. Generate the Admin Password Hash

```bash
./agw hash-password --plaintext 'your-password'
```

Replace `<bcrypt-hash>` in the Caddyfile with the generated value.

### 4a. Run the Gateway

```bash
OPENAI_API_KEY=sk-... ./agw run --config ./Caddyfile
```

Validate the config before startup with:

```bash
./agw adapt --config ./Caddyfile
```

### 5a. Send a Request

No API key header is required because `require_virtual_key false` was set on the route:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4.1","messages":[{"role":"user","content":"hello"}]}'
```

---

## Option 2: Minimal Caddyfile + Dynamic Config via agwctl

Keep the Caddyfile minimal and manage all providers, routes, and VirtualKeys through a bundle YAML applied at runtime via `agwctl gateway apply`.

### 2b. Create a Minimal Caddyfile

No providers or routes are declared here — they will be applied dynamically:

```caddy
{
	admin localhost:2019

	agent_gateway {
		config_store sqlite {
			path ./data/configstore.db
		}
	}
}

http://localhost:8019 {
	route /admin/* {
		agent_gateway_admin {
			admin_user admin
			admin_password_hash <bcrypt-hash>
		}
	}
}

http://127.0.0.1:8080 {
	agent_route_dispatcher {
		llm_api openai
		llm_api anthropic
		mcp
	}
}
```

### 3b. Generate the Admin Password Hash

```bash
./agw hash-password --plaintext 'your-password'
```

Replace `<bcrypt-hash>` in the Caddyfile with the generated value.

### 4b. Run the Gateway

```bash
OPENAI_API_KEY=sk-... ./agw run --config ./Caddyfile
```

### 5b. Create a Bundle File

Create `gateway.bundle.yaml`:

```yaml
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
    api_key: ${OPENAI_API_KEY}
    default_model: gpt-4.1
llmRoutes:
  - id: openai-chat
    protocol: openai
    match_policy:
      path_prefix: /
    auth_policy:
      require_virtual_key: true
    target_policy:
      provider_target:
        provider_id: openai-main
virtualKeys:
  - id: test-key
    allowed_route_ids:
      - openai-chat
```

The `${OPENAI_API_KEY}` placeholder is expanded from the environment when `agwctl` runs.

### 6b. Apply the Bundle

Set the admin credentials as environment variables, then apply:

```bash
export AGW_ADMIN_USER=admin
export AGW_ADMIN_PASSWORD=your-password

OPENAI_API_KEY=sk-... ./agwctl gateway apply -f gateway.bundle.yaml
```

`apply` is idempotent — it creates objects that do not exist, updates those that have changed, and skips unchanged ones:

```
gateway apply: gateway.bundle.yaml
  create provider openai-main
  create llm_route openai-chat
  create virtual_key test-key
summary: create=3 update=0 skip=0 error=0
```

The `AGW_ADMIN_ADDR` environment variable sets the admin API address (default: `http://localhost:8019`).

### 7b. Verify with agwctl chat

The gateway generates the actual bearer key when the VirtualKey is created. Retrieve it and send a test request:

```bash
AGW_API_KEY=$(./agwctl gateway virtualkey get test-key | jq -r '.key')
./agwctl chat "hello" --api-key "$AGW_API_KEY"
```

`agwctl chat` defaults to the OpenAI API at `http://127.0.0.1:8080/v1`. Additional options:

- `--stream`: use SSE streaming
- `--api anthropic`: switch to the Anthropic-compatible surface
- `--model <name>`: override the model name

---

## Notes

- In both options the admin API is served at `http://localhost:8019`. The LLM API is at `http://127.0.0.1:8080`.
- Admin sessions are in memory. Restarting the gateway invalidates existing tokens.
- In Option 2, the VirtualKey can be sent as `Authorization: Bearer <key>` or `x-api-key: <key>`.
- Run `agwctl gateway export` to dump the current gateway state as a bundle YAML.

## Next

- [admin-auth.md](../guides/admin-auth.md): Admin API auth and session behavior
- [runtime-modes.md](../reference/runtime-modes.md): `agw`, `agwd`, and `agwctl`
- [caddyfile-reference.md](../reference/caddyfile-reference.md): full Caddyfile config reference
- `quickstart-mcp.md`: MCP gateway quick start (planned)
