# MCP Tool Policy Design

## 1. Scope

This document describes the design for MCP tool policy in `agent-gateway`.

Tool policy is gateway-level control over which MCP tools are exposed to downstream clients and how their definitions are presented. It lives on the MCP route, not on the MCP service, allowing the same upstream service to be exposed through different routes with different tool sets and descriptions.

This allows operators and agent builders to:

- enforce tool allowlists or denylists at the gateway edge without modifying upstream MCP servers
- reduce client-facing token overhead by controlling and rewriting MCP tool definitions
- expose gateway-executed synthetic tools that are more token-efficient or more observable than raw upstream tools
- unify tool naming across different upstream services

This document extends the MCP gateway architecture described in `architecture/mcp-architecture.md`. Observability for tool policy executions is defined in `observability.md`; the `presented_tool_name`, `executed_tool_name`, and `execution_mode` audit fields on `MCPUsageEvent` capture policy attribution.

The current implementation baseline is:

- MCP routing and dispatch through `pkg/dispatcher/mcp_handler.go` and `pkg/mcp/service`
- MCP route config in `pkg/gateway/mcproute/`
- Admin API in `pkg/admin`

## 2. Goals

- Allow MCP routes to declare a tool policy that filters and rewrites tool definitions before they reach the client
- Support allowlist and denylist filtering applied to both `tools/list` responses and `tools/call` enforcement
- Support description override to reduce token overhead from verbose upstream tool definitions
- Support tool name aliasing so routes can expose tools under different names without changing the upstream
- Enable gateway-executed synthetic tools as a later phase, where the gateway exposes and runs its own tool implementation

## 3. Non-Goals

This design does not attempt to:

- apply tool policy to LLM-level function calling (request-side tools in LLM API payloads)
- implement tool policy enforcement at the MCP service level; policy is always route-level
- implement synthetic tool execution in phase 2; that is a later phase

## 4. Capability Overview

MCP routes may declare a `tool_policy` that controls what the gateway exposes to downstream clients.

Phase 2 supports three policy controls:

- **Tool filtering**: allowlist or denylist of tool names. Applied to `tools/list` responses and enforced on `tools/call` requests.
- **Description override**: replace upstream tool description with a shorter or more precise gateway-defined description. Reduces per-request token overhead when tool definitions are large.
- **Tool name alias**: expose a tool under a different name than the upstream provides. The gateway maps back to the upstream name on `tools/call`.

Later phases extend route-level policy into route-level tool replacement, where the gateway exposes a synthetic tool name to the client and executes gateway-owned logic instead of forwarding the call to the original upstream tool.

## 5. Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                        Inbound HTTP                              │
│               http.handlers.agent_route_dispatcher               │
└────────────────────────┬─────────────────────────────────────────┘
                         │ MCP dispatch
                         ▼
                ┌─────────────────────┐
                │ pkg/dispatcher      │
                │ mcp_handler.go      │
                │                     │
                │  resolve MCPRoute   │
                │  (owns tool_policy) │
                └────────┬────────────┘
                         │
              ┌──────────┴──────────────┐
              │ tools/list              │ tools/call
              ▼                         ▼
   ┌──────────────────────┐   ┌──────────────────────┐
   │ pkg/mcp/service      │   │ pkg/mcp/service      │
   │ ListToolsForRoute()  │   │ CallToolForRoute()   │
   │  1. fetch upstream   │   │  1. resolve alias    │
   │  2. apply filter     │   │  2. check allow set  │
   │  3. apply overrides  │   │  3. forward / deny   │
   └──────────┬───────────┘   └──────────┬───────────┘
              │                           │
              ▼                           ▼
   filtered + rewritten list        upstream call or
   returned to client               -32601 error
```

Policy ownership is route-level. The dispatcher always carries the selected `MCPRoute` into policy evaluation. The same upstream service may be exposed through multiple routes with different tool policies.

## 6. Policy Model

### 6.1 Configuration Shape

`tool_policy` is a field on `MCPRouteConfig`. It is absent by default, which means all upstream tools are passed through unchanged.

```json
{
  "id": "mcp:fs-service:/mcp/fs",
  "kind": "mcp",
  "service_id": "fs-service",
  "match_policy": {
    "path_prefix": "/mcp/fs"
  },
  "tool_policy": {
    "mode": "allow",
    "tools": ["read_file", "list_directory", "search_files"],
    "overrides": {
      "read_file": {
        "description": "Read a file. Path must be absolute.",
        "name": "read_file"
      },
      "list_directory": {
        "description": "List entries in a directory."
      },
      "search_files": {
        "name": "find_files"
      }
    }
  }
}
```

Fields:

- `mode`: `"allow"` or `"deny"`. With `"allow"`, only listed tools are exposed. With `"deny"`, listed tools are hidden.
- `tools`: list of upstream tool names subject to the mode.
- `overrides`: per-tool patches applied to the tool definition after filtering. Each entry may contain `name` (alias) and/or `description` (replacement text).

### 6.2 Filtering

Filtering applies to `tools/list` responses from the upstream. The gateway removes tools that do not satisfy the policy before returning the list to the client.

For `tools/call`, the gateway checks the requested tool name against the effective allow set before forwarding. If the tool is not allowed, the gateway returns a JSON-RPC error with code `-32601` rather than forwarding the request.

### 6.3 Description Override

When an override entry contains a `description` field, the gateway replaces the upstream `description` in the tool definition with the provided text. The upstream tool schema (`inputSchema`) is unchanged.

This allows operators to supply shorter descriptions for verbose upstream tools without modifying the upstream server. Shorter descriptions reduce the token cost of carrying tool definitions in every LLM API request.

### 6.4 Tool Name Aliasing

When an override entry contains a `name` field, the gateway exposes the tool under the alias in `tools/list`. When the client calls the alias via `tools/call`, the gateway maps it back to the upstream tool name before forwarding.

The mapping is maintained per-route at runtime. Alias names must be unique within the effective tool set for a route.

Example upstream `tools/list` result:

```json
{
  "tools": [
    {
      "name": "search_files",
      "description": "Search files recursively with many options."
    },
    {
      "name": "read_file",
      "description": "Read a file."
    }
  ]
}
```

Example route policy:

```json
{
  "tool_policy": {
    "mode": "allow",
    "tools": ["search_files", "read_file"],
    "overrides": {
      "search_files": {
        "name": "find_files",
        "description": "Find files by name or content."
      }
    }
  }
}
```

Client-visible `tools/list` result after policy application:

```json
{
  "tools": [
    {
      "name": "find_files",
      "description": "Find files by name or content."
    },
    {
      "name": "read_file",
      "description": "Read a file."
    }
  ]
}
```

If the client later calls:

```json
{
  "method": "tools/call",
  "params": {
    "name": "find_files",
    "arguments": {
      "query": "TODO"
    }
  }
}
```

the gateway resolves `find_files` back to upstream tool `search_files` and forwards:

```json
{
  "method": "tools/call",
  "params": {
    "name": "search_files",
    "arguments": {
      "query": "TODO"
    }
  }
}
```

This is aliasing, not replacement:

- the client sees `find_files`
- the upstream still executes `search_files`
- the result comes from the upstream tool's normal execution path

The main use cases are:

- shorten verbose or awkward tool names
- unify naming across different upstream services
- improve model-facing clarity without changing execution ownership

### 6.5 Execution Points

Policy ownership is route-level, so the dispatcher must carry the selected route into policy evaluation.

Execution flow:

- `pkg/dispatcher/mcp_handler.go`: resolve the route, then call policy-aware helpers using both `route` and `service_id`
- `pkg/mcp/service/`: host reusable pure functions for filtering, alias resolution, and override application
- transport clients remain unchanged

Route-aware methods on the service manager:

- `ListToolsForRoute(ctx, route, cursor)`
- `CallToolForRoute(ctx, route, clientToolName, args, progressCh)`

The service manager internally delegates to lower-level service-only methods after policy resolution. This preserves the design goal that one upstream service may be exposed through multiple routes with different policies.

### 6.6 Future Extension: Tool Replacement

The phase 2 policy is limited to filtering, aliasing, and description rewrite. That is enough to reduce token overhead and improve governance, but it is not yet full tool replacement.

If the gateway later needs to replace an upstream tool with a gateway-implemented or gateway-wrapped tool, that requires a separate execution model:

- a route-visible synthetic tool registry
- explicit audit attribution of `presented_tool_name` versus `executed_tool_name`
- optional comparison fields for token savings or alternate implementation path

That work is intentionally out of scope for phase 2.

### 6.7 Synthetic Tool Replacement

This section defines the next-step design for gateway-executed synthetic tools. This is the mechanism needed when aliasing is not enough.

Aliasing only changes the presented name and optionally the description. It does not change who executes the tool or what result shape is returned. If the operator wants a code agent to call a more token-efficient `well_git` tool instead of a broad raw `git` tool, the gateway must expose and execute a different tool, not merely rename one.

#### 6.7.1 Problem Shape

Typical motivating case:

- a client or upstream service currently exposes a broad tool such as `git`
- the raw tool output is verbose, unstable, or difficult to audit
- the operator wants the agent to call a narrower tool such as `well_git`
- the narrower tool should be executed by the gateway and should return a shorter, more structured result

The core goal is not just naming control. The goal is execution replacement with better result shaping.

#### 6.7.2 Client Compatibility

Gateway-executed synthetic tools are compatible with code agents as long as the client sees a normal MCP tool lifecycle:

1. the tool appears in `tools/list`
2. the client calls it through `tools/call`
3. the gateway returns a valid MCP tool result

From the client's perspective, this is a standard MCP tool. The client does not need to know whether the tool is:

- directly forwarded to an upstream MCP server
- wrapped around an upstream tool
- implemented entirely inside the gateway

This means a code agent will generally accept and use a gateway-executed synthetic tool if:

- the presented tool definition is valid
- the input schema matches the actual supported behavior
- the returned result is protocol-valid and semantically consistent with the tool definition

#### 6.7.3 Execution Modes

Route-level tool policy may eventually support three execution modes:

- `forwarded_tool`: normal case; the gateway forwards the call to the upstream tool
- `wrapped_tool`: the gateway executes pre-processing or post-processing around an upstream tool call
- `synthetic_tool`: the gateway executes gateway-owned logic without forwarding to the original upstream tool

#### 6.7.4 Presentation And Execution Separation

Synthetic replacement requires two names:

- `presented_tool_name`: the name shown to the client and returned in `tools/list`
- `executed_tool_name`: the internal execution target, which may be an upstream tool name or a gateway synthetic tool identifier

For pure aliasing:

- `presented_tool_name` differs from the upstream name
- `executed_tool_name` still resolves to the original upstream tool

For synthetic replacement:

- `presented_tool_name` is what the agent calls, such as `well_git`
- `executed_tool_name` resolves to a gateway-owned executor, not the original raw tool

This distinction must be reflected in audit records so operators can answer both questions:

- what tool the agent believed it was calling
- what implementation actually executed

#### 6.7.5 Example Flow

Upstream capability:

```json
{
  "tools": [
    {
      "name": "git",
      "description": "Run arbitrary git commands"
    }
  ]
}
```

Route policy exposes only a synthetic tool:

```json
{
  "tool_policy": {
    "mode": "deny",
    "tools": ["git"]
  },
  "synthetic_tools": [
    {
      "name": "well_git",
      "description": "Return concise repository summaries for agent use.",
      "input_schema": {
        "type": "object",
        "properties": {
          "action": {
            "type": "string",
            "enum": ["status_summary", "changed_files", "diff_summary", "recent_commits"]
          },
          "paths": {
            "type": "array",
            "items": { "type": "string" }
          }
        },
        "required": ["action"]
      },
      "executor": {
        "kind": "synthetic_tool",
        "id": "well-git"
      }
    }
  ]
}
```

Client-visible `tools/list` result:

```json
{
  "tools": [
    {
      "name": "well_git",
      "description": "Return concise repository summaries for agent use."
    }
  ]
}
```

Client call:

```json
{
  "method": "tools/call",
  "params": {
    "name": "well_git",
    "arguments": {
      "action": "changed_files"
    }
  }
}
```

Gateway execution:

- resolve `well_git` to synthetic executor `well-git`
- run gateway-owned logic
- optionally call local command execution, a bounded adapter, or another internal service
- return a compact, structured MCP tool result

Example result:

```json
{
  "content": [
    {
      "type": "text",
      "text": "{\"action\":\"changed_files\",\"count\":2,\"files\":[\"pkg/dispatcher/mcp_handler.go\",\"docs/design/mcp-tool-policy.md\"]}"
    }
  ]
}
```

#### 6.7.6 Result Shape Guidance

Synthetic tools should prefer stable, compact, structured outputs over raw command output.

Good result properties:

- bounded size
- stable field names
- task-oriented summaries rather than terminal dumps
- enough detail for follow-up reasoning without replaying the full raw output

For `well_git`, that usually means returning summaries such as:

- changed files
- diff summary by path
- recent commits
- working tree status summary

It should not default to returning unrestricted `git diff` or `git status --verbose` output unless the synthetic tool contract explicitly allows it.

#### 6.7.7 Audit Impact

Synthetic replacement extends the audit model defined in `observability.md`. The `MCPUsageEvent` already carries `presented_tool_name`, `executed_tool_name`, `execution_mode`, and `policy_action` fields from phase 1. The tool policy layer is responsible for populating these fields when it resolves the tool call:

- `presented_tool_name`: set to the client-provided tool name (the alias or synthetic name)
- `executed_tool_name`: set to the upstream tool name (for aliases) or synthetic executor ID (for synthetic tools)
- `execution_mode`: `forwarded_tool` | `wrapped_tool` | `synthetic_tool`
- `policy_action`: `forward` | `deny` | `alias` | `replace`

This preserves full observability when the agent-visible tool is not the same as the executed implementation.

#### 6.7.8 Scope And Phasing

Synthetic tool replacement is feasible in this architecture because the gateway already terminates the client-facing MCP session and is allowed to execute protocol-aware logic before returning a result.

Recommended phasing:

- phase 2: filtering, aliasing, description rewrite
- later phase: synthetic tool registry and gateway-executed tool replacement

That later phase should be treated as an extension of MCP tool policy, not as a small variation of aliasing.

## 7. Admin API

Tool policy is stored on the MCP route config. The existing MCP route CRUD endpoints in `/admin/mcp/routes/` are extended to accept and return the `tool_policy` field:

- `POST /admin/mcp/routes` — create route with optional `tool_policy`
- `PUT /admin/mcp/routes/{id}` — update route; replace or clear `tool_policy`
- `GET /admin/mcp/routes/{id}` — includes `tool_policy` in the response

No dedicated tool policy endpoints are needed in phase 2; the policy is a field on the route object.

For bundle workflows, the `tool_policy` field participates in the bundle schema, validation, and export as part of the MCP route config.

## 8. Package Structure

```
pkg/gateway/mcproute/
    tool_policy.go   ToolPolicy, ToolOverride types
    types.go         tool_policy field added to MCPRouteConfig and MCPRoute

pkg/mcp/service/
    tool_policy.go   route-aware filter, alias resolution, and override helpers
                     ListToolsForRoute(), CallToolForRoute()
```

`ToolPolicy` and `ToolOverride` are defined in `pkg/gateway/mcproute/` because they are part of the route config model. The pure helper functions for applying the policy live in `pkg/mcp/service/` because they operate on MCP protocol objects and may be reused across dispatch contexts.

## 9. Integration Points

`pkg/dispatcher/mcp_handler.go`:
- after resolving the `MCPRoute`, use `ListToolsForRoute(ctx, route, cursor)` for `tools/list` requests instead of the service-level `ListToolsPage`
- use `CallToolForRoute(ctx, route, clientToolName, args, progressCh)` for `tools/call` requests instead of `CallTool`
- the route-aware helpers apply filtering, alias resolution, and override application before delegating to the underlying service manager methods
- on `tools/call`, if the tool is denied, return JSON-RPC error `-32601` without forwarding

`pkg/mcp/service/manager.go`:
- keep service-only RPC methods (`ListToolsPage`, `CallTool`) unchanged
- add `ListToolsForRoute` and `CallToolForRoute` as route-aware wrappers that apply `ToolPolicy` before calling service-only methods
- later synthetic-tool phase: resolve presented tool names to forwarded, wrapped, or synthetic executors via a synthetic tool registry

`pkg/gateway/mcproute/types.go`:
- add `ToolPolicy *ToolPolicy` field to `MCPRouteConfig` and `MCPRoute`
- `ToolPolicy` is marshaled/unmarshaled as part of the route JSON stored in the config store

## 10. Implementation Order

### Phase 2: MCP Tool Policy (target scope)

Goal: route-level tool filtering and description overrides.

1. Add `ToolPolicy` and `ToolOverride` types to `pkg/gateway/mcproute/tool_policy.go`
2. Add `ToolPolicy` field to `MCPRouteConfig` and `MCPRoute` in `types.go`; update JSON marshal/unmarshal
3. Implement `ListToolsForRoute` in `pkg/mcp/service/tool_policy.go`: fetch upstream list, apply filter, apply overrides
4. Implement `CallToolForRoute`: resolve alias, check allow set, forward or return `-32601`
5. Update `pkg/dispatcher/mcp_handler.go` to call route-aware helpers for `tools/list` and `tools/call`
6. Add `tool_policy` to Admin CRUD for MCP routes (accept in POST/PUT, return in GET)
7. Add `tool_policy` to bundle schema, validation, and export

### Phase 5: Synthetic MCP Tools

Goal: allow the gateway to expose and execute synthetic tools such as `well_git`.

1. Define route-visible synthetic tool config (`SyntheticTool`, `SyntheticToolExecutor`) and validation
2. Implement `SyntheticToolRegistry` and `Executor` interface in `pkg/mcp/service/`
3. Merge synthetic tools into `tools/list` output in `ListToolsForRoute`
4. Extend `CallToolForRoute` to resolve `tools/call` to forwarded, wrapped, or synthetic execution
5. Populate `presented_tool_name`, `executed_tool_name`, and `execution_mode` on the `InteractionSpan` for synthetic execution paths (see `observability.md` §7.1)
6. Add admin and bundle support for synthetic tool definitions

## 11. Relationship To Existing Documents

`observability.md`:
- the `MCPUsageEvent` in that document carries `presented_tool_name`, `executed_tool_name`, `execution_mode`, and `policy_action` fields that this tool policy layer is responsible for populating
- the `InteractionObserver` / `InteractionSpan` interface defined there is used by `CallToolForRoute` to record policy-attributed audit events

`architecture/mcp-architecture.md`:
- §11 Security And Policy in that document describes tool allow/deny lists as future work
- those items are now defined concretely in this document
- `architecture/mcp-architecture.md` remains the primary reference for MCP gateway architecture and transport design
