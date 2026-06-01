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

There are two ways to get started: static Caddyfile config, or a minimal Caddyfile with all objects managed dynamically via `agwctl`.

### Option 1: Static Caddyfile (no VirtualKey required)

Declare the provider and route directly in the Caddyfile with `require_virtual_key false`. No VirtualKey is needed to call the gateway.

Create a `Caddyfile`:

```caddy
{
	admin localhost:2019

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
		llm_api cc
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

Send a request directly — no API key required:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4.1","messages":[{"role":"user","content":"hello"}]}'
```

### Option 2: Minimal Caddyfile + dynamic config via agwctl

Keep the Caddyfile minimal and declare all providers, routes, and VirtualKeys in a bundle YAML applied at runtime via `agwctl gateway apply`.

Create a minimal `Caddyfile` (no providers or routes):

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
		llm_api cc
		mcp
	}
}
```

Generate the hash and run the gateway:

```bash
./agw hash-password --plaintext 'your-password'
OPENAI_API_KEY=sk-... ./agw run --config ./Caddyfile
```

Create a bundle file `gateway.bundle.yaml`:

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

Set the admin credentials as environment variables, then apply the bundle:

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

Retrieve the generated VirtualKey value and verify with `agwctl chat`:

```bash
AGW_API_KEY=$(./agwctl gateway virtualkey get test-key | jq -r '.key')
./agwctl chat "hello" --api-key "$AGW_API_KEY"
```

`agwctl chat` defaults to the OpenAI API at `http://127.0.0.1:8080/v1`. Pass `--stream` for SSE streaming, `--api anthropic` for the Anthropic-compatible surface, or `--api cc` for the Claude Code CLI-compatible surface.

The `AGW_ADMIN_ADDR` environment variable sets the admin API address (default: `http://localhost:8019`). Run `agwctl gateway export` to dump the current gateway state as a bundle YAML.

Provider `options.compact` selects compatibility request shaping. Supported values are `cc`, `codex`, and `none`; providers ignore modes they do not implement.

## MCP Quick Start

The MCP gateway uses the same minimal Caddyfile as LLM Option 2. Enable `mcp` in the dispatcher, then apply an MCP bundle.

Create or reuse the minimal Caddyfile from Option 2 above (the `mcp` directive is already present in `agent_route_dispatcher`). Run the gateway, then create a bundle file `gateway.bundle.mcp.yaml`.

**Streamable HTTP upstream** (remote MCP server over HTTP):

```yaml
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
mcpServices:
  - id: mcp-main
    name: Main MCP Service
    transport: streamable_http
    url: ${MCP_SERVICE_URL}
mcpRoutes:
  - service_id: mcp-main
    match_policy:
      path_prefix: /mcp
    auth_policy:
      require_virtual_key: true
virtualKeys:
  - id: mcp-key
    allowed_route_ids:
      - mcp:mcp-main:/mcp
```

**stdio upstream** (local subprocess, e.g. `@modelcontextprotocol/server-filesystem`):

```yaml
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
mcpServices:
  - id: mcp-fs
    name: Filesystem MCP Service
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
mcpRoutes:
  - service_id: mcp-fs
    match_policy:
      path_prefix: /mcp
    auth_policy:
      require_virtual_key: true
virtualKeys:
  - id: mcp-key
    allowed_route_ids:
      - mcp:mcp-fs:/mcp
```

Apply and verify:

```bash
export AGW_ADMIN_USER=admin
export AGW_ADMIN_PASSWORD=your-password

MCP_SERVICE_URL=https://your-mcp-server/mcp ./agwctl gateway apply -f gateway.bundle.mcp.yaml

# Discover tools via admin API
./agwctl gateway mcp-service tools mcp-main

# Retrieve the VirtualKey and send an MCP request
MCP_API_KEY=$(./agwctl gateway virtualkey get mcp-key | jq -r '.key')

curl -s http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $MCP_API_KEY" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

MCP route IDs are auto-generated as `mcp:<service_id>:<path_prefix>` when `id` is omitted. See [docs/getting-started/quickstart-mcp.md](docs/getting-started/quickstart-mcp.md) for the full walkthrough.

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
go test ./pkg/llm/provider/...
```

```bash
./agw adapt --config ./Caddyfile
./agw run --config ./Caddyfile
```
