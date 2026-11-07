# pkg/mcp — AGENTS.md

Scope: the MCP service runtime (`pkg/mcp/service/`) and the eino tool bridge
(`pkg/mcp/einotool/`). Paths are repository-root relative; the root
`AGENTS.md` global rules apply.

- The MCP architecture (service config, discovery, execution, dispatcher
  integration) is documented in `docs/architecture/mcp-architecture.md`.
- MCP routes live in `pkg/gateway/mcproute/` — see `pkg/gateway/AGENTS.md`.
- `pkg/mcp/einotool`: presents gateway-managed MCP service tools
  (`pkg/mcp/service`) as eino `InvokableTool`s; selecting tools by name is
  fail-closed (a missing tool is an error, not a silent skip). It is one of
  the PB0 prerequisites of the builtin agent runtime — see
  `docs/design/builtin-agent-runtime.md` §11.

MCP service discoveryClient is currently service-scoped and reused for execution.
It is not partitioned by Agent, VirtualKey or client. Do not remove Agent MCP-route
uniqueness or advertise caller-isolated sharing without the session/credential
work and tests specified in docs/design/mcp-route-sharing.md (Proposed handoff).
