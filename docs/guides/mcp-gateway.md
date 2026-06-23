# MCP Gateway

This guide covers the current MCP gateway surface, how MCP services and routes are configured, and what is implemented today.

## What The MCP Gateway Does

The gateway can terminate inbound MCP requests, validate VirtualKeys, and act as an MCP client toward upstream MCP services.

Current implemented areas include:

- MCP routing through `agent_route_dispatcher` when `mcp` is enabled
- MCP services persisted in config store
- MCP routes persisted in config store
- discovery, execution, and runtime inspection Admin APIs

## Upstream Transport Types

Current supported upstream transport types include:

- `streamable_http`
- `sse`
- `stdio`

## Minimal MCP Dispatcher Setup

Enable MCP in the dispatcher:

```caddy
agent_route_dispatcher {
	llm_api openai
	llm_api anthropic
	mcp
}
```

## Bundle Example

Minimal MCP bundle shape:

```yaml
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle

mcpServices:
  - id: mcp-http
    name: HTTP MCP Service
    transport: streamable_http
    url: ${MCP_HTTP_URL}

mcpRoutes:
  - service_id: mcp-http
    match_policy:
      path_prefix: /mcp/http
    auth_policy:
      require_virtual_key: true
```

See [examples/gateway.bundle.mcp.yaml](/Users/simpcl/github/agent-guide/agent-gateway/examples/gateway.bundle.mcp.yaml).

## MCP Route Behavior

MCP routes:

- always use `protocol: mcp` at runtime
- are tied to one `service_id`
- can require a VirtualKey
- share the common route matching foundation with LLM routes

If `id` is omitted, the MCP route config derives one from:

- `service_id`
- `match_policy.path_prefix`

The derived shape is the deterministic, slash-free (path prefix lowercased,
non-alphanumeric runs collapsed to `-`, `/` → `root`):

- `mcp:<service_id>:<path-slug>`

## Current Admin Surface

Implemented MCP admin families include:

- MCP service CRUD
- MCP route CRUD
- discovery endpoints
- execution endpoints
- runtime inspection endpoints
- session inspection endpoints

Current runtime inspection endpoints include:

- `GET /admin/mcp/runtime`
- `GET /admin/mcp/runtime/inflight`
- `GET /admin/mcp/runtime/progress`
- `GET /admin/mcp/runtime/history`

## Current Inbound MCP Methods

Currently handled inbound methods include:

- `initialize`
- `ping`
- `roots/list`
- `tools/list`
- `resources/list`
- `resources/templates/list`
- `prompts/list`
- `tools/call`
- `resources/read`
- `prompts/get`
- `completion/complete`

## Current Limitations

- richer runtime mutation controls are still limited
- broader notification semantics are not fully implemented
- completed-request history is currently bounded in memory

## Related Docs

- [bundle-yaml.md](bundle-yaml.md)
- [../architecture/mcp-architecture.md](../architecture/mcp-architecture.md)
