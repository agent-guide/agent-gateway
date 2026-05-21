# agent-gateway

`agent-gateway` is an AI gateway for LLM and MCP workloads. It provides OpenAI-compatible and Anthropic-compatible ingress, route-based provider dispatch, VirtualKey authentication, dynamic config backed by SQLite, and Admin APIs for gateway operations.

This repository builds three binaries:

- `agw`: Caddy-based gateway runtime
- `agwd`: standalone gateway daemon
- `agwctl`: management CLI for gateway and local CLI auth flows

## What It Does

- expose OpenAI-compatible and Anthropic-compatible HTTP APIs
- route requests to direct providers or logical model targets
- manage providers, routes, VirtualKeys, credentials, and CLI auth through an Admin API
- support MCP gateway routing, discovery, execution, and runtime inspection
- run with either a Caddyfile-based runtime or a standalone daemon with a config store

## Quick Start

Build the binaries:

```bash
make build
```

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

Generate the admin password hash:

```bash
./agw hash-password --plaintext 'your-password'
```

Run the gateway:

```bash
OPENAI_API_KEY=sk-... ./agw run --config ./Caddyfile
```

Log in to the Admin API and create a VirtualKey:

```bash
TOKEN=$(
  curl -s -X POST http://localhost:2019/admin/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"your-password"}' |
    jq -r '.token'
)

AGW_API_KEY=$(
  curl -s -X POST http://localhost:2019/admin/virtual_keys \
    -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d '{"id":"test-key","allowed_route_ids":["openai-chat"]}' |
    jq -r '.key'
)
```

Send your first request:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $AGW_API_KEY" \
  -d '{
    "model": "gpt-4.1",
    "messages": [{"role": "user", "content": "hello"}]
  }'
```

The gateway accepts a VirtualKey from either `Authorization: Bearer <key>` or `x-api-key: <key>`.

## Runtimes

- `agw` uses a Caddyfile plus the shared config store
- `agwd` runs as a standalone daemon with `--config-store` and optional `--static-config`
- `agwctl` talks to the gateway Admin API, the Caddy admin API, or local CLI auth flows depending on the command

See [docs/README.md](docs/README.md) for runtime-specific guides and references.

## Documentation

- [docs/README.md](docs/README.md): documentation index and split plan
- [docs/architecture/architecture-overview.md](docs/architecture/architecture-overview.md): current architecture overview
- [docs/architecture/mcp-architecture.md](docs/architecture/mcp-architecture.md): MCP gateway architecture
- [docs/architecture/configstore-architecture.md](docs/architecture/configstore-architecture.md): config store architecture
- [docs/design/gateway-bundle-yaml.md](docs/design/gateway-bundle-yaml.md): bundle YAML design

## Current Limits

- OpenAI-compatible chat and Anthropic-compatible messages are the primary mature LLM paths
- OpenAI embeddings and Anthropic token counting are not fully implemented
- MCP is active in the dispatcher and Admin API surface, but some adjacent subsystems are still evolving
- memory, agents, and metrics Admin API families still contain `501 Not Implemented` endpoints

## Development

Useful commands:

```bash
go test ./...
go test ./pkg/admin ./pkg/gateway ./pkg/dispatcher/...
go test ./pkg/llm/provider/... ./caddy/provider/...
```

```bash
./agw adapt --config ./Caddyfile
./agw run --config ./Caddyfile
```
