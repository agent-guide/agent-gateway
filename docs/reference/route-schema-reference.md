# Route Schema Reference

This page summarizes the current LLM route shape used by static config, Admin API objects, and runtime resolution.

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
- `created_at`
- `updated_at`

Current route kinds:

- `llm`
- `mcp`
- `acp`

Current route protocols:

- `openai`
- `anthropic`
- `cc`
- `mcp`
- `acp`

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

LLM routes support two valid target modes:

- direct-provider mode
- logical-model mode

### Direct-Provider Mode

Current shape:

```json
{
  "provider_target": {
    "provider_id": "openai-main"
  }
}
```

Behavior:

- request `model` is treated as the upstream model name
- supported in dynamic routes, Caddyfile routes, and `agwd --static-config`

### Logical-Model Mode

Current shape includes concepts such as:

- `default_model`
- `model_selector_strategy`
- `fallback`
- `model_targets`

Each `model_target` can contain:

- `name`
- `strategy`
- `default_candidate`
- `candidates`

Each candidate can contain:

- `provider_id`
- `upstream_model`
- `weight`
- `priority`
- `default`

Behavior:

- request `model` is treated as the route model name
- the gateway resolves it to one concrete provider and upstream model binding
- supported through dynamic route management and bundle workflows
- rejected in Caddyfile routes and `agwd --static-config`

### ACP-Service Mode

ACP routes use an ACP service target. The stored route target policy is:

```json
{
  "kind": "acp-service",
  "service_id": "codex-main"
}
```

ACP Admin API and bundle objects expose this as top-level `service_id`:

```json
{
  "service_id": "codex-main",
  "match_policy": {
    "path_prefix": "/acp/codex"
  },
  "auth_policy": {
    "require_virtual_key": true
  }
}
```

ACP routes normalize to `kind: "acp"` and `protocol: "acp"`. When `id` is
omitted, it defaults to the deterministic, slash-free `acp:<service_id>:<path-slug>`
(the path prefix lowercased, non-alphanumeric runs collapsed to `-`, `/` →
`root`). Route ids must be slash-free so they are addressable as a single Admin
API path segment; a slash-bearing id is rejected with `400` on create/update.

## Static Config Restrictions

Current static restrictions:

- Caddyfile LLM routes only support direct-provider mode
- `agwd --static-config` `llmRoutes` only support direct-provider mode
- `agwd --static-config` does not support `managedModels`

## Related Docs

- [../guides/routes.md](../guides/routes.md)
- [../design/model-first-routing.md](../design/model-first-routing.md)
- [../design/route-target-policy.md](../design/route-target-policy.md)
