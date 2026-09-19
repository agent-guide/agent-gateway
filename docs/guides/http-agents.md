# HTTP Agents

This guide covers registration, authentication, route selection, health, and
session behavior for remote A2A services represented by
`runtime.type: http` Agents.

## Choose An Ingress Contract

One HTTP Agent can have either or both route forms:

| Route protocol | Client-facing contract | Gateway behavior |
| --- | --- | --- |
| `agent` | `POST /<prefix>/turn` and common Agent SSE events | translates between the common turn model and A2A |
| `a2a` | Agent Card GET plus native A2A JSON-RPC POST | governs and forwards accepted JSON-RPC/SSE bytes without re-encoding |

Use `protocol: agent` when clients should be independent of the remote
runtime. Use `protocol: a2a` when clients need native tasks, subscriptions, or
other admitted A2A methods.

## Register The Agent

The required HTTP runtime fields are:

```yaml
runtime:
  type: http
  http:
    card_url: https://reviewer.internal/.well-known/agent-card.json
    protocol: a2a
```

Optional fields:

- `auth_ref`: ID of a managed credential owned by this Agent;
- `timeout_seconds`: total operation deadline; zero uses 120 seconds.

The Card must advertise an A2A 1.0 interface whose `protocolBinding` is exactly
`JSONRPC`. The interface must be same-origin with the Card. HTTPS is required
except for loopback development addresses. Redirects are rejected.

## Configure Remote Bearer Authentication

If a Card requires HTTP bearer security, store a credential with the exact
scope `http-agent:<agent-id>` and set `runtime.http.auth_ref` to its ID. Do not
use a provider-scoped LLM credential. Credential values are resolved for each
request, so rotation and OAuth refresh do not require rebuilding the Agent.

For example, create an API-key credential through the credentials Admin API:

```bash
curl -X POST http://localhost:8019/admin/credentials \
  -u "$AGW_ADMIN_BASIC_AUTH" \
  -H 'Content-Type: application/json' \
  -d '{
    "id":"reviewer-upstream",
    "scope":"http-agent:remote-reviewer",
    "type":"api_key",
    "label":"Remote reviewer bearer",
    "attributes":{"api_key":"replace-with-upstream-secret"}
  }'
```

Then add `auth_ref: reviewer-upstream` under `runtime.http`. The gateway
terminates ingress `Authorization` and `x-api-key` values and injects only the
selected upstream bearer credential.

## Configure Routes

A common turn route needs an Agent ID and path prefix:

```yaml
- id: reviewer-turn
  protocol: agent
  agent_id: remote-reviewer
  match_policy:
    path_prefix: /agents/reviewer
  auth_policy:
    require_virtual_key: true
```

A native A2A route additionally requires a trusted `host`. Its method matcher
must be empty or include both `GET` and `POST`:

```yaml
- id: reviewer-a2a
  protocol: a2a
  agent_id: remote-reviewer
  match_policy:
    host: gateway.example.com
    path_prefix: /a2a/reviewer
    methods: [GET, POST]
  auth_policy:
    require_virtual_key: true
```

The native endpoints are exactly:

- `GET /a2a/reviewer/.well-known/agent-card.json`;
- `POST /a2a/reviewer`.

Additional path segments are not A2A REST operations and return `404`.

## Authentication And Admission

For routes with `require_virtual_key: true`, common turns and native POSTs
accept either `Authorization: Bearer <virtual-key>` or `x-api-key:
<virtual-key>`. The rewritten Card GET remains public discovery. Native POSTs
also consume the Agent rate-limit bucket configured on the VirtualKey.

The served Card always points to the gateway URL. It advertises gateway bearer
security only when the route requires a VirtualKey, omits remote signatures,
and disables push notifications and extended-card discovery.

## Sessions And Cancellation

The common turn path maps the gateway `session_id` to the remote A2A
`contextId`. If a task stops in `INPUT_REQUIRED`, send the next input with the
same `session_id`; the gateway resumes the bound task. These bindings are
bounded, process-local state and do not survive restart or move across
replicas.

HTTP Agents do not implement the common permission or transcript capability.
Cancel an active common run through:

```text
DELETE /admin/agents/{agent_id}/runs/{run_id}
```

The gateway performs a bounded best-effort remote `CancelTask` once a task ID
is known.

## Inspect Readiness And Usage

Use the Agent management surface:

```bash
./agwctl agent get remote-reviewer
./agwctl agent health remote-reviewer
./agwctl agent capabilities remote-reviewer
./agwctl agent runs remote-reviewer
```

Native A2A interactions use `route_kind=agent`, `route_protocol=a2a`, and
`runtime_type=http`. SQLite stores them in `a2a_usage_events`; they also appear
in unified interaction queries, OTLP export, and Prometheus request counters.

## Troubleshooting

- Route apply fails: fetch the Card directly and verify A2A version `1.0`, a
  same-origin `JSONRPC` interface, and a satisfiable anonymous or bearer
  security alternative.
- Agent is not ready: inspect `/admin/agents/{id}/health`; verify Card
  reachability and the `auth_ref` credential owner/scope.
- Native POST returns `-32009`: send exactly one `A2A-Version: 1.0` header or
  query service parameter.
- Native POST returns `-32602`: check the selected interface tenant and remove
  embedded push notification configuration.
- A continuation starts a new context: session bindings may have expired or
  been lost on process restart; start a new conversation explicitly.

## Related Docs

- [HTTP Agent Quick Start](../getting-started/quickstart-http-agent.md)
- [A2A Ingress Reference](../reference/a2a-ingress.md)
- [HTTP Agent Architecture](../architecture/http-agent-architecture.md)
