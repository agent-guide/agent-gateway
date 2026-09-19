# MCP Architecture

## Scope

This page describes the implemented MCP gateway architecture. The gateway
terminates the client-facing MCP session, applies route and VirtualKey policy,
and acts as a protocol-aware MCP client toward an upstream service. It does not
use Caddy `reverse_proxy` as the MCP execution engine.

Tool policy decisions that are not yet shipped are defined separately in
[MCP Tool Policy](../design/mcp-tool-policy.md).

## Components

```text
MCP client
  -> http.handlers.agent_route_dispatcher (mcp enabled)
  -> pkg/dispatcher.Handler.dispatchMCP
  -> pkg/gateway/mcproute resolver
  -> pkg/mcp/service.Manager
  -> pkg/mcp/transport (streamable HTTP, stdio, or legacy SSE)
  -> upstream MCP server or local process
```

The shared `pkg/gateway/routecore` manager persists and matches LLM, MCP, and
Agent routes. `pkg/gateway/mcproute` projects an MCP route from that common
record and resolves its target `service_id`. `pkg/mcp/service` owns protocol
execution and gateway-visible sessions. `pkg/mcp/transport` owns only
transport-specific connection, framing, upstream request ids, and session
handles.

## Configuration And Storage

MCP services are stored in `mcp_services`. MCP routes share the `routes` store
with other route families and use:

- `kind: mcp`;
- `protocol: mcp`;
- host, path-prefix, and method match policy;
- optional VirtualKey admission;
- a target policy containing the MCP `service_id`.

Service definitions select one upstream transport:

- Streamable HTTP with a URL and optional upstream authentication;
- stdio with a command, arguments, and controlled process environment;
- legacy HTTP+SSE with stream and POST endpoints.

Services and routes participate in Admin CRUD, GatewayBundle validation,
export, and idempotent apply. Runtime sessions and request history are not
configuration objects.

## Request Flow

```text
HTTP JSON-RPC request
  -> route match
  -> VirtualKey validation and rate-limit admission
  -> MCP envelope and method validation
  -> service resolution
  -> initialize or reuse the upstream session
  -> execute through the selected transport
  -> write the MCP JSON-RPC response
  -> finish the interaction event
```

The dispatcher handles the standard discovery and execution families,
including tools, resources, resource templates, prompts, completions, ping,
roots, and the supported notification set. Pagination cursors and upstream
`nextCursor` values pass through the protocol layer.

## Transport Runtime

### Streamable HTTP

The Streamable HTTP client performs initialization, retains
`Mcp-Session-Id`, accepts JSON or SSE responses, and injects configured
upstream authentication. Initialized clients are cached by the service layer.

### stdio

The stdio transport starts a local MCP process with `exec.CommandContext`,
exchanges framed JSON-RPC over its pipes, and ties process lifetime to the
gateway-managed session. It is a first-class service transport, not an HTTP
proxy fallback.

### Legacy SSE

The legacy SSE transport connects to the event stream, discovers the POST
endpoint, sends requests over HTTP, matches responses, and records progress.
It exists for MCP servers using the older HTTP+SSE transport shape.

## Sessions

`pkg/mcp/service` owns the gateway session lifecycle. Transport clients own
upstream handles such as `Mcp-Session-Id`, but raw upstream ids are not the
gateway's sole session identity. This separation lets the service layer close
or replace transports without changing route identity.

Initialized sessions exist for Streamable HTTP, stdio, and legacy SSE. The
Admin API exposes service session inspection at:

```text
GET /admin/mcp/services/{id}/sessions
```

## Runtime Tracking

The shared MCP runtime registry tracks:

- in-flight requests;
- cancellation state;
- progress observed from upstream notifications;
- bounded completed-request history.

Read-only snapshots are available from:

```text
GET /admin/mcp/runtime
GET /admin/mcp/runtime/inflight
GET /admin/mcp/runtime/progress
GET /admin/mcp/runtime/history
```

Inbound `notifications/cancelled` update request cancellation. Upstream
progress is captured in runtime state; it is not relayed as a complete
downstream progress stream.

## Security Boundary

The dispatcher terminates caller authentication. A VirtualKey is validated
against the matched route before the service executes. Upstream credentials
belong to the service transport and are not copied from caller
`Authorization` or `x-api-key` headers.

Transport implementations do not own route authorization or tool policy.
This keeps security decisions at the protocol-aware dispatcher and service
layers rather than embedding them in HTTP or process plumbing.

## Observability

Each accepted MCP request uses the shared interaction span and produces an
`MCPUsageEvent`. Events record bounded route, service, method, tool, success,
latency, cancellation, Agent correlation, and optional audited argument
dimensions. SQLite persists them in `mcp_usage_events`; the same events feed
unified interaction queries, Prometheus counters, and optional OTLP export.

## Current Limits

- Upstream progress and arbitrary notifications are observed selectively, not
  transparently relayed end to end.
- Completed request history is bounded and in memory rather than durable.
- Runtime Admin endpoints are inspection-oriented; they do not provide a
  general mutation API for arbitrary session state.
- Route-level tool filtering, aliasing, rewriting, and synthetic tools are not
  part of the current runtime.
