# HTTP Agent Runtime Architecture

## Scope

This page describes the implemented `runtime.type = "http"` architecture.
HTTP Agents are remote services that own their process and task lifecycle. The
gateway discovers their A2A 1.0 Agent Card, publishes an immutable execution
snapshot, and exposes either a translated common Agent turn or governed native
A2A ingress.

The durable protocol and security decisions are defined in
[HTTP Agent Runtime](../design/http-agent-runtime.md). This page describes how
the checked-in components realize those decisions.

## Components

```text
Agent definition (runtime.type=http)
  -> pkg/agent.Manager
  -> pkg/gateway.HTTPRuntimeManager
       -> fetch and validate Agent Card
       -> select same-origin JSONRPC interface
       -> resolve auth_ref through credential manager
       -> publish one immutable generation
            - Path B execution target
            - Path A proxy target and public Card template

AgentRoute(protocol=agent) -> pkg/gateway.HTTPBackend -> pkg/a2a/client
AgentRoute(protocol=a2a)   -> pkg/dispatcher A2A path -> pkg/a2a/proxy
```

`pkg/a2a` is a lower protocol package and does not depend on `pkg/agent`:

- `card` parses remote cards, selects interfaces and security alternatives,
  and rewrites the gateway-served public card;
- `jsonrpc` owns bounded envelope inspection, A2A method names, service
  parameters, and SSE validation;
- `client` is the typed A2A client used by translated Path B turns;
- `proxy` is the HTTP-terminated body and SSE pipe used by native Path A.

## Configuration And Publication

An HTTP Agent stores:

- `runtime.type: http`;
- `runtime.http.card_url`;
- `runtime.http.protocol: a2a`;
- an optional `runtime.http.auth_ref` owned by that Agent;
- optional timeout and public-card metadata.

`HTTPRuntimeManager` is the sole definition listener for HTTP execution state.
For every enabled HTTP Agent it fetches and validates the card, selects an A2A
1.0 JSON-RPC interface, verifies that one advertised security alternative can
be satisfied, and builds both path views in one immutable generation. A bad
Agent is isolated as non-ready without preventing unrelated accepted Agents
from publishing.

The manager keeps credential material live rather than copying a secret into
the snapshot. Credential creation, rotation, disablement, deletion, and OAuth
refresh update readiness or request-time authentication through the credential
manager. Card-input changes rebuild the affected entry; unrelated definition
changes reuse accepted snapshots.

## Path B: Common Agent Turn

Path B lets callers use the runtime-neutral Agent API:

```text
POST /<agent-route>/turn
  -> dispatcher route match and VirtualKey admission
  -> runtime registry resolves HTTPBackend
  -> HTTPRuntimeManager.ResolveExecution(agent_id)
  -> pkg/a2a/client SendMessage or SendStreamingMessage
  -> A2A task/message/status/artifact events mapped to common turn events
  -> ordered SSE response
```

The backend keeps bounded, process-local session bindings between the gateway
session and the A2A `contextId` plus an interrupted `taskId`. It supports
request-bound execution, cancellation, input-required continuation, and
authentication-required failure mapping. It does not turn the gateway into an
A2A task store.

## Path A: Native Governed A2A

A `kind=agent`, `protocol=a2a` route targets the same HTTP Agent but remains
native A2A on both sides. The dispatcher owns two exact route surfaces.

### Agent Card

```text
GET /<agent-route>/.well-known/agent-card.json
  -> route match
  -> HTTPRuntimeManager.ResolveProxyTarget(agent_id)
  -> serve the snapshot-owned rewritten card
```

Card GET is public by design. The served card points back to the matched
gateway route, advertises only JSON-RPC, drops remote signatures, disables push
notifications and extended-card discovery, and advertises gateway bearer
security only when the route requires a VirtualKey.

### JSON-RPC

```text
POST /<agent-route>
  -> content type, size, envelope, id, version, and method checks
  -> VirtualKey and rate-limit admission when required
  -> proxy readiness and interface-tenant validation
  -> ingress credential removal and auth_ref credential injection
  -> pkg/a2a/proxy forwards unchanged JSON-RPC bytes
  -> bounded JSON response or validated, incrementally flushed SSE
```

The native path admits the six request methods defined by the design and
rejects push-configuration operations. JSON-RPC notifications receive HTTP
204 after bounded rejection accounting. Gateway-generated JSON-RPC failures
use the original request id whenever the envelope supplied a valid id.

## HTTP And Security Boundary

Both paths force `A2A-Version: 1.0` southbound and inject normalized W3C trace
context plus incremented `X-Agent-Depth`. They do not forward the caller's raw
trace headers, `Authorization`, `x-api-key`, cookies, proxy credentials, or
hop-by-hop headers. An `auth_ref` credential is injected only for the selected
remote interface.

Redirects are rejected so credentials cannot move to another origin. Card and
interface selection is same-origin and JSON-RPC-only. Request bodies,
non-stream responses, SSE events, and whole streams are bounded; the total
request deadline and a 60-second body idle timeout apply to both buffered and
streaming responses.

## Observability

Both paths use the dispatcher interaction span and stamp `agent_id` and
`runtime_type=http`. Path B keeps `route_protocol=agent`; Path A uses
`route_protocol=a2a`. The current interaction span id is injected southbound,
so a remote Agent span can use it as its parent.

Path A has no token contract. Its request-level events are stored in
`a2a_usage_events`, participate in unified interaction queries and OTLP export,
and increment bounded Prometheus counters. Path B uses the common Agent event
accounting while retaining the HTTP runtime dimension.

## Failure And Readiness

AgentRoute creation and update fail closed when a native A2A target is not
proxy-ready. Path B capability resolution fails closed when its execution view
is unavailable. Runtime failures in one Agent do not invalidate other entries
in the manager generation.

Path A returns governed HTTP or JSON-RPC errors according to the point of
failure. A streaming response can become a JSON-RPC error only before the first
validated SSE record commits the downstream response; later failures terminate
the stream and mark the interaction failed.

## Current Limits

- A2A Protocol 1.0 JSON-RPC is the only southbound binding.
- A2A v0.3, REST, gRPC, and consumed push webhooks are unsupported.
- Path B accepts text input through the common turn contract and is
  request-bound.
- Session/task bindings are process-local and do not survive restart or move
  across replicas.
- Native A2A ingress targets HTTP Agents; it does not reverse-translate ACP or
  builtin Agents into A2A.
