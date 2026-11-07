# MCP route sharing and attribution

Capability: Proposed. Sharing is explicitly handed off pending service-session
isolation. Current MCP route uniqueness checks and attribution behavior remain.

## Audited current contracts

Agent MCP route declarations are exclusive at management write time
(`pkg/agent/manager.go`, checkMCPRouteUniqueness). The snapshot index drops ambiguous
route ownership instead of selecting a winner (`snapshot.go`, buildAttributionIndex).
LLM route dependencies are already shared and are outside this constraint.

An interaction with trusted Agent identity retains that stamp. Current write-time
fallback is MCP-only: untagged events may resolve to the single declared route
owner. Agent query filters also allow untagged MCP route history through their
RouteIDs fallback. These current fallbacks must change before sharing; current
exclusive declarations do not establish the actual caller of an untagged request.

Resource references, authorization and attribution are separate contracts. A
reference is a dependency or management declaration, not execution authority.
VirtualKey and route checks remain required, and no caller-supplied Agent header,
service ID, session ID or permission token establishes trusted identity.

## Concrete isolation constraint and handoff

`pkg/mcp/service/session.go` stores both discovery sessions and GatewaySession
metadata by service ID. `operations.go`, callMethodWithRetry, obtains that same
cached discoveryClient for tools/call, resources/read and prompts/get. Streamable
HTTP, SSE and stdio transports carry upstream session/process state. The dispatcher
passes route.ServiceID; the in-process einotool bridge also calls by service ID.
Neither path partitions cached transport/session state by trusted Agent, VirtualKey
or client. Service authentication is also service configuration, not a per-Agent
credential selection boundary.

Consequently an attribution-only patch cannot prove that independent Agents or
clients do not share upstream state. Different routes pointing at the same service
already reuse that state; existing route uniqueness is an attribution rule, not a
complete security isolation guarantee. This refactor does not expand that support
claim. Simply removing uniqueness would conceal the missing isolation contract.

Handoff owner: MCP service/runtime maintainers, coordinating Agent and observability
maintainers; destination: this Proposed design and pkg/mcp/service with higher assembly.

The future MCP session work belongs to the MCP service runtime and higher assembly,
with attribution changes coordinated with the Agent/observability surfaces. Keep
lower MCP packages independent of pkg/agent: inject a neutral, trusted execution
scope rather than importing Agent definitions. The implementation decision is
explicitly handed off until the following contracts and tests exist.

## Required sharing contract

A neutral scope must distinguish Agent execution, direct authenticated traffic and
operator discovery. It must include trusted caller dimensions and relevant service
configuration/credential generations. A client-supplied upstream session identifier
cannot substitute for ownership proof. Stateful sessions/processes cannot cross
Agent, VirtualKey or client boundaries. Discovery may be shared only when its
upstream state and credentials are explicitly proven safe; execution must not
borrow discovery's session merely because the schemas match.

Define transport-specific lifetime, capacity, concurrent occupancy, eviction,
credential rotation, permissions/progress ownership, reconnect and shutdown rules.
Restart or lost ownership fails closed. Reconnection must preserve the chosen
scope; existing retry behavior cannot move an operation into another scope.
Do not merge ACP owner proofs or HTTP A2A claims into a generic session manager.

New events use trusted Agent identity from execution context. Direct traffic
without that identity remains route-level even if an Agent declares the route.
Remove observer owner fallback and query-time route fallback together when sharing
lands. Historical events with stored Agent identity remain queryable by that
identity. Historical untagged events remain route-level/unknown; never rewrite them
or assign them to a current Agent because it now references a route. No SQLite
backfill may infer historical ownership from today's configuration.

Only after service isolation is proven may management accept shared MCP route
references and drop the exclusive attribution index. Route deletion still rejects
remaining Agent dependency references under the coordinated mutation lock.
Authorization is evaluated independently of reference ownership and usage tags.

## Acceptance before implementation can be enabled

- Two Agents sharing one route/service use distinct stateful sessions, with the
  same guarantees for different VirtualKeys and clients of either Agent.
- Direct MCP requests, builtin tools and operator discovery cannot resume each
  other's sessions or permissions, including after eviction, reconnect or restart.
- Credential changes retire only the appropriate scope/generation and never
  transfer secrets or upstream session state between callers.
- Trusted Agent events, direct route-level events and ambiguous historical events
  retain the specified attribution and query behavior.
- Streamable HTTP, SSE and stdio tests cover session binding, concurrent use,
  progress/cancellation ownership, retry, capacity, retirement and shutdown.

This document is a durable handoff, not an implemented sharing capability or a
promise that current service-scoped execution provides caller partitioning.
