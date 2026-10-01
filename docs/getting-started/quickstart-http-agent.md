# Quick Start: HTTP Agent

This guide registers an existing A2A Protocol 1.0 service as an HTTP Agent,
then exposes both the common Agent turn API and governed native A2A ingress.

## Prerequisites

- a running A2A 1.0 JSON-RPC service with an HTTPS Agent Card URL (loopback
  HTTP is accepted for local development);
- Go toolchain, `curl`, and `jq`;
- the gateway binaries built with `make build`.

## 1. Start The Gateway

Generate an admin password hash:

```bash
./agw hash-password --plaintext 'your-password'
```

Create `Caddyfile`:

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
		basic_auth {
			admin <bcrypt-hash>
		}
		agent_gateway_admin
	}
}

http://127.0.0.1:8080 {
	agent_route_dispatcher {
		agent
	}
}
```

Start the gateway:

```bash
./agw run --config ./Caddyfile
```

## 2. Apply The HTTP Agent And Routes

Replace the `card_url` below with the remote service's Agent Card URL. The
native route's `host` matches only the hostname clients use to reach the
gateway; do not include the listener port.

```yaml
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle

agents:
  - id: remote-reviewer
    name: Remote Reviewer
    runtime:
      type: http
      http:
        card_url: https://reviewer.internal/.well-known/agent-card.json
        protocol: a2a
        timeout_seconds: 120
    routes: {}
    resources: {}
    policy: {}

agentRoutes:
  - id: reviewer-turn
    protocol: agent
    agent_id: remote-reviewer
    match_policy:
      path_prefix: /agents/reviewer
    auth_policy:
      require_virtual_key: true

  - id: reviewer-a2a
    protocol: a2a
    agent_id: remote-reviewer
    match_policy:
      host: 127.0.0.1
      path_prefix: /a2a/reviewer
      methods: [GET, POST]
    auth_policy:
      require_virtual_key: true

virtualKeys:
  - id: reviewer-key
    allowed_route_ids: [reviewer-turn, reviewer-a2a]
```

Save this as `gateway.bundle.http-agent.yaml`, then apply it:

```bash
export AGW_ADMIN_BASIC_AUTH=admin:your-password
./agwctl validate -f gateway.bundle.http-agent.yaml
./agwctl apply -f gateway.bundle.http-agent.yaml
./agwctl agent get remote-reviewer
./agwctl agent-route list
```

Route creation fails closed unless the Card contains a usable, same-origin
`JSONRPC` interface for A2A 1.0. If the Card requires bearer authentication,
configure an Agent-owned credential as described in the HTTP Agent guide.

## 3. Send A Common Turn

```bash
AGENT_API_KEY=$(./agwctl virtualkey get reviewer-key | jq -r '.key')

curl -N -s http://127.0.0.1:8080/agents/reviewer/turn \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $AGENT_API_KEY" \
  -d '{"input":"Review this change"}'
```

The gateway translates the turn to A2A and returns the common Agent SSE event
contract. Reuse the emitted `session_id` on a later request to continue the
same remote context.

## 4. Use Native A2A Ingress

Card discovery is public even though the JSON-RPC operation requires a
VirtualKey:

```bash
curl -s http://127.0.0.1:8080/a2a/reviewer/.well-known/agent-card.json

curl -s http://127.0.0.1:8080/a2a/reviewer \
  -H 'Content-Type: application/json' \
  -H 'A2A-Version: 1.0' \
  -H "Authorization: Bearer $AGENT_API_KEY" \
  -d '{"jsonrpc":"2.0","id":"demo-1","method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"text":"Review this change"}]}}}'
```

## Next

- [Operate HTTP Agents](../guides/http-agents.md)
- [A2A Ingress Reference](../reference/a2a-ingress.md)
- [HTTP Agent Architecture](../architecture/http-agent-architecture.md)
