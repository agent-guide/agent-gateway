# MCP protocol

`pkg/mcp` defines protocol primitives; `transport/` owns upstream wire transports
and `tracking/` owns ingress request/progress/history tracking. Connector models,
management and the builtin bridge live in `pkg/connector`; upstream MCP execution,
configuration and session resources live in `pkg/connector/mcp`;
read its AGENTS.md before changing those implementations or cross-cutting adapters.

MCP publishing routes retain kind/protocol `mcp` and target `connector_id`.
Protocol packages must not depend on Agent definitions. Upstream MCP sessions
remain connector-scoped; do not claim caller-isolated sharing or remove Agent
MCP-route uniqueness without implementing docs/design/mcp-route-sharing.md.
