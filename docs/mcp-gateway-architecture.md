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
- route resolution for MCP requests through the shared runtime
- VirtualKey validation for MCP routes when required
- MCP JSON-RPC request parsing and response writing

#### Persisted MCP Configuration

- config-store-backed `mcp_services`
- config-store-backed `mcp_routes`
- Admin CRUD for `mcp_services`
- Admin CRUD for `mcp_routes`

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

### 2.2 Implemented But Not Yet Complete

These areas exist in the codebase, but should not be treated as fully complete MCP gateway product surfaces yet.

#### Transport Integration Beyond Streamable HTTP

- `stdio` and `sse` related packages or code paths exist
- they are not yet integrated as equally supported runtime transports for MCP dispatch

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

#### Additional Transport Support

- first-class `stdio` upstream transport integration
- first-class non-compatibility `sse` transport integration, if retained as a normal configured transport

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

MCP should follow the same architectural style instead of introducing a separate reverse-proxy-first model.

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

This repository already includes `pkg/mcp` transport concepts for:

- `stdio`
- `sse`

Even though the transport package is early, the architectural implication is important:

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
- hand off to runtime MCP gateway service

MCP request handling stays inside the shared dispatcher runtime, while LLM protocol adapters remain separate `llm_api` modules.

### 6.2 Runtime Service Layer

Add a runtime package responsible for MCP gateway orchestration.

Recommended package:

- `pkg/mcp/service/`

Responsibilities:

- resolve the inbound gateway route or upstream binding
- select the configured upstream MCP server
- manage client-facing and upstream-facing session state
- enforce policy before forwarding requests upstream
- translate between client-facing MCP surface and upstream transport implementation
- record audit and observability events

This package is the MCP equivalent of the current `pkg/gateway` plus parts of `pkg/dispatcher`.

### 6.3 Upstream Transport Layer

Extend `pkg/mcp/` so it becomes the outbound MCP transport and client runtime.

Recommended transport direction:

- first-class
  - `stdio`
  - `streamable_http`
- compatibility
  - legacy `sse`

Recommended `pkg/mcp` responsibilities:

- transport-specific connect and send logic
- initialize handshake
- session ID tracking
- auth header injection
- JSON-RPC request ID tracking
- tool, resource, and prompt request helpers

Recommended `pkg/mcp` non-responsibilities:

- gateway VirtualKey policy
- inbound HTTP route matching
- admin CRUD

### 6.4 Configuration And Storage Layer

Persist MCP upstream definitions through the existing config store model rather than static-only Caddy directives.

Recommended store family:

- `mcp_services`

Recommended config shape:

- `id`
- `name`
- `transport_type`
- `url` for remote transports
- `command` and `args` for `stdio`
- auth reference or inline auth config
- enabled flag
- capability cache metadata
- policy metadata such as tags or tool filters

The existing admin shape under `/admin/mcp/services/...` and `/admin/mcp/routes/...` is already close to this direction.

## 7. Request Flow

Recommended client-facing request flow:

```text
HTTP request
  -> http.handlers.agent_route_dispatcher with mcp enabled
  -> gateway auth and VirtualKey validation
  -> pkg/mcp/service resolves upstream MCP client definition
  -> pkg/mcp/service loads or opens upstream MCP session
  -> pkg/mcp transport sends initialize if needed
  -> pkg/mcp/service evaluates policy for the requested MCP method
  -> pkg/mcp transport executes upstream JSON-RPC call or stream
  -> pkg/mcp/service writes client-facing MCP response
```

For a remote Streamable HTTP upstream, the outbound path should look like:

```text
pkg/mcp/service
  -> pkg/mcp/streamablehttp client
  -> HTTP request with Authorization and MCP headers
  -> upstream MCP server
  -> response and optional session ID
  -> session state cached by gateway
```

For a local stdio upstream, the outbound path should look like:

```text
pkg/mcp/service
  -> pkg/mcp/stdio client
  -> spawn or connect local MCP process
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
- richer filter/policy editing for `mcp_services` and `mcp_routes`

Recommended response metadata:

- connection status
- last error
- resolved transport type
- session status
- discovered capabilities

## 9. Route Model

Do not model MCP servers as `llm.providers.*`.

Do not model MCP endpoints as `llm_api` handler types.

Instead, add a separate MCP gateway route model.

Recommended package:

- `pkg/gateway/mcproute/`

Recommended route fields:

- `id`
- `match.host`
- `match.path_prefix`
- `upstream_client_id`
- `require_virtual_key`
- optional policy fields
  - allowed_tools
  - denied_tools
  - allowed_resource_prefixes
  - audit_mode

Reason:

- MCP routing is upstream-client oriented, not model-routing oriented

## 10. Session Model

The gateway should be stateful about upstream MCP sessions even if the client-facing API remains HTTP-based.

Recommended session ownership:

- `pkg/mcp/service` owns gateway-visible session lifecycle
- `pkg/mcp` transport client owns transport-visible session handles such as `Mcp-Session-Id`

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

These controls should live in `pkg/mcp/service`, not inside transport implementations.

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

## 13. MVP Recommendation

The first shippable MCP gateway should be intentionally narrow.

### 13.1 MVP Scope

Support:

- inbound HTTP MCP endpoint
- one upstream transport: remote Streamable HTTP
- config-store-backed MCP client definitions
- gateway VirtualKey validation
- pass-through JSON-RPC methods
- audit logging for `tools/call`

Defer:

- `stdio`
- legacy SSE upstream compatibility
- advanced tool policy
- multi-upstream fan-out

### 13.2 MVP Internal Shape

Recommended first implementation units:

- `caddy/dispatcher/` with `agent_route_dispatcher { mcp }`
- `pkg/mcp/service/`
- `pkg/mcp/transport/streamablehttp.go`
- `pkg/gateway/mcproute/`
- `pkg/mcp/runtime/`
- admin CRUD and runtime inspection for `mcp_services`, `mcp_routes`, and dispatcher state

### 13.3 MVP Non-Goals

The first version should not try to:

- reuse `agent_route_dispatcher`
- reuse `pkg/llm/provider`
- support every historical MCP transport variant
- implement transparent reverse proxy fallback

## 14. Evolution Path

After the MVP is stable, the recommended expansion order is:

1. add `stdio` upstream support
2. add compatibility support for legacy SSE-based upstream MCP servers
3. add tool, resource, and prompt policy controls
4. add richer session inspection and debugging endpoints

## 15. Final Recommendation

For this repository, the correct default architecture is:

- Caddy module for inbound serving
- runtime MCP gateway service for protocol and policy handling
- forward MCP clients for outbound execution

In short:

- use Caddy as the ingress host
- do not use reverse proxy as the core MCP abstraction
