# Route Schema Reference

This page summarizes the current LLM, MCP, and unified Agent route shapes used
by static config, Admin API objects, and runtime resolution.

## Core Route Fields

The current route object includes:

- `id`
- `kind`
- `protocol`
- `description`
- `disabled`
- `match_policy`
- `auth_policy`
- `target_policy`
- `created_at` and `updated_at` (server-managed response fields)

Current route kinds:

- `llm`
- `mcp`
- `agent`

Current route protocols:

- `openai`
- `anthropic`
- `cc`
- `mcp`
- `agent`
- `a2a`

`a2a` is a route protocol, not a route kind. Native A2A routes use
`kind=agent` together with `protocol=a2a`.

## `match_policy`

Current fields:

- `host`
- `path_prefix`
- `methods`

These fields control request matching only.

## `auth_policy`

Current fields:

- `require_virtual_key`

If enabled, the gateway accepts a VirtualKey from:

- `Authorization: Bearer <key>`
- `x-api-key: <key>`

## `target_policy`

All LLM routes use `type: models`. Targets reference registered ProviderModels
or independent LogicalModels. Provider-only targets are rejected.

### Models Mode

A LogicalModel route exposes logical references:

```json
{
  "type": "models",
  "default_model": "chat",
  "model_targets": [
    {"name": "chat", "kind": "logical-model", "logical_model_id": "chat-default"}
  ]
}
```

A separate ProviderModel route exposes concrete models from one Provider, using
their upstream names:

```json
{
  "type": "models",
  "default_model": "gpt-4.1",
  "model_targets": [
    {"name": "gpt-4.1", "kind": "provider-model", "provider_id": "openai-main", "upstream_model": "gpt-4.1"}
  ]
}
```

Each route must use one target kind; mixing the two kinds is rejected.

Request `model` selects a route-visible `name`; absent model uses `default_model`.
Names are unique within the route. Each reference requires exactly the fields
for its kind. The referenced resource must exist. Unlisted models are forbidden.
Candidates, strategy and fallback belong to an independent LogicalModel; route
policies reject those fields. Model exposure routes use dynamic Admin/Bundle
management. Caddyfile and startup static Bundles support model declarations and explicit references.

### Agent Mode

An AgentRoute targets a stable Agent identity. The Agent's `runtime.type`
selects ACP, builtin, or HTTP execution without changing the
route URL, ID, or VirtualKey allowlist:

```json
{
  "id": "agent:codex-main:agents-codex",
  "kind": "agent",
  "protocol": "agent",
  "agent_id": "codex-main",
  "match_policy": {
    "path_prefix": "/agents/codex"
  },
  "auth_policy": {
    "require_virtual_key": true
  }
}
```

When `id` is omitted, it defaults to the deterministic, slash-free
`agent:<agent_id>:<path-slug>`
(the path prefix lowercased, non-alphanumeric runs collapsed to `-`, `/` →
`root`). Route ids must be slash-free so they are addressable as a single Admin
API path segment. Manage these routes through bundle `agentRoutes`,
`/admin/agents/routes`, or `agwctl agent-route`.

For native A2A ingress, use `protocol: "a2a"`. The target must be a ready HTTP
Agent, `match_policy.host` is required, and `methods` must be empty or contain
both `GET` and `POST`. See [a2a-ingress.md](a2a-ingress.md).

## Static Config Restrictions

Current static restrictions:

- Caddyfile and startup Bundle model definitions are read-only through Admin
- static/dynamic model identity conflicts fail startup
- static and dynamic routes both require explicitly registered model references
- Caddyfile and standalone static bundles accept AgentRoutes targeting Agents

## Related Docs

- [../guides/routes.md](../guides/routes.md)
- [../design/model-first-routing.md](../design/model-first-routing.md)
- [../design/route-target-policy.md](../design/route-target-policy.md)
