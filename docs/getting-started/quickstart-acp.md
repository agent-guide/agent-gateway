# Quick Start: ACP Gateway

This guide runs a Codex ACP agent behind `agent-gateway`, applies service and
route config with `agwctl`, sends one streamed turn, and inspects the runtime.

## Prerequisites

- Go toolchain installed
- `jq` installed
- Codex auth already configured
- `codex-acp` on `PATH`

Install the Codex ACP adapter if needed:

```bash
npm install -g @zed-industries/codex-acp
```

For opencode, install and authenticate `opencode`, then use
`agent_type: opencode` in the bundle.

## 1. Build

```bash
make build
```

## 2. Create A Minimal Caddyfile

ACP services and routes are applied dynamically, so the Caddyfile only needs the
gateway app, Admin API, and dispatcher with `acp` enabled:

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
		acp
	}
}
```

## 3. Generate The Admin Password Hash

```bash
./agw hash-password --plaintext 'your-password'
```

Replace `<bcrypt-hash>` in the Caddyfile.

## 4. Run The Gateway

```bash
./agw run --config ./Caddyfile
```

## 5. Create The Agent Working Directory

```bash
mkdir -p /tmp/acp-codex-test
```

## 6. Create A Bundle File

```yaml
# gateway.bundle.acp.yaml
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle

acpServices:
  - id: codex-main
    name: Codex
    agent_type: codex
    cwd: /tmp/acp-codex-test
    max_instances: 4
    permission_mode: auto_approve

acpRoutes:
  - id: acp-codex
    service_id: codex-main
    match_policy:
      path_prefix: /acp/codex
    auth_policy:
      require_virtual_key: true

virtualKeys:
  - id: acp-key
    allowed_route_ids:
      - acp-codex
```

Permission modes:

- `deny`: default; permission requests are rejected.
- `auto_approve`: allow-style permission options are selected automatically.
- `interactive`: permission requests are sent as `permission` SSE events and
  must be answered through the route or Admin API.

## 7. Apply The Bundle

```bash
export AGW_ADMIN_USER=admin
export AGW_ADMIN_PASSWORD=your-password

./agwctl gateway apply -f gateway.bundle.acp.yaml
```

Expected output:

```text
gateway apply: gateway.bundle.acp.yaml
  create acp_service codex-main
  create acp_route acp-codex
  create virtual_key acp-key
summary: create=3 update=0 skip=0 error=0
```

## 8. Verify Config

```bash
./agwctl gateway acp-service list
./agwctl gateway acp-route list
./agwctl gateway acp-runtime get
```

## 9. Send A Turn

Retrieve the generated VirtualKey value:

```bash
ACP_API_KEY=$(./agwctl gateway virtualkey get acp-key | jq -r '.key')
```

Send one streamed turn:

```bash
curl -N -s http://127.0.0.1:8080/acp/codex/turn \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $ACP_API_KEY" \
  -d '{"thread_id":"t-demo-1","input":"Reply with exactly one word: pong"}'
```

Expected event shape:

```text
event: session
data: {"session_id":"..."}

event: delta
data: {"text":"pong"}

event: done
data: {"stop_reason":"..."}
```

Use the emitted `session_id` on later turns to continue or reload the same
agent-side session:

```bash
curl -N -s http://127.0.0.1:8080/acp/codex/turn \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $ACP_API_KEY" \
  -d '{"thread_id":"t-demo-1","session_id":"<session-id>","input":"Continue with one short sentence."}'
```

## 10. Inspect Sessions And Transcript

Use the route-scoped consumer API with the same VirtualKey:

```bash
curl -s "http://127.0.0.1:8080/acp/codex/sessions?cwd=/tmp/acp-codex-test" \
  -H "Authorization: Bearer $ACP_API_KEY"

curl -s "http://127.0.0.1:8080/acp/codex/sessions/<session-id>/transcript?cwd=/tmp/acp-codex-test" \
  -H "Authorization: Bearer $ACP_API_KEY"
```

Or use the Admin API through `agwctl` for service-scoped operator inspection:

```bash
./agwctl gateway acp-service sessions codex-main --cwd /tmp/acp-codex-test
./agwctl gateway acp-service transcript codex-main <session-id> --cwd /tmp/acp-codex-test
```

## 11. Interactive Permission Mode

Change the service to:

```yaml
permission_mode: interactive
```

When the agent asks for permission, the turn stream emits:

```text
event: permission
data: {"request_id":"perm-...","data":{...}}
```

Answer through the route:

```bash
curl -s http://127.0.0.1:8080/acp/codex/permission \
  -H 'Content-Type: application/json' \
  -d '{"request_id":"perm-...","outcome":"selected","option_id":"<option-id>"}'
```

Or answer as an operator:

```bash
./agwctl gateway acp-runtime resolve-permission perm-... \
  --outcome selected \
  --option-id <option-id>
```

## Notes

- The dispatcher is enabled by the `acp` directive inside
  `agent_route_dispatcher`.
- ACP route IDs are auto-generated as `acp:<service_id>:<path_prefix>` when
  `id` is omitted.
- The gateway accepts a VirtualKey as `Authorization: Bearer <key>` or
  `x-api-key: <key>`.
- `codex` uses `codex-acp` by default. There is no native `codex acp`
  subcommand path in this gateway.
- `opencode` services use the fixed `opencode acp --cwd <cwd>` launch shape.
- Admin sessions are in memory. Restarting the gateway invalidates existing
  admin tokens.

## Next

- [ACP Architecture](../architecture/acp-architecture.md)
- [ACP Technical Specification](../reference/acp-technical-spec.md)
- [ACP API Reference](../reference/acp-api.md)
