# External Agent Callback Correlation

Capability status: **Proposed**.

Ordinary W3C trace propagation is implemented; the authenticated
callback-attribution contract defined here is not.

## 1. Purpose

The gateway already attributes AgentRoute ingress to the target Agent and
propagates W3C trace context plus `X-Agent-Depth` to HTTP Agents. Builtin inner
LLM/MCP calls remain in-process and inherit the turn context directly. The
remaining gap is trustworthy correlation when an externally running ACP or
HTTP Agent later calls an LLM, MCP, or Agent route through the gateway.

This design defines the requirements for that callback identity without
changing the event storage and query contracts in
[Observability Design](observability.md).

## 2. Current Capability

Three levels must remain distinct:

1. **Agent attribution** identifies the configured Agent that owns a gateway
   turn or native A2A request.
2. **Trace correlation** connects cooperating processes through W3C
   `traceparent` and `tracestate`.
3. **Authenticated callback correlation** proves that a later resource call
   belongs to a particular Agent session and turn.

Current runtime behavior is:

| Runtime | Agent attribution | Trace propagation | Authenticated callback correlation |
|---|---|---|---|
| builtin | exact `agent_id` on the turn and inherited child spans | in-process parent/child context | inherent to the in-process call path |
| HTTP | exact `agent_id` on Path A and Path B; both paths use `a2a_usage_events` | current interaction span injected southbound | not implemented for later callbacks |
| ACP | exact `agent_id` on the turn and ACP runtime events | gateway turn context exists; adapter behavior varies | not implemented for later callbacks |

A VirtualKey may identify permitted routes but does not by itself prove which
Agent turn caused a callback. Static process environment variables are not
turn context, and caller-supplied Agent/session ids are not trusted evidence.

## 3. Required Correlation Model

A conforming callback contract carries a gateway-issued, authenticated context
containing at least:

- `agent_id`;
- canonical gateway `session_id` when one exists;
- one stable logical turn id distinct from the per-attempt `run_id`;
- trace and parent-span context;
- issued-at and expiry bounds;
- an audience or resource scope preventing replay to unrelated routes.

The context is correlation evidence, not general authorization. VirtualKey and
route policy still decide whether the callback may access the target resource.
A valid context augments the accepted interaction with causal dimensions; it
must never broaden route access.

The carrier must be integrity protected, bounded, short-lived, redacted from
logs, and rejected across Agent or audience boundaries. Raw correlation tokens
must not be stored in usage tables.

## 4. HTTP Runtime Contract

Path A and Path B already inject normalized W3C trace context and incremented
Agent depth on calls to the remote Agent. An authenticated callback design may
add a dedicated context carrier to those southbound requests.

A conforming HTTP Agent returns that carrier only on callbacks caused by the
active turn. The gateway validates it before applying Agent/session/turn
attribution. Missing context leaves the callback independently authorized and
traceable but only agent-attributed when another unambiguous ownership rule
exists. Invalid context never silently becomes trusted attribution.

The carrier is separate from the HTTP Agent's `auth_ref`: `auth_ref` is the
credential the gateway uses to call the Agent, not a principal the Agent uses
to call gateway resources.

## 5. ACP Runtime Contract

ACP processes are long-lived and may serve multiple sessions, so static process
environment cannot carry per-turn identity. Correlation must be bound for the
duration of `session/prompt` through an explicit adapter capability.

Preferred adapters apply the current context to their gateway LLM/MCP clients.
A local forwarding proxy is acceptable only for opaque adapters and only when
its lifecycle, concurrency, cancellation, and stale-context behavior are
provably bounded. An adapter unable to propagate context remains supported but
must report correlation as unavailable rather than guessed.

## 6. Persistence And Query Requirements

If authenticated callback correlation is adopted, common interaction
dimensions gain nullable canonical session, logical-turn, and bounded
correlation-status fields. These dimensions apply consistently to typed LLM,
MCP, ACP, builtin, and A2A event projections.

Indexes must support Agent+session and logical-turn queries without indexing
raw authentication material. A logical-turn query returns the outer turn plus
its proven callbacks and excludes unrelated calls from the same Agent.
Interaction storage remains metadata-only and does not become a transcript
store.

## 7. Failure And Security Rules

- Forged, expired, wrong-Agent, wrong-audience, and replayed contexts are never
  accepted as attribution.
- Optional correlation failure may discard the claimed relationship while
  preserving independently authorized traffic; a route requiring correlation
  fails closed.
- A missing carrier never causes the gateway to infer a session or turn from
  timing, process identity, or a reused VirtualKey.
- Non-conforming runtimes remain usable and expose an honest unavailable or
  agent-only correlation state.
- Prompt, response, transcript, secret, and raw carrier content are never added
  to usage storage.

## 8. Open Design Decisions

A separate implementation plan requires decisions on:

- carrier format and signing-key ownership;
- rotation, maximum TTL, replay defense, and route audience representation;
- optional versus required-correlation route policy;
- canonical session identity when a runtime exposes both thread and session;
- logical turn-id format and relationship to `run_id`;
- which ACP adapters can propagate per-turn context without a forwarding proxy;
- retention and cardinality limits for session and turn indexes.

## 9. Related Documents

- [Observability Design](observability.md)
- [Agents Control Plane](agents-control-plane.md)
- [HTTP Agent Runtime](http-agent-runtime.md)
- [Builtin Agent Runtime](builtin-agent-runtime.md)
- [ACP Runtime Architecture](../architecture/acp-architecture.md)
