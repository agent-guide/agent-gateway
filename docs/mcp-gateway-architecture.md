# MCP Gateway Architecture And Current Status

## 1. Scope

This document describes the MCP gateway architecture and the current implementation status in `agent-gateway`.

It combines two concerns:

- the recommended architecture and design direction
- the current implementation status and remaining gaps

It is intentionally not a full project status document. It does not try to summarize the LLM gateway, memory, agent, metrics, or the whole repository roadmap.

The goal is to answer one design question clearly:

- should MCP gateway behavior be implemented mainly as a Caddy reverse proxy wrapper
- or as a protocol-aware gateway that uses forward MCP client transports at runtime

The recommended answer is:

- use the main inbound gateway dispatcher with MCP protocol handling enabled
- use protocol-aware forward MCP client transports for upstream access
- do not make `reverse_proxy` the primary MCP execution model

## 2. Current Implementation Status

### 2.1 Implemented And Usable Today

#### Inbound MCP Gateway Surface

- MCP handling in `http.handlers.agent_route_dispatcher` when `mcp` is enabled
- MCP route model in `pkg/gateway/mcproute`
- unified route matching via `pkg/gateway/routecore.AgentRouteConfigManager` shared with LLM routes
- MCP dispatch in `pkg/dispatcher/mcp_handler.go`, dispatched by `pkg/dispatcher/handler.go` on `RouteKindMCP`
- VirtualKey validation for MCP routes when required
- MCP JSON-RPC request parsing and response writing

#### Persisted MCP Configuration

- config-store-backed `mcp_services`
- config-store-backed MCP routes stored in `routes` (same store as LLM routes, distinguished by `kind: "mcp"`)
- Admin CRUD for `mcp_services`
- Admin CRUD for MCP routes

#### Upstream MCP Discovery

- `tools/list`
- `resources/list`
- `resources/templates/list`
- `prompts/list`
- pagination passthrough using `cursor` and `nextCursor`

#### Upstream MCP Execution

- `tools/call`
- `resources/read`
- `prompts/get`
- `completion/complete`

#### Supported Upstream Transport Behavior

- Streamable HTTP transport support
- `initialize`
- `notifications/initialized`
- `MCP-Session-Id`
- JSON response handling
- SSE response handling
- session cache for initialized upstream MCP services

#### Inbound MCP Methods Currently Handled

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

#### Inbound Notifications Currently Handled

- `notifications/initialized`
- `notifications/cancelled`
- `notifications/progress`
- `notifications/message`

#### Runtime Inspection And Tracking

- shared MCP runtime registry
- in-flight request tracking
- request cancellation tracking
- progress state capture
- Admin runtime inspection endpoints:
  - `GET /admin/mcp/dispatcher/runtime`
  - `GET /admin/mcp/dispatcher/inflight`
  - `GET /admin/mcp/dispatcher/progress`

#### Transport Packages

- `pkg/mcp/transport/streamablehttp.go`: fully integrated as the primary upstream transport
- `pkg/mcp/transport/stdio.go`: transport-layer implementation complete (Connect, Send, Receive, Close), not yet wired as a gateway upstream service type
- `pkg/mcp/transport/sse.go`: SSE read loop implemented, `Send()` not yet implemented

### 2.2 Implemented But Not Yet Complete

These areas exist in the codebase, but should not be treated as fully complete MCP gateway product surfaces yet.

#### stdio Upstream Transport Integration

- `pkg/mcp/transport/stdio.go` exists with a working Connect/Send/Receive/Close implementation
- no service config type or session manager in `pkg/mcp/service` wires stdio as an upstream client
- stdio is a transport-layer implementation only and cannot yet be selected as a gateway upstream in `mcp_services`

#### SSE Transport Send

- `pkg/mcp/transport/sse.go` handles SSE stream reading via `Connect()` and `readLoop()`
- `Send()` is not implemented and returns an error
- the SSE transport cannot be used for bidirectional upstream communication until `Send()` is implemented

#### Runtime Registry Depth

- the runtime registry captures in-flight requests and progress state
- it does not yet provide durable history
- it does not yet relay upstream progress events back to clients as a complete end-to-end flow

#### Session Model

- current behavior supports initialized upstream sessions for Streamable HTTP
- richer long-lived stateful session handling is not yet complete

#### MCP Bundle / Export / Apply Flows

- MCP config objects are persisted and manageable through Admin APIs
- MCP objects are not yet fully integrated into broader bundle export / apply workflows

### 2.3 Not Yet Implemented

#### stdio As Gateway Upstream Type

- service config and session manager support for `transport_type: stdio`
- spawning and managing local MCP processes as upstream clients from gateway service definitions

#### Legacy SSE Upstream Transport

- first-class non-compatibility SSE upstream support, if retained as a normal configured transport
- this requires completing `SSETransport.Send()` and wiring SSE as a selectable service type

#### Richer Runtime Control

- admin-triggered cancellation endpoints for in-flight MCP requests
- richer capability inspection and runtime debugging endpoints beyond the current runtime snapshots
- durable request or event history

#### More Complete Upstream Event Relay

- end-to-end relay of upstream progress events back to clients
- fuller notification semantics beyond the currently handled notification set

## 3. Decision

The repository should implement MCP gateway as:

- inbound HTTP handler module
- runtime MCP gateway service
- forward MCP transport clients
- config-store-backed MCP upstream definitions

This means the gateway terminates the client-facing MCP session and acts as an MCP client toward upstream MCP servers.

It should not behave as a mostly transparent reverse proxy.

## 4. Why This Fits This Repository

The current repository already uses a protocol-aware gateway pattern:

- Caddy-facing modules parse inbound protocol requests
- runtime packages own provider or route resolution
- outbound calls are executed by repository-owned clients
- admin surfaces manage persisted configuration and runtime state

That pattern is already used for LLM APIs:

- inbound protocol handler under `pkg/dispatcher/llmapi/...`
- runtime route and provider logic under `pkg/gateway/...`
- outbound upstream calls through `pkg/llm/provider/...`

MCP follows the same architectural style. LLM and MCP routes share a common route foundation in `pkg/gateway/routecore/` and are dispatched by the same `pkg/dispatcher.Handler`, which branches on `RouteKind`.

## 5. Why Reverse Proxy Should Not Be The Core Abstraction

Using Caddy `reverse_proxy` as the primary MCP implementation has some short-term convenience, but it creates the wrong center of gravity.

### 5.1 MCP Is Not Just Raw HTTP Pass-Through

Remote MCP over Streamable HTTP includes protocol semantics such as:

- JSON-RPC request and response framing
- initialize lifecycle
- optional `Mcp-Session-Id`
- request-by-request authorization requirements
- stream and non-stream response handling
- compatibility handling for older SSE-based servers

Those are protocol concerns, not just proxy concerns.

### 5.2 The Repository Needs More Than HTTP Tunneling

The expected value of an MCP gateway in this repository is not simple byte forwarding. It is likely to include:

- VirtualKey authentication at the gateway edge
- upstream credential selection
- per-upstream auth injection
- tool allow or deny policy
- resource allow or deny policy
- audit logging of tool calls
- multiplexing across multiple upstream MCP servers
- compatibility handling across MCP transport variants

Those behaviors require protocol-aware request handling and upstream client logic.

### 5.3 Reverse Proxy Biases The Design Toward HTTP-Only Upstreams

This repository includes `pkg/mcp/transport/` implementations for:

- `stdio`
- `sse`
- `streamablehttp`

The architectural implication is important:

- MCP upstream access should be transport-abstracted
- not every upstream is best modeled as a remote HTTP origin behind `reverse_proxy`

If the core design starts from Caddy reverse proxy, `stdio` becomes an awkward special case instead of a first-class transport.

## 6. Recommended Architecture

### 6.1 Inbound Layer

Use the main Caddy HTTP handler module and enable MCP handling explicitly.

Current module ID:

- `http.handlers.agent_route_dispatcher`

Current Caddyfile option:

- `mcp`

Responsibilities:

- match inbound MCP gateway routes
- enforce gateway authentication and VirtualKey policy
- attach request-scoped gateway metadata
- hand off to `pkg/dispatcher.Handler` for MCP dispatch

MCP request handling stays inside the shared dispatcher runtime. LLM protocol adapters remain separate `llm_api` modules registered under `pkg/dispatcher/llmapi/...`.

### 6.2 Shared Route Foundation Layer

Both LLM and MCP routes share a common config and storage foundation.

Package:

- `pkg/gateway/routecore/`

Key types:

- `AgentRouteConfig`: the base persisted route record, with `Kind` (`llm` or `mcp`), `Protocol`, `MatchPolicy`, `AuthPolicy`, `TargetPolicy`
- `AgentRouteConfigManager`: stores and resolves both LLM and MCP routes from the same config store under `StoreRoutes`
- `RouteKind`, `RouteProtocol`, `RouteTargetPolicyKind`: shared constants used by both route families

`pkg/gateway/mcproute/` extends `AgentRouteConfig` into `MCPRoute` and `MCPRouteConfig` by decoding the `TargetPolicy` JSON field into a `ServiceID`.

`pkg/gateway/llmroute/` extends `AgentRouteConfig` into `LLMRoute` and related types using provider and model target policy shapes.

### 6.3 Runtime Dispatch Layer

Actual protocol dispatch lives in `pkg/dispatcher/`.

Package:

- `pkg/dispatcher/`

Key files:

- `handler.go`: matches route kind, branches to `dispatchLLM` or `dispatchMCP`
- `mcp_handler.go`: parses MCP JSON-RPC, resolves the MCP route, and calls `pkg/mcp/service`
- `llmapi/openai/`, `llmapi/anthropic/`: LLM protocol handlers

The Caddy adapter at `caddy/dispatcher/` wires configuration and modules into `pkg/dispatcher.Handler`.

### 6.4 Runtime MCP Service Layer

Add a runtime package responsible for MCP gateway orchestration.

Current package:

- `pkg/mcp/service/`

Responsibilities:

- manage MCP service definitions (config CRUD)
- manage client-facing upstream session state
- execute upstream discovery and execution requests
- select and initialize the upstream transport client

### 6.5 Upstream Transport Layer

`pkg/mcp/transport/` provides outbound MCP transport implementations.

Current transport direction:

- first-class integrated
  - `streamablehttp`: fully integrated as the upstream transport for `mcp_services`
- transport-layer complete but not yet integrated as upstream service type
  - `stdio`: Connect/Send/Receive/Close implemented; no gateway service wiring yet
- transport-layer incomplete
  - `sse`: read path implemented; `Send()` not yet implemented

Recommended transport responsibilities:

- transport-specific connect and send logic
- initialize handshake
- session ID tracking
- auth header injection
- JSON-RPC request ID tracking
- tool, resource, and prompt request helpers

Transport non-responsibilities:

- gateway VirtualKey policy
- inbound HTTP route matching
- admin CRUD

### 6.6 Configuration And Storage Layer

Persist MCP upstream definitions through the existing config store model rather than static-only Caddy directives.

Current store family:

- `mcp_services`

Current config shape:

- `id`
- `name`
- `transport_type`
- `url` for remote transports
- `command` and `args` for `stdio` (field exists in config, not yet fully wired)
- auth reference or inline auth config
- enabled flag
- capability cache metadata
- policy metadata such as tags or tool filters

The existing admin shape under `/admin/mcp/services/...` and `/admin/mcp/routes/...` reflects this direction.

## 7. Request Flow

Current client-facing request flow:

```text
HTTP request
  -> http.handlers.agent_route_dispatcher with mcp enabled
  -> gateway auth and VirtualKey validation
  -> pkg/dispatcher.Handler.dispatchMCP()
  -> MCPRouteResolver.Resolve() -> *MCPRoute (with ServiceID)
  -> pkg/dispatcher.Handler.dispatchJSONRPC()
  -> pkg/mcp/service.Manager executes the requested MCP method
  -> pkg/mcp transport sends initialize if needed
  -> pkg/mcp transport executes upstream JSON-RPC call or stream
  -> MCP JSON-RPC response written back to client
```

For a remote Streamable HTTP upstream, the outbound path looks like:

```text
pkg/mcp/service.Manager
  -> pkg/mcp/transport/streamablehttp client
  -> HTTP request with Authorization and MCP headers
  -> upstream MCP server
  -> response and optional session ID
  -> session state cached by gateway
```

For a local stdio upstream (transport layer only, not yet wired as gateway upstream):

```text
pkg/mcp/transport/stdio.StdioTransport
  -> spawn or connect local MCP process via exec.CommandContext
  -> initialize session
  -> exchange JSON-RPC messages over stdio pipes
```

## 8. Admin API Shape

The current implemented admin surface is:

- `GET /admin/mcp/services`
- `POST /admin/mcp/services`
- `GET /admin/mcp/services/{id}`
- `PUT /admin/mcp/services/{id}`
- `DELETE /admin/mcp/services/{id}`
- `GET /admin/mcp/services/{id}/tools`
- `GET /admin/mcp/services/{id}/resources`
- `GET /admin/mcp/services/{id}/prompts`
- `POST /admin/mcp/services/{id}/tools/call`
- `POST /admin/mcp/services/{id}/resources/read`
- `POST /admin/mcp/services/{id}/prompts/get`
- `GET /admin/mcp/routes`
- `POST /admin/mcp/routes`
- `GET /admin/mcp/routes/{id}`
- `PUT /admin/mcp/routes/{id}`
- `DELETE /admin/mcp/routes/{id}`
- `GET /admin/mcp/dispatcher/runtime`
- `GET /admin/mcp/dispatcher/inflight`
- `GET /admin/mcp/dispatcher/progress`

Recommended near-term additions:

- `POST /admin/mcp/dispatcher/inflight/{route_id}/{request_id}/cancel`
- capability cache inspection
- upstream session connect/disconnect actions if session lifecycle needs to be externally managed
- richer filter/policy editing for `mcp_services` and MCP routes

Recommended response metadata:

- connection status
- last error
- resolved transport type
- session status
- discovered capabilities

## 9. Route Model

Do not model MCP servers as `llm.providers.*`.

Do not model MCP endpoints as `llm_api` handler types.

MCP routes use a separate route kind (`kind: "mcp"`) within the shared `routecore.AgentRouteConfig` foundation. The `MCPRouteConfig` and `MCPRoute` types in `pkg/gateway/mcproute/` extend this base.

Current route fields:

- `id` (auto-generated as `"mcp:" + service_id + ":" + path_prefix` if not set)
- `kind` (always `"mcp"`)
- `protocol` (always `"mcp"`)
- `description`
- `disabled`
- `match_policy.host`
- `match_policy.path_prefix`
- `match_policy.methods`
- `auth_policy.require_virtual_key`
- `service_id` (the linked `mcp_services` entry; encoded in `target_policy` as `kind: "mcp-service"`)

Reason:

- MCP routing is upstream-client oriented, not model-routing oriented
- policy fields such as `allowed_tools`, `denied_tools`, and `allowed_resource_prefixes` are reserved for a later phase

## 10. Session Model

The gateway should be stateful about upstream MCP sessions even if the client-facing API remains HTTP-based.

Current session ownership:

- `pkg/mcp/service` owns gateway-visible session lifecycle
- `pkg/mcp/transport` client owns transport-visible session handles such as `Mcp-Session-Id`

Important rule:

- do not expose raw upstream session IDs as the only gateway session identifier

The gateway may map one gateway session to one upstream session, but the gateway should own that mapping.

This keeps room for:

- credential rotation
- reconnect logic
- auditing
- future session isolation rules

## 11. Security And Policy

The main reason to avoid a transparent reverse proxy design is policy enforcement.

Recommended phase-1 controls:

- gateway VirtualKey authentication
- per-route upstream client allowlist
- static allowlist or denylist for tool names
- full audit logging for `tools/call`

Recommended later controls:

- resource URI policy
- prompt exposure policy
- per-VirtualKey capability restrictions
- human approval hooks for selected tools

These controls should live in `pkg/mcp/service` or `pkg/dispatcher/mcp_handler.go`, not inside transport implementations.

## 12. Caddy Integration Strategy

Caddy still has an important role, just not as the upstream execution engine.

Use Caddy for:

- HTTP serving
- TLS
- host and path binding
- lifecycle integration with the rest of the gateway
- consistent config loading through the app module

Do not rely on Caddy `reverse_proxy` for:

- upstream MCP session management
- initialize handshake logic
- auth token injection policies
- tool-level authorization
- transport abstraction over `stdio` and HTTP

## 13. MVP Status

The first shippable MCP gateway scope defined in the original plan is substantially complete.

### 13.1 Achieved

- inbound HTTP MCP endpoint via `agent_route_dispatcher` with `mcp` enabled
- remote Streamable HTTP upstream transport (fully integrated)
- config-store-backed MCP service definitions and routes
- gateway VirtualKey validation for MCP routes
- pass-through JSON-RPC method handling for all standard MCP methods
- Admin CRUD for services, routes, and runtime inspection
- Admin execution endpoints for tools, resources, and prompts

### 13.2 Remaining Gaps Before Full MVP Closure

- stdio upstream wiring: `pkg/mcp/transport/stdio.go` exists but `pkg/mcp/service` does not yet create or manage stdio-backed clients
- SSE `Send()`: `pkg/mcp/transport/sse.go` connect/read works but `Send()` is not implemented, blocking SSE as an upstream transport
- audit logging for `tools/call` is not yet confirmed as a durable log path

## 14. Evolution Path

After the remaining MVP gaps are closed, the recommended expansion order is:

1. wire stdio upstream: add service config support and session manager for `transport_type: stdio`
2. complete SSE upstream: implement `SSETransport.Send()` and wire SSE as a selectable service type
3. add tool, resource, and prompt policy controls (allow/deny lists on routes)
4. add admin-triggered cancellation for in-flight MCP requests
5. add richer session inspection and debugging endpoints
6. add durable request and event history

## 15. Final Recommendation

For this repository, the correct default architecture is:

- Caddy module for inbound serving
- shared route foundation in `pkg/gateway/routecore/` for both LLM and MCP routes
- `pkg/dispatcher.Handler` for unified dispatch branching on route kind
- `pkg/mcp/service` runtime for MCP protocol and session handling
- `pkg/mcp/transport` forward clients for outbound execution

In short:

- use Caddy as the ingress host
- do not use reverse proxy as the core MCP abstraction
- MCP and LLM routes share the same route storage and match infrastructure
