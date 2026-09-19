# HTTP Agent Runtime

## 1. Purpose

This document is the authoritative product and technical design for
`runtime.type = "http"`: agents that are remote HTTP services owning their own
lifecycle, with the gateway acting as a client (translating ingress) or a
governed proxy (non-translating ingress).

Capability status: **Implemented**. HTTP Agents execute through the common
`protocol agent` `/turn` ingress using A2A 1.0 JSON-RPC southbound, or through
native governed `protocol a2a` routes (Path A, §8).

The design records the direction chosen after evaluating alternatives:

- **A2A Protocol 1.0 JSON-RPC is the first southbound dialect**
  (`runtime.http.protocol = "a2a"`), replacing the previously deferred
  "versioned HTTP Agent contract". Adopting the standard removes the
  wire-contract blocker; the remaining work is mapping and governance, not
  protocol invention. The current scope does not dual-stack A2A v0.3.
- **The target architecture has two northbound paths** for the same agents.
  The implemented `protocol agent` route translates A2A into the common turn
  envelope (Path B). An implemented `protocol a2a` route proxies A2A JSON-RPC
  end-to-end without event translation (Path A). Both use the same ingress
  governance model and share the `pkg/a2a` protocol package as their common
  lower layer.
- **Path A is a governed JSON-RPC proxy, not a byte-identical L4 pipe.** HTTP
  is terminated, credentials are swapped, and the Agent Card is gateway-served.
  The request/response body and SSE frames are forwarded without reassembly.

## 2. Related documents

- [Agents Control Plane](agents-control-plane.md) owns the shared `Agent`
  identity, resources, policy, attribution, the runtime-backend contract, and
  the runtime-type axis. §5.4 of that document defines `http` as "the agent
  service owns its lifecycle"; this document keeps that definition and adds
  the executable detail.
- [HTTP Agent Runtime Architecture](../architecture/http-agent-architecture.md)
  describes the checked-in manager, backend, dispatcher, and protocol-package
  composition for both paths.
- [Builtin Agent Runtime](builtin-agent-runtime.md) is the sibling design for
  `runtime.type = "builtin"`.
- [`pkg/a2a/AGENTS.md`](../../pkg/a2a/AGENTS.md) owns the implemented
  protocol-package invariants; §5 records the package surface shared by both
  paths and their path-specific components.
- This document owns the `http` runtime: schema, southbound dialects,
  credential use, the A2A proxy ingress, the translating backend, and the
  `pkg/a2a` layering. Other documents should summarize and link here rather
  than duplicate the mapping tables.

## 3. Three axes, one runtime type

The naming question this design closes first: supporting A2A does **not**
introduce `runtime.type = "a2a"`. Runtime type, southbound dialect, and
northbound protocol are three separate axes:

| Axis | Values | Answers |
|---|---|---|
| `runtime.type` | `acp` / `http` / `builtin` | Who owns the agent's lifecycle? |
| `runtime.http.protocol` | `a2a` (`custom` reserved, undesigned) | What dialect does the gateway speak to the service? |
| AgentRoute `protocol` | `agent` / `a2a` / LLM family protocols | What protocol does the client speak to the gateway? |

`runtime.type = "http"` keeps its control-plane meaning — "an external
service owns its lifecycle; the gateway is a client over HTTP" — and every
A2A agent falls squarely inside it: A2A v1.0 JSON-RPC runs over HTTP(S) with
SSE streaming, the remote service holds its own task state, and the gateway
never manages a process. Adding a `runtime.type = "a2a"` would fold a dialect
into the lifecycle axis and require one runtime type per future dialect
(`responses`, `custom`, …), repeating exactly the axis-mixing the control-plane
design rejects for external processes (§5.4 "No bespoke external-process
backend").

Vocabulary stays layered: the model, Admin API, and code say `http` runtime
with `runtime.http.protocol = "a2a"`; product and user documentation may say
"connect A2A agents", which is true because registering an A2A agent is
creating one `http`-runtime Agent.

## 4. Southbound dialect: A2A Protocol 1.0

The locked specification is **A2A Protocol 1.0**. Normative source:
`specification/a2a.proto` and the published spec at
[a2a-protocol.org](https://a2a-protocol.org/latest/). The current scope speaks only the
**JSON-RPC 2.0** binding. HTTP+JSON/REST and gRPC interfaces advertised on a
remote Agent Card are ignored; if no JSON-RPC interface is present, the Agent
is not executable and Path A rejects the route.

v0.3 method names, kebab-case task states, `kind` event discriminators, and
the pre-v1.0 Agent Card shape (`url`, `preferredTransport`,
`protocolVersion` on the card itself) are not implemented and are not
accepted as a compatibility mode.

### 4.1 What v1.0 provides

- **Agent Card** at `/.well-known/agent-card.json`: identity, skills,
  capabilities (`streaming`, `pushNotifications`, `extendedAgentCard`),
  `securitySchemes` / `securityRequirements`, and `supportedInterfaces[]`
  (each with `url`, `protocolBinding`, `protocolVersion`). The JSON-RPC
  `protocolBinding` wire token is exactly `"JSONRPC"`; `"JSON-RPC"` is prose,
  not an accepted Card value. The v0.3 JSON field `security` is not a v1.0
  name.
- **Tasks** with states `TASK_STATE_SUBMITTED` → `TASK_STATE_WORKING` →
  interrupted (`TASK_STATE_INPUT_REQUIRED`, `TASK_STATE_AUTH_REQUIRED`) or
  terminal (`TASK_STATE_COMPLETED`, `TASK_STATE_FAILED`,
  `TASK_STATE_CANCELED`, `TASK_STATE_REJECTED`).
- **Messages** composed of a unified **Part** whose wire JSON contains exactly
  one top-level `text` / `raw` / `url` / `data` member — no `kind` field. The
  Go SDK represents this internally as `Part{Content: PartContent}` but its
  custom JSON codec flattens `Content`; implementations use the SDK helpers /
  type switch and must not expect a wire-level `content` wrapper.
- **Artifacts** as task outputs, streamable incrementally.
- **JSON-RPC methods**: `SendMessage`, `SendStreamingMessage`, `GetTask`,
  `ListTasks`, `CancelTask`, `SubscribeToTask`, `GetExtendedAgentCard`, and
  the push-notification config family. `contextId` groups tasks; `taskId`
  identifies one task.
- **Streaming events** discriminated by JSON member name (`statusUpdate`,
  `artifactUpdate`, `task`, `message`), not a `kind` field. Stream closure
  is the terminal signal; v1.0 removed `final` from status updates.
- **Push notifications** (webhooks) for disconnected clients.

### 4.2 What A2A does not provide, and what that costs this gateway

| A2A gap | Consequence |
|---|---|
| No usage/token reporting | Path B emits no `usage` events; metering stays gateway-side (request/turn level) |
| No structured tool-approval protocol (only generic `TASK_STATE_INPUT_REQUIRED`) | Path B reports `Permissions.Interactive = false`; §9.4 defines the resume-by-new-turn fallback |
| Opaque `contextId`, no transcript contract; `ListTasks` is not a session list | `SessionLister` / `TranscriptLoader` stay fail-closed (`capability_not_supported`) |
| No exact-run cancellation modes | `RunCanceller` supports force (`CancelTask`) only; graceful is unsupported |

The push-notification webhook is **not consumed**: the gateway itself holds
the client connection, which is precisely the disconnected-client problem
webhooks solve. Long-running tasks are the remote's concern. Path A forwards
`GetTask` and `SubscribeToTask`. Path B does not poll or resubscribe after
`ServeTurn` returns; it uses blocking/streaming southbound calls bounded by
the connect, response-header, idle-stream, and total-turn policy in §6.

### 4.3 v0.3 → v1.0 names used in this document

| Topic | v0.3 (do not implement) | v1.0 (this design) |
|---|---|---|
| Send | `message/send` | `SendMessage` |
| Stream | `message/stream` | `SendStreamingMessage` |
| Get / cancel / subscribe | `tasks/get`, `tasks/cancel`, `tasks/resubscribe` | `GetTask`, `CancelTask`, `SubscribeToTask` |
| Agent Card path | `/.well-known/agent-card` | `/.well-known/agent-card.json` |
| Task states | `"working"`, `"input-required"` | `"TASK_STATE_WORKING"`, `"TASK_STATE_INPUT_REQUIRED"` |
| Stream events | `kind: "status-update"` | member `statusUpdate` / `artifactUpdate` |
| Card endpoint | top-level `url` + `protocolVersion` | `supportedInterfaces[]` |
| Card security requirements | `security` (OpenAPI array) | `securityRequirements` (`repeated SecurityRequirement`) |

## 5. Shared protocol package (`pkg/a2a`)

A2A v1.0 is a **lower protocol package**, analogous to `pkg/acp` and
`pkg/mcp`. Both implemented paths reuse it. It is
not an "HTTP Agent runtime" package: it contains no `agentruntime.Backend`, no
dispatcher handler, no VirtualKey logic, and no `auth_ref` resolution.

```text
pkg/dispatcher (Path A handler)
pkg/gateway    (Path B HTTPBackend)
        \          /
         v        v
            pkg/a2a
```

`pkg/a2a` must not import `pkg/agent`, `pkg/dispatcher`, `pkg/gateway`, or
`pkg/credential`. Callers inject an `http.RoundTripper` (or `*http.Client`)
that performs live southbound auth, plus selected interface URL, timeout, and
body-cap options.

### 5.1 Package shape

```text
pkg/a2a/
  card/      fetch and parse interfaces/security alternatives (implemented);
             public Card rewrite helper (implemented)
  jsonrpc/   v1.0 envelope validation and method names (implemented);
             Path A allowlist/service-parameter/SSE helpers (implemented)
  client/    typed API used only by Path B (implemented)
  proxy/     HTTP-terminated body/SSE pipe used only by Path A (implemented)
```

| Layer | Current surface | Path A surface | Used by |
|---|---|---|---|
| `card/` | GET `/.well-known/agent-card.json`; parse and validate `"JSONRPC"` interface candidates; return structured Card security alternatives without credential policy | Rewrite the public URL and `securitySchemes` / `securityRequirements` from caller-supplied values | Both paths |
| `jsonrpc/` | JSON-RPC 2.0 envelope validation and local v1.0 method-name constants used by Path B guards/tests | Method allowlist, service-parameter parsing/injection, and bounded SSE copying | Both paths |
| `client/` | Thin wrapper over official `a2aclient` locked to JSON-RPC (`WithJSONRPCTransport` / `NewJSONRPCTransport`) | None | Path B only |
| `proxy/` | HTTP-terminated governed proxy | Copy request body and SSE upstream↔downstream with flush and body caps; enforce `A2A-Version`; do not re-encode or fetch/forward the remote Agent Card | Path A only |

Path A must **not** parse a JSON-RPC request into a typed `SendMessage` call
and re-marshal the response. That would drop unknown fields and extensions
and would turn Path A into a second translation path. The typed client exists
because Path B has to emit the common turn envelope; the proxy exists because
Path A must not.

Path A and Path B tests use bounded in-process `httptest` servers close to the
owning package. A shared JSON-RPC/SSE fake server may be extracted later if it
materially reduces duplication; it is not part of the protocol package API.

### 5.2 Official Go SDK

Pin `github.com/a2aproject/a2a-go/v2` at **v2.5.0** (not the unversioned
`github.com/a2aproject/a2a-go` path). Path B **uses** the official client
instead of hand-rolling JSON-RPC/SSE:

- `github.com/a2aproject/a2a-go/v2/a2a` — v1.0 domain types
- `github.com/a2aproject/a2a-go/v2/a2aclient` — Path B client, constructed
  with `WithDefaultsDisabled()` plus `WithJSONRPCTransport(httpClient)` (or
  `NewJSONRPCTransport`). JSON-RPC is a public transport. gRPC lives in
  `a2agrpc` and is **not** a mandatory `a2aclient` dependency; current scope must not
  import `a2agrpc` or enable REST.

It must **not** use:

- `a2asrv` as Path A — that package is an A2A executor. Path A is a governed
  proxy, not a task runtime. The SDK CLI's typed operation-forwarding proxy
  is the same class of translation this design rejects for Path A.
- Default factory transports — `NewFactory` registers JSON-RPC **and REST**
  unless defaults are disabled. The current scope must pin JSON-RPC only.
- The v0.3 compatibility packages. The v2.5.0 module graph contains the
  unversioned `github.com/a2aproject/a2a-go v0.3.15`, but current scope must not import or
  link it, `a2acompat/a2av0`, or `a2apb/v0`. Module-graph presence alone is
  expected and is not evidence that the compatibility layer is linked.

`pkg/a2a/proxy`, card rewrite, protocol-only security classification, and
`A2A-Version` enforcement stay gateway-owned. Final auth satisfiability stays
in `pkg/gateway`; none of these behaviors is provided as a verbatim HTTP/SSE
pipe by `a2aclient`.

### 5.3 Auth injection

```go
type DialOptions struct {
    InterfaceURL string       // selected, validated AgentInterface.url
    HTTPClient   *http.Client // caller-owned live-auth RoundTripper
    TotalTimeout time.Duration
    IdleTimeout  time.Duration
    MaxBodyBytes int64        // 4 MiB request/non-stream response
    MaxEventBytes int64       // 1 MiB per SSE event
    MaxStreamBytes int64      // 64 MiB per SSE response
}
```

The protocol package never sees `auth_ref`, VirtualKey material, or the
credential manager. The Path A handler and Path B backend inject a caller-owned
RoundTripper which resolves and, when needed, refreshes the referenced
credential on **every** southbound operation before attaching Bearer auth (see
§7). A prepared SDK client may retain that RoundTripper, but neither the client
nor an execution snapshot may retain the resolved secret. The public Agent Card
GET uses a separate unauthenticated client; redirects are not followed in current scope.
Both paths inject `A2A-Version: 1.0` on southbound HTTP (see §8.2). The injected
header is a protocol requirement, not credential material.

### 5.4 What stays out of `pkg/a2a`

- `HTTPBackend` / `ServeTurn` / common-envelope mapping (Path B, `pkg/gateway`)
- dispatcher, VirtualKey, rate limits, AgentRoute (Path A, `pkg/dispatcher`)
- credential manager and `auth_ref` (gateway adapters)
- Agent Card product policy (which Agent fields seed the card): the `card/`
  rewrite helper only rewrites values the caller supplies
- REST and gRPC bindings
- push webhooks, durable task mirroring, session/transcript emulation

### 5.5 Shared HTTP runtime snapshots

`pkg/gateway.HTTPRuntimeManager` is the single owner of immutable, Card-derived
HTTP execution state. The implemented manager owns both Path A and Path B views
and is registered exactly once as an Agent definition listener; `HTTPBackend`
composes it rather than owning a second snapshot map. It imports `pkg/a2a/card`,
constructs and owns `pkg/a2a/client` clients and `pkg/a2a/proxy` instances, and
exposes `ResolveExecution(agent_id)` and `ResolveProxyTarget(agent_id)`. `pkg/a2a`
keeps the lower-package dependency boundary in §5.

For every `runtime.type=http` Agent, prepare validates configuration and keeps
three distinct identities:

- the **Card-input fingerprint** contains normalized `card_url`, `protocol`,
  and Card-fetch transport policy. Only a new/changed Card-input fingerprint
  (or an entry with no accepted Card-derived state) requires a remote Card
  fetch;
- the **credential-eligibility fingerprint** contains no secret or refresh
  metadata. It is the tuple `auth_ref`, credential existence, credential type,
  normalized owner scope, disabled state, and whether the credential has a
  usable non-empty Bearer source (`strings.TrimSpace(Credential.APIKey()) != ""`;
  an expired OAuth token remains eligible because request-time refresh owns
  expiry). Secret/token bytes, expiry/refresh metadata, and `UpdatedAt` are
  deliberately excluded;
- the **definition-input fingerprint** combines those two fingerprints plus
  the Agent identity fields used by the `public_card_template`. An identity-only
  edit reuses cached Card state without fetching and rebuilds that template
  locally. The execution
  fingerprint additionally contains the selected Card interface URL/tenant
  and effective credential principal, so it decides resource and binding
  retirement but never whether a Card must be fetched.

For a new or Card-input-changed Agent, prepare fetches the remote Card.
`card/` returns ordered interface candidates plus structured security
alternatives (`anonymous`, `single_http_bearer`, or `unsupported`); it receives
neither `auth_ref` nor a credential resolver. The manager applies one
deterministic selection algorithm:

1. walk `supportedInterfaces` in source order and retain only entries whose
   `protocolBinding` is exactly `"JSONRPC"`, whose Major.Minor
   `protocolVersion` is `1.0`, whose URL is valid, and whose scheme and authority
   equal the configured `card_url`;
2. independently walk the Card-level `securityRequirements` alternatives in
   source order and retain the first alternative satisfiable by the configured
   `auth_ref` under §7.1;
3. select the first retained interface and retained security alternative; if
   either side has no survivor, publish the Agent non-ready.

The implemented selector supplies both Path A and Path B views; route
validation must not reimplement it. The current candidate snapshot is
equivalent to:

```text
agent_id -> {
  card_input_fingerprint, credential_eligibility_fingerprint,
  definition_input_fingerprint, execution_fingerprint,
  parsed_interface_candidates, parsed_security_alternatives,
  interface_url, tenant, auth_ref, transport_policy,
  selected_security, path_b_execution, public_card_template,
  path_a_proxy_ready, config_error
}
```

The snapshot contains no credential secret. It includes `public_card_template`
and `path_a_proxy_ready` in the same generation. The
route-neutral template is synthesized from the Agent's identity plus the
accepted remote Card's skills, modes, and safely proxyable capabilities;
signatures and remote interface/security values are excluded. It is cached for
the complete definition generation, so a Path A Card GET never fetches the
remote. Restart performs a new definition prepare; live health drift never
mutates an accepted generation.

Definition listeners receive the full Agent generation on every Refresh,
Create, Update, Delete, and Recommit, but HTTP preparation is incremental:

- an unchanged Card-input fingerprint with accepted Card-derived state reuses
  the parsed interface/security candidates and performs no Card request. The
  manager reapplies credential eligibility and selection locally, so a
  credential-only change can move the Agent between ready and non-ready
  without network I/O;
- an unchanged definition-input and execution fingerprint inherits its
  per-fingerprint resources. A secret/access-token rotation that preserves the
  credential-eligibility fingerprint also preserves clients, active runs, and
  session bindings: the live RoundTripper observes the new value on its next
  operation;
- disabled HTTP Agents publish a disabled entry without fetching a Card or
  constructing an A2A client. Only enabled new/Card-input-changed HTTP Agents
  and entries without accepted Card-derived state fetch Cards. They are fetched with bounded parallelism,
  not serially across the full Agent list. Fingerprinting, disabled handling,
  retry-backoff inheritance, and accepted-Card reuse happen before admission to
  that bounded I/O pool, so an unchanged entry can never time out waiting behind
  unrelated Card requests. A valid parsed Card is retained even
  when selection is currently non-ready only because `auth_ref` is absent,
  disabled, wrong-kind, wrong-owner, or has no usable secret;
- the listener retains the existing five-second total prepare budget from
  `pkg/agent/snapshot.go`. Each definition Card request uses the smaller of a
  four-second definition-fetch limit and the remaining prepare budget, leaving
  time for selection and publication. Health probes
  are outside definition prepare and retain their full ten-second limit;
- timeout or Card validation failure affects only the new/Card-input-changed
  entry, which is published non-ready. It never replaces unrelated inherited
  snapshots and it never falls back to the changed Agent's old Card state.
  An entry that exhausts the prepare budget while waiting for a Card-fetch slot
  retains its new input fingerprints and is marked as a Card-fetch failure so
  the same retry path applies.
  Because it has no accepted Card-derived state, a later generation retries it
  even when its local input fingerprint is unchanged. The manager also schedules
  an Agent/fingerprint-scoped retry starting at one second with exponential
  backoff capped at one minute. A retry triggers `AgentManager.Recommit`; it does
  not mutate the committed generation in place. While its retry timer is
  pending, unrelated generations inherit the same bounded failure without
  another Card request. Success, disablement, deletion, or a changed Card-input
  fingerprint cancels and resets that retry state;
- startup may therefore publish a mixture of ready and non-ready HTTP Agents
  within one generation. Recovery occurs through the background Recommit loop
  or an earlier operator reapply/Refresh/Recommit.

`HTTPRuntimeManager` also implements
`credential.CredentialLifecycleListener`. From the last committed Agent
generation it maintains a reverse `auth_ref -> agent_id` dependency index.
Credential create, update, delete, and replace callbacks compare the affected
referenced credential's new eligibility with the committed fingerprint and
coalesce one `AgentManager.Recommit` only when it changed; unrelated credential
ids and same-eligibility secret/OAuth refresh rotations do nothing. The
callback schedules recommit only after the credential-manager mutation and
callback stack have released their locks—it must not synchronously re-enter
either manager or create a lock cycle. If another eligibility-changing callback
arrives while that Recommit is in flight, the manager records a pending bit and
runs one follow-up Recommit; coalescing never discards the final credential
state. Recommit recomputes credential
eligibility and selection from the cached parsed Card candidates. A
same-eligibility secret or OAuth refresh rotation therefore causes no Card
fetch and no execution-fingerprint change. Existence, type, owner, disabled,
or usable-secret changes update readiness,
and a changed effective credential principal retires the old execution
resources before the replacement becomes dispatchable. The immutable
`Capabilities` view changes only at this commit boundary. A credential may
still disappear between resolution and the request; the live RoundTripper
therefore remains the final fail-closed check.

Commit atomically publishes the whole map before the new Agent generation is
dispatchable. The manager also owns the per-fingerprint
execution resources referenced by a resolved view: the SDK client plus the
bounded mutable claim, binding, and run registries. Those registries are not
part of the immutable configuration snapshot; `HTTPBackend` operates on them
through the resolved execution handle. Cleanup arms all active runs for
cancellation, issues concurrent bounded best-effort `CancelTask` calls for task
ids that are already bound, retires the replaced client, closes the
execution-owned transport's idle connections, and retires its execution
resources. A task id that binds concurrently observes the armed slot and follows
the same exactly-once cancellation path, so lifecycle retirement does not
require a second Agent definition listener.
A failed new/Card-input-changed fetch or validation publishes a non-ready
snapshot with a bounded `config_error`; it never retains stale Card state. A
credential-only non-ready snapshot does retain the current parsed candidates
for local reselection. Health reports that bounded `config_error`, coalesces
only probes for the same execution fingerprint, allows unrelated Agents to
probe concurrently, and prunes retired health-cache fingerprints at commit.
The implemented manager exposes read-only `ResolveExecution(agent_id)` for
Path B and `ResolveProxyTarget(agent_id)` for Path A. The latter returns the
selected URL, tenant, auth reference, transport policy, execution fingerprint,
and Card template and fails closed unless `path_a_proxy_ready` is true. The
dispatcher obtains it through `AgentGateway.HTTPRuntimeManager()`; it must never
reach into an `HTTPBackend` or read the config store in a request hot path.

Both paths have shipped on the shared manager. The manager exists independently
of runtime-backend registration, so the Path A view does not depend on
`RuntimeRegistry.Resolve("http")` or Path B capability state.

## 6. Schema

`HTTPRuntime` (`pkg/agent/types.go`) replaces the design-only `endpoint` field
with an explicit Agent Card reference and grows from `{endpoint, auth_ref}` to:

```jsonc
"runtime": {
  "type": "http",
  "http": {
    "card_url": "https://agents.internal/.well-known/agent-card.json",
    "protocol": "a2a",              // required; "custom" is reserved and rejected today
    "auth_ref": "agent-callback-key",
    "timeout_seconds": 300          // total turn/proxy deadline; 0 = 120 seconds
  }
}
```

Validation rules:

- `card_url` is an absolute Agent Card URL, not the JSON-RPC service URL. The current scope
  requires HTTPS, except loopback HTTP in tests/development. The old
  design-only `endpoint` name is removed rather than retained as an alias.
  Card fetch does not follow redirects. Interface and security selection uses
  the single ordered algorithm in §5.5; no validation path may first pick an
  otherwise-unacceptable `"JSONRPC"` entry and fail without considering a later
  acceptable one.
- The current scope requires every selectable interface URL to have the same scheme and
  authority as `card_url`; authority comparison uses the effective port, so an
  omitted default `:443`/`:80` is equivalent to its explicit form. Cross-origin
  candidates are skipped. If no same-origin
  candidate survives, the Agent fails closed. This prevents an authenticated
  Card from redirecting the gateway's Bearer credential to a different origin.
  A future explicit origin allowlist may relax the rule.
- `protocol` is required and its only value is `a2a`. Empty and `custom` fail
  validation; no transition-only empty
  value or legacy alias is retained.
- `timeout_seconds` bounds one complete southbound Path B turn or one Path A
  proxied request. `0` selects the default of 120 seconds. Expiry maps to
  `backend_timeout` on Path B and to the §8.4 proxy error on Path A. The
  finer transport deadlines are gateway-owned policy rather than more Agent
  fields: TCP connect and TLS handshake are each 10 seconds, response-header
  wait is 30 seconds, and an established response/SSE stream may be idle for
  60 seconds. The idle timer resets on every body byte, including an SSE
  comment/heartbeat; it does not extend the total deadline. A Card request has
  a 10-second standalone ceiling and the same connect/header bounds; during
  definition prepare it is further capped by the remaining five-second
  listener budget (§5.5), while health probes receive the full 10 seconds. All
  values participate in the execution fingerprint so a
  policy change retires the old client and bindings. The control-plane design
  already assigns "`runtime.http` owns Card URL/auth/timeouts" (§5.3); the
  schema field plus this transport policy complete that assignment.
- Size limits are gateway-owned constants: an Agent Card is at most 1 MiB;
  one JSON-RPC request or non-stream response is at most 4 MiB; one SSE
  event is at most 1 MiB; and one complete SSE response is at most 64 MiB. The
  aggregate limit counts every stream byte, including comments and heartbeats.
  Southbound transports disable automatic compression and accept only an
  absent `Content-Encoding` or `identity`; every other encoding fails closed,
  so limits apply to the identity body bytes actually parsed. A request over
  its limit is never forwarded. A Path B response
  or event over its limit maps to `turn_failed`; Path A uses the response rules
  in §8.4. All four constants participate in the execution fingerprint beside
  the timeout policy. SSE parsing reads bounded chunks before appending them to
  the current event, so a newline-free line cannot allocate past the event cap
  before it is rejected.
- Consistency: an AgentRoute with `protocol a2a` must target an Agent with
  `runtime.type = "http"` and `runtime.http.protocol = "a2a"`. Mismatch fails
  at route validation, so the two axes never drift apart. A `protocol agent`
  route accepts any executable `http` Agent regardless of dialect.

The AgentRoute model (`pkg/gateway/agentroute`) currently forces
`protocol = agent` or `protocol = a2a` in `Normalize` / `ToConfig`: `kind`
stays `agent` for both paths.
Changing runtime type or dialect still must not change the route id, URL, or
VirtualKey allowlist. Dispatcher matching stays kind-based; `dispatchAgent`
branches on `protocol` so `a2a` does not enter the `/turn` family.

### 6.1 Southbound HTTP media and framing contract

Path B does not inherit permissive MIME/framing behavior from the SDK. Its
guarding transport parses `Content-Type` with `mime.ParseMediaType`; media-type
tokens and the optional `charset` name/value are compared case-insensitively.
The header must be present. The only accepted parameter set is empty or exactly
`charset=utf-8`; duplicate, malformed, or any additional parameter fails
closed.

- Agent Card prepare/health sends `Accept: application/json` and accepts HTTP
  `200` with `application/json`. A conditional health request may also accept
  `304` with no body. A `200` body is exactly one JSON object followed only by
  JSON whitespace and EOF within 1 MiB; non-success status, wrong MIME,
  trailing JSON/value bytes, or invalid JSON makes prepare non-ready and health
  unhealthy.
- Every JSON-RPC operation sends `Content-Type: application/json`. Non-stream
  calls send `Accept: application/json` and require HTTP `200`, that media type,
  and exactly one non-batch JSON-RPC 2.0 response object with the matching
  request id and exactly one of `result` or `error`, followed only by JSON
  whitespace and EOF within 4 MiB.
- `SendStreamingMessage` sends `Accept: text/event-stream` and requires HTTP
  `200` with `text/event-stream`. SSE comments and empty heartbeat records are
  permitted and count toward the aggregate limit. Every non-heartbeat
  dispatched event must contain at least one `data:` line; multiple data lines
  are joined with `\n` as required by SSE. The joined data must be exactly one
  JSON-RPC 2.0 response object whose `id` matches the request and which contains
  exactly one of `result` or `error`. JSON-RPC batches, trailing JSON values,
  data-less named events, and both/neither result/error fail closed. Standard
  `event`, `id`, and `retry` fields and unknown SSE fields do not change A2A
  semantics and are ignored after framing validation. One dispatched event is
  capped at 1 MiB counting all of its raw identity-encoded field bytes, and the
  full identity stream is capped at 64 MiB.
- The guarding transport sets `DisableCompression` and rejects every response
  `Content-Encoding` except absent/`identity` before exposing its body to
  `a2aclient`. A clean stream EOF is successful only under the terminal/direct
  message rules in §9.2.

For Path B, status codes retain the mapping in §9.3; a wrong media type,
encoding, JSON-RPC envelope/request id, or SSE frame before downstream commit
maps to `turn_failed` and HTTP 502. The same failure after common-envelope SSE
commit emits the single terminal error owned by the sequencer. Size and timeout
failures retain their mappings from §6 and §9.3. Tests exercise status and
missing/wrong MIME, accepted UTF-8 charset, rejected extra
parameters/compression, trailing JSON, mismatched ids,
both/neither result/error, data-less events, multiline data, heartbeat traffic,
and clean versus premature EOF.

## 7. Credentials (`auth_ref`)

`auth_ref` is currently normalized but never resolved. This design makes it a
reference into the credential store, resolved through the shared credential
manager.

The credential design avoids inventing a second secret store or a fake
provider row. An HTTP Agent's upstream credential uses the existing
`Credential` model with the dedicated non-provider scope
`http-agent:<agent-id>` and empty `provider_type` / `provider_id`. The prefix is
canonicalized case-insensitively, while the Agent-id suffix preserves the exact
case-sensitive `Agent.ID`; case-distinct Agents cannot share a credential.
`Credential.Validate` retains both provider fields for ordinary provider scopes,
but accepts their absence only for this recognized HTTP-Agent scope. `auth_ref`
may reference only an enabled `api_key` or `oauth_token` credential with the
exact target Agent scope; an LLM-provider credential, a generic unbound
credential, or another Agent's HTTP credential is rejected. The credential model extends
Admin create/update, bundle validation, CLI display, and tests for this shape.
This scope identifies the southbound HTTP credential owner only; it does not
grant the Agent a gateway principal or enforce `Agent.resources`.

Southbound authentication is **HTTP Bearer only**:

- The client's VirtualKey **terminates at ingress** and is never forwarded.
- When `auth_ref` is set, the gateway injects `Authorization: Bearer <secret>`
  on southbound HTTP. `api_key` credentials attach that way; `oauth_token`
  credentials go through the existing request-time external refresh path
  (`GetCredential` + `RefreshCredentialIfNeeded`, the same transport
  `RoutedProvider` uses) and are also attached as Bearer. The current scope does not place
  secrets in a custom header, query, or cookie, and does not speak Basic,
  OAuth/OIDC redirects, or mTLS. It never attaches this credential to the
  public Card GET or forwards it across an HTTP redirect.
- The execution snapshot stores only `auth_ref`. Its reusable HTTP client has a
  live credential RoundTripper which performs `GetCredential` and, for
  `oauth_token`, `RefreshCredentialIfNeeded` for every operation before cloning
  the request and setting `Authorization`. It never mutates the caller's request,
  caches a resolved token, authenticates Card fetches, or reuses Authorization
  across redirects. Credential deletion, disablement, owner mismatch, refresh
  failure, or an empty secret therefore fails the next request closed even when
  the Card-derived snapshot itself is unchanged.
- A missing, wrong-kind, or unusable credential is a normalized error
  (`backend_unavailable` for Path B, the §8.4 server error for Path A),
  never a silent unauthenticated call.
- Existence of `auth_ref` is validated when the Agent is executable; a
  dangling or wrong-owner reference fails closed. A future Agent callback
  principal that enforces `resources` remains the control-plane §5.1 open
  question; it is separate from this closed southbound credential scope.

### 7.1 Card security must match the Bearer injector

`credential.type = api_key` does not encode header name or location. A2A
`APIKeySecurityScheme` can require `location=query|header|cookie` and an
arbitrary `name`. OAuth2, OIDC, Basic, and mTLS are different schemes.
Therefore **JSON-RPC interface presence is not enough for `Executable`**.

The `securityRequirements` list is an **OR** of alternatives; the schemes
inside one requirement are an **AND**. `pkg/a2a/card` classifies those
alternatives without credential state. `HTTPRuntimeManager` combines that
classification with the Agent's validated HTTP-owned credential and selects
one satisfiable alternative:

- an empty `securityRequirements` list, or an alternative whose `schemes` map
  is empty, is anonymous and is usable only when `auth_ref` is empty;
- a Bearer alternative is usable only when `auth_ref` resolves and the
  alternative references exactly one declared `httpAuthSecurityScheme` whose
  `scheme` is `Bearer` (case-insensitive per RFC 9110);
- all referenced scheme names must exist. An alternative containing multiple
  schemes is not satisfiable in current scope because the single `Authorization` header
  injector cannot fulfill an AND of credentials.

The card is authenticable when **at least one** alternative is satisfiable.
Unsupported alternatives (api-key-in-query/header/cookie, Basic, OAuth2,
OIDC, or mTLS) do not invalidate another satisfiable anonymous/Bearer
alternative. If none is satisfiable, the Agent is `Executable: false` and
Path A route validation fails closed.

Path A northbound cards advertise VirtualKey the same way: one named
`httpAuthSecurityScheme` (`scheme: Bearer`) under `securitySchemes`, and a
matching `securityRequirements` entry when the route requires a VirtualKey.
Do not copy the remote's `securitySchemes` / `securityRequirements` onto the
served card.

## 8. Path A: A2A proxy ingress (`protocol a2a`)

A route with `protocol a2a` speaks A2A JSON-RPC northbound and southbound.
Event-level translation is zero: the JSON-RPC envelope, Messages/Parts, task
lifecycle, artifacts, and `TASK_STATE_INPUT_REQUIRED` pass through without
reassembly. Governance is not absent.

```text
client ──A2A JSON-RPC──> [ingress governance: VirtualKey, rate limits, budget]
                         [credential rewrite: strip VirtualKey, inject auth_ref]
                         [optional read: taskId/contextId for attribution]
                         ════════ body / SSE forwarded without re-encoding ════════>
                         [GET /.well-known/agent-card.json  gateway-served]
```

What the path still must do:

- **Terminate HTTP** to swap credentials (this alone rules out a raw L4
  proxy or Caddy `reverse_proxy`).
- **JSON-RPC only.** REST paths (`POST /message:send`, …) and gRPC return
  the HTTP/JSON-RPC error defined by §8.4. The current scope does not translate bindings.
- **Allow only request-bound/task-control methods.** `pkg/a2a/jsonrpc` owns
  local wire-name constants because the SDK definitions live under Go
  `internal/` and cannot be imported by this repository. The current scope allowlist is
  exactly `SendMessage`, `SendStreamingMessage`, `GetTask`, `ListTasks`,
  `CancelTask`, and `SubscribeToTask`. Reject JSON-RPC batches, notifications,
  unknown methods, `GetExtendedAgentCard`, and
  `GetTaskPushNotificationConfig` / `CreateTaskPushNotificationConfig` /
  `ListTaskPushNotificationConfigs` / `DeleteTaskPushNotificationConfig`
  before forwarding. The push-config family would let a caller register a
  remote-to-client webhook outside gateway auth, rate limits, attribution,
  and metering; current scope does not expose that side channel.
- **Reject embedded push configuration.** `SendMessage` and
  `SendStreamingMessage` additionally fail closed when
  `params.configuration.taskPushNotificationConfig` is present and non-null.
  The read-only envelope check may parse a bounded copy for method, tenant, and
  this field, but an accepted request forwards the original bytes without
  re-marshalling. This closes the webhook path that remains even when the four
  dedicated push-config methods are denied.
- **Own one exact JSON-RPC endpoint.** If the normalized route prefix is `P`,
  the Card advertises the absolute URL for `P` and clients send JSON-RPC with
  `POST P` (for a root route, `POST /`). The dispatcher branches on
  `kind=agent && protocol=a2a` before the existing `/turn` endpoint matcher;
  after route-prefix removal it accepts only `/` with `POST`. Additional path
  segments, including the REST operation paths, fail closed. Query service
  parameters do not change endpoint matching. A `protocol=a2a` AgentRoute must
  leave `match_policy.methods` empty or contain both `GET` and `POST`; any other
  method set fails route create/update validation so a POST-only route cannot
  advertise an unreachable Card. Runtime dispatch still restricts `POST` to the
  exact JSON-RPC endpoint and `GET` to the exact Card path.
- **Serve the Agent Card.** Blindly forwarding the remote card is wrong once
  the gateway fronts the service: it would advertise the remote URL and the
  remote auth schemes, and a v1.0 card in front of a v0.3 remote would make
  clients send methods the remote cannot honor. The gateway answers
  `GET <route-prefix>/.well-known/agent-card.json` from an **Agent-owned card**
  (see §8.1). This route-local URL is a directly configured Card URL, not the
  standardized origin-level discovery URI. Standard well-known discovery
  requires a dedicated hostname whose AgentRoute owns `/`; multiple agents on
  one origin use direct Card URLs or a registry. `pkg/a2a/proxy` must not fetch
  or forward the remote card. This Card GET is public discovery: it bypasses
  VirtualKey and rate-limit admission but still requires a present, enabled,
  correct-runtime, proxy-ready Agent snapshot. JSON-RPC POST continues through
  the route's ordinary VirtualKey and rate-limit policy. The served Card
  advertises that POST policy as HTTP Bearer when VirtualKey is required.
- **Stamp identity**: interaction spans and per-runtime metrics for A2A
  ingress join the existing attribution bridge (a new `a2a` branch beside
  `pkg/dispatcher/agent_handler.go`).
- **Attribution is request-level.** Optional read-only frame inspection of
  `taskId`/`contextId` is deferred until there is traffic that needs run-level
  usage; peeking must not rewrite the body.
- **Enforce `A2A-Version` rather than forwarding it blindly** (see §8.2).
  The served Agent Card must advertise v1.0 JSON-RPC only when the remote
  card also does; otherwise route validation fails.

What is absent by design on this path: no `ServeTurn`, no run registry, no
permission broker, no event sequencer. Cancellation and resumption are the
client's own `CancelTask` / `SubscribeToTask`, forwarded. Run views,
permission resolution, and transcripts are fail-closed for these routes.
Admin capability surfaces must present this honestly: an Agent can be
reachable through Path A while remaining non-executable on `protocol agent`
routes until Path B ships.

Path A has its own dispatch-time admission path; it must not borrow or bypass
Path B readiness accidentally. `dispatchAgent` is refactored in this order:

1. resolve the AgentRoute and strip its prefix;
2. branch on route protocol;
3. for `protocol=a2a`, accept only JSON-RPC `POST /` or Card
   `GET /.well-known/agent-card.json`, resolve the Agent definition, reject
   missing/disabled/wrong-runtime Agents, then call
   `HTTPRuntimeManager.ResolveProxyTarget(agent_id)` and reject a missing,
   invalid, or non-ready snapshot before serving the Card, resolving a
   credential, or forwarding;
4. for `protocol=agent`, retain the existing `/turn`/optional endpoint matcher,
   resolve the runtime backend, and require `Capabilities().Executable`;
5. JSON-RPC POST on both protocol branches retains common VirtualKey/rate-limit
   admission and Agent attribution; the Path A Card GET is the explicit public
   discovery exception and performs only Agent/snapshot admission plus bounded
   observability.

The A2A branch intentionally does not call `RuntimeRegistry.Resolve("http")`:
Path A can be ready independently of Path B. Its equivalent executable gate is
the shared snapshot's `path_a_proxy_ready`, which covers accepted Card,
interface, tenant, security/auth reference, public Card template, and transport
policy. Route create/update validation performs the same lookup, while request
dispatch repeats it against the current immutable generation to prevent stale
routes from forwarding.

### 8.1 Agent Card source

Path A serves an **Agent-owned card**, optionally seeded from the remote card
at apply/update time:

- `supportedInterfaces[0].url` is the absolute AgentRoute prefix URL (the
  exact `POST` endpoint defined above), not the remote selected interface URL.
- `supportedInterfaces[0].protocolBinding` is the exact wire token `"JSONRPC"`;
  `protocolVersion` is `1.0`.
- `supportedInterfaces[0].tenant` copies the selected remote interface tenant
  exactly when present. The Path A method-envelope peek validates that every
  forwarded request carries that exact tenant (and omits it when the interface
  omits it), while forwarding the original bytes unchanged after validation.
- `securitySchemes` / `securityRequirements` describe VirtualKey as HTTP
  Bearer (`httpAuthSecurityScheme.scheme = "Bearer"`), never the remote's
  scheme, never a custom API-key header/query/cookie.
- `name`, `description`, `skills`, and capability flags may be copied from
  the accepted remote Card into the immutable `public_card_template` during
  definition prepare (§5.5). The served Card always forces
  `pushNotifications: false` and `extendedAgentCard: false`; streaming is true
  only when supported by the selected remote interface. Runtime
  fetch-and-rewrite of the remote card on every well-known GET is rejected: it
  races, breaks JWS signatures, and can advertise a version the body path
  cannot speak.
- Copied skills never retain the remote skill-level `securityRequirements`.
  When the route requires a VirtualKey, every served skill references the same
  gateway-owned HTTP Bearer scheme as the Card; otherwise skill-level security
  requirements are omitted. This prevents dangling references to remote
  schemes that are deliberately absent from the rewritten Card.
- Remote-card `signatures` are never copied. The current scope does not verify JWS signatures
  while fetching or seeding a remote Card and does not sign the gateway-owned
  rewritten Card. HTTPS, the configured same-origin Card URL, and operator
  approval are the current scope trust boundary. Because rewriting the interface and
  security fields invalidates the signed payload, a gateway-served Card MUST
  omit `signatures`; it must never retain or claim a remote signature.

Template construction is part of definition prepare rather than best-effort:
`path_a_proxy_ready` is false if no usable v1.0 JSON-RPC interface/template can
be built, and route creation or dispatch fails closed. A later remote-card
change does not mutate the accepted template until the operator re-applies the
Agent (or a new process performs startup definition prepare). Extended-card
(`GetExtendedAgentCard`) is not served in current scope.

### 8.2 `A2A-Version`

A2A 1.0 §3.6: clients MUST supply `A2A-Version` as a service parameter on
every request; agents interpret an absent/empty version as **0.3**. The current scope is
v1.0-only, so a verbatim pipe would let a dual-stack remote treat unversioned
Path A traffic as v0.3.

- **Path A northbound:** accept Major.Minor `1.0` from either the
  `A2A-Version` header or query parameter. Service-parameter keys are matched
  case-insensitively (`A2A-Version`, `a2a-version`, etc. are the same key),
  while values are compared case-sensitively. Implementations must enumerate
  query keys rather than relying on the ordinary case-sensitive
  `url.Values.Get`. At least one source must be present. When both sources are
  absent/empty the request is interpreted as 0.3 and rejected; an absent
  header with a valid query value is accepted. `0.3`, any other value,
  duplicate conflicting values, or a header/query mismatch is
  `VersionNotSupportedError` and is **not** forwarded.
  Well-known Agent Card GET is served by the gateway and does not need a
  client version header (the served card is v1.0 JSON-RPC).
- **Path A southbound:** set `A2A-Version: 1.0` on the upstream request
  after credential swap and remove every case variant of the northbound
  version query parameter.
  Do not copy a missing/0.3 client value through. The JSON-RPC body and SSE
  frames stay un-reencoded.
- The selected interface URL's existing query is retained first. Other
  northbound query fields are appended in their original order, except every
  case variant of `A2A-Version`, which is removed before the canonical header
  is set. The proxy never derives an upstream origin from northbound headers.
- **Path B:** `pkg/a2a/client` injects `A2A-Version: 1.0` on every
  southbound call, including Agent Card fetch. Do not rely on the SDK
  default if it omits the header.

### 8.3 Header and extension governance

Path A terminates HTTP, so headers are governed independently of the unchanged
JSON-RPC body and SSE frames:

- Request forwarding removes hop-by-hop headers named by RFC 9110 (including
  names nominated by `Connection`), `Host`, `Authorization`,
  `Proxy-Authorization`, `X-Api-Key`, `Cookie`, `Forwarded`, `X-Forwarded-*`, the inbound
  `X-Agent-Depth`, `traceparent`, `tracestate`, `X-Trace-ID`, and `X-Span-ID`.
  The gateway then injects the selected upstream `Authorization`, canonical
  `A2A-Version: 1.0`, its normalized W3C trace context, and trusted incremented
  `X-Agent-Depth`. Other end-to-end request headers are retained so an A2A
  extension is not silently broken.
- `A2A-Extensions` is matched case-insensitively as a service-parameter key;
  its case-sensitive values and ordering are forwarded unchanged. The proxy
  does not claim to understand, filter, or activate an extension. The
  gateway-owned Card may advertise only extensions copied at seed/apply time
  whose traffic can traverse this transparent body/header policy. The remote
  remains responsible for accepting or rejecting requested extensions.
- Response forwarding removes hop-by-hop headers, `Set-Cookie`,
  `WWW-Authenticate`, and `Proxy-Authenticate`; those challenges describe
  upstream credentials clients cannot use at the gateway. Other end-to-end
  response headers, including extension-defined headers, are retained. The
  A2A specification does not require
  `A2A-Extensions` to be echoed, so the gateway neither synthesizes nor removes
  an upstream response value.
- Path B uses the SDK service-parameter API to send `A2A-Version` and any
  future explicitly supported extension. It does not copy arbitrary
  northbound `/turn` headers to the remote.

The served Card's public interface origin is derived only from trusted routing
state: `protocol=a2a` requires a non-empty `match_policy.host`, the request Host
must match that configured hostname through ordinary route matching, and the
scheme comes from the actual inbound TLS state (`https` with TLS, `http`
otherwise). The matched request authority supplies an explicit listener port
when present. `Forwarded` and `X-Forwarded-*` are never used to construct the
Card URL; the response is `no-store`, preventing an untrusted Host from being
reflected through a shared public Card cache.

### 8.4 Path A rejection and error envelope

Path A first validates the exact route path/method, request content type and
4 MiB body limit, and one JSON-RPC 2.0 request envelope. Notifications then
take the dedicated `204` rejection path below. Requests with an id continue in
this order: `A2A-Version`, method allowlist, snapshot-independent method params
(embedded push config), ready snapshot, selected-interface tenant, and live
credential. No failing request is forwarded.

Failures before a single JSON-RPC request can be identified use HTTP transport
errors: wrong path is `404`, wrong method is `405` with `Allow`, unsupported
media type is `415`, and an oversized request is `413`. They use the existing
gateway `application/json` shape `{"error":"<sanitized message>"}`; Card GET
errors are likewise ordinary HTTP JSON
(`404` for a missing Agent/route and `503` for a non-ready snapshot), never a
JSON-RPC envelope. Error bodies contain no Card validation detail, credential
id, upstream URL, or secret.

After those transport checks, bounded JSON-RPC decoding failures and all later
rejections return HTTP `200` with a JSON-RPC 2.0 error. A valid single request
echoes its exact `id`; parse errors and invalid batches use `null`. A recognized
`SendStreamingMessage` rejection is one `text/event-stream` data event carrying
that error envelope followed by clean closure; other cases use
`application/json`. These gateway-generated rejection envelopes record a failed
interaction with an error type selected from a fixed mapping of the JSON-RPC
code. This keeps the official SDK on its JSON-RPC decoding path.
The fixed mapping is:

| Rejection | JSON-RPC code | Message |
|---|---:|---|
| malformed JSON | `-32700` | `Parse error` (id is `null`) |
| batch or otherwise invalid envelope | `-32600` | `Invalid Request` (id is `null` when unavailable) |
| missing/empty/unsupported/conflicting A2A version | `-32009` | `Version not supported` |
| unknown or denied method, including extended-card and push-config RPCs | `-32601` | `Method not found` |
| tenant mismatch or non-null embedded push config | `-32602` | `Invalid params` |
| non-ready snapshot, credential failure, upstream non-200, connect failure, or pre-response timeout | `-32000` | `Server error` |
| oversized or invalid upstream response before downstream commit | `-32006` | `Invalid agent response` |

A JSON-RPC notification has no response by definition: Path A rejects it
without forwarding and returns HTTP `204` with an empty body while recording a
failed interaction with the bounded `a2a_notification_rejected` error type.
Remote JSON-RPC errors and successful response bytes
are forwarded unchanged. If an SSE response has already committed and then
times out, disconnects, or crosses the per-event/aggregate limit, the proxy
aborts the downstream stream and records the failure; it cannot append a
gateway-synthesized frame without violating Path A's no-reencoding contract.
Non-stream responses are buffered only up to 4 MiB so an oversize can still be
replaced by the `-32006` envelope before downstream commit.

## 9. Path B: translating backend (`agentruntime`)

Path B makes `runtime.type = "http"` executable for existing
`protocol agent` routes: an `HTTPBackend` registered in the
`agentruntime.Registry` (`pkg/gateway/runtime_backends.go`, wired in
`agentgateway.go` alongside `ACPBackend`/`BuiltinBackend`). The backend
consumes the shared manager's typed execution handle; that manager imports
`pkg/a2a/client` and `pkg/a2a/card`. Together they speak A2A v1.0 JSON-RPC
southbound and emit the common turn envelope northbound.

`HTTPBackend` lives in `pkg/gateway`, not in `pkg/a2a`.

### 9.1 Turn mapping

Northbound `POST /turn` semantics project onto A2A:

| `/turn` (northbound) | A2A v1.0 (southbound) |
|---|---|
| `input` (string) | `SendMessage` / `SendStreamingMessage` params message, **one text Part** |
| `session_id` | `contextId` for conversation continuity; **and** `Message.taskId` when an interrupted task is bound to that session (§9.1.1) |
| `run_id` (gateway-allocated) | not sent as A2A identity; the backend keeps a process-local `run_id` ↔ `taskId` map for attribution and `CancelTask` |
| `options.runtime` | current scope: omit or empty object; unknown fields fail `unsupported_option`. Interrupted `taskId` is gateway-owned, not a client option. |
| SSE common envelope | `SendStreamingMessage` events translated per §9.2 |

Path B fidelity is intentionally lossy: `TurnRequest.Input` is a string, so
file/raw/data Parts cannot be sent. That is a northbound contract limit, not
an A2A limit, and capability docs / Admin views must say so. Path A preserves
multimodal Parts because it does not translate.

Path B is **request-bound**, like ACP and builtin turns. Southbound calls use
blocking `SendMessage` or streaming `SendStreamingMessage` and are bounded by
the four-layer timeout policy in §6. The backend does not set
`returnImmediately`, does not
poll `GetTask` after the HTTP call returns, and does not consume push
webhooks. Long-running A2A work that outlives one `/turn` is a Path A
concern (`GetTask` / `SubscribeToTask`).

That blocking contract is enforced even if a conforming remote elects to
return early. A non-stream `SendMessage` result that is a Task in
`TASK_STATE_SUBMITTED` or `TASK_STATE_WORKING` is invalid for Path B: capture
and validate its `taskId`/`contextId` only for cleanup and diagnostics, issue
one best-effort `CancelTask` through the §9.3 active-run slot on an independent
context bounded to five seconds, and return `turn_failed`. Never return nil and
let the common sequencer
synthesize a successful `done`. Because the request reached the remote, drop
an existing claimed binding and do not publish a new binding; retrying the
same turn could duplicate non-idempotent work. Failure of cleanup changes only
bounded logs/metrics (ids and secrets are not labels), not the normalized
client error. The caller may use Path A to inspect long-running tasks.

The typed client is constructed from the exact selected `AgentInterface` in
the shared snapshot with `Config.DisableTenantPropagation: true`. Path B leaves
the tenant field empty on each newly constructed SDK request; the SDK's
interface-bound tenant decorator then writes the selected interface tenant
into `SendMessage`, `SendStreamingMessage`, `CancelTask`, and every other typed
operation. If the selected interface omits tenant, the field remains empty — an
ambient context or northbound `/turn` caller must not invent or override it.
Tests assert the exact tenant for send, stream, and cancel paths.
Path B never sets `SendMessageConfig.PushConfig` and never invokes any
push-configuration method.

### 9.1.1 `contextId` vs interrupted `taskId`

A2A 1.0 treats these as different operations:

- `contextId` without `taskId` **starts a new task** in the same conversation.
- Resuming `TASK_STATE_INPUT_REQUIRED` requires the follow-up `Message` to
  carry the original `taskId` (and matching `contextId`). Spec: clients MAY
  use `contextId` without `taskId` to start a new task; the input-required
  client continues by sending a new message with the **same `taskId` and
  `contextId`**.

Path B keeps one process-local session binding whose optional task id
distinguishes ordinary conversation continuity from an interrupted task:

```text
(agent_id, session_id) -> { contextId, interruptedTaskId?, claimed, expiresAt }
```

Rules:

- If the northbound request omits `session_id`, the backend allocates a gateway
  session id before the southbound call and emits it in the common `session`
  event. The client must reuse that id for later conversation/task
  continuation; an empty id is never used as a shared map key.
- A new `session_id` sends neither `contextId` nor `taskId`; the remote
  allocates a context and, for a Task result, a task id. The backend captures
  a non-empty returned opaque `contextId`, including one returned on a direct
  Message. After
  a completed task, the binding retains only `contextId`, so the next turn
  starts a new task in the same remote conversation. The gateway never assumes
  that its northbound `session_id` is a valid remote `contextId`.
- `TASK_STATE_INPUT_REQUIRED` stores `interruptedTaskId` on the binding, emits
  `done` with `stop_reason: "input_required"` and `data.task_id` /
  `data.context_id`
  (informational; not a permission continuation). The `/turn` run still
  terminates; the next `/turn` is a new `run_id`.
- Every `ServeTurn` with a `session_id` **atomically claims the entire live
  binding** before any southbound call. The current scope deliberately applies single-flight
  semantics to `(agent_id, session_id)`, including a context-only binding after
  a completed task; a second concurrent turn fails immediately with
  `session_busy`. This is a gateway product narrowing, not an A2A limitation:
  A2A permits multiple tasks in one `contextId`, but the common Agent session
  contract needs deterministic event/session ordering. For an interrupted
  binding the claimed `Message` carries both `taskId` and `contextId`; for an
  ordinary binding it carries only `contextId` and starts a new task. A new
  `session_id` starts a new conversation instead of abandoning via a
  client-supplied task id (`options.runtime` stays empty).
- A successful interrupted response replaces/releases the claim with the
  returned binding. An A2A terminal response (`COMPLETED`, `FAILED`, `CANCELED`,
  or `REJECTED`) clears `interruptedTaskId` but retains the returned `contextId`
  for conversation continuity. A pre-send failure
  restores the original binding with its original expiry; once the request may
  have reached the remote, an ambiguous failure drops the claimed binding
  rather than silently restoring and replaying it. The client must start a new
  task or use Path A to inspect the remote task explicitly.
- The binding's `interruptedTaskId` is cleared when the task reaches a terminal
  state (`COMPLETED` / `FAILED` / `CANCELED` / `REJECTED`) or on `CancelTask` /
  `RunCanceller`; its `contextId` remains until expiry. The whole binding is
  cleared on Agent delete/runtime switch/execution-fingerprint change or when
  `expiresAt` elapses. Binding retention is independent of the run registry:
  current scope retains at most 1,024 bindings per Agent, uses an LRU cap, and gives each
  binding a 24-hour sliding TTL refreshed only after an unambiguous successful
  turn. Run tombstones remain governed by their separate 10-minute TTL. The
  execution fingerprint includes normalized `card_url`, selected
  interface URL/tenant, `protocol`, `auth_ref`, and timeout/body-limit policy.
  Therefore an endpoint, dialect, credential principal, or transport-policy
  update cannot reuse a task created under the previous configuration. An
  expired binding is dropped; the following turn starts without either remote
  id rather than guessing stale state. Its initial `session` event includes
  `data.resumed: false` and `data.reset_reason: "binding_expired"`; any supplied
  session id with no process-local binding uses `"binding_missing"`. The latter
  includes process restart and a request routed to another replica. A resumed
  binding emits `data.resumed: true`. Metrics count binding resumes and resets
  by bounded reason; ids never become labels.
- `TASK_STATE_AUTH_REQUIRED` terminates the common `/turn` run with an error but
  is not an A2A terminal state. After validating the returned ids, the backend
  retains the returned `contextId`, clears `interruptedTaskId`, releases the
  claim, and never stores the auth-required `taskId` as resumable. This applies
  to both a new session binding and an existing claimed binding: the next turn
  starts a new task in the same conversation and cannot complete the A2A auth
  challenge. If the response has no valid `contextId`, the claimed binding is
  dropped and the next turn reports the ordinary `binding_missing` reset.
- This map is not the permission broker, not durable
  (`Sessions.Durable: false`), and not `SessionLister`.

`Sessions.Resume: true` therefore means best-effort resume on the same gateway
process, within the binding TTL and capacity: the same `session_id` continues
the A2A `contextId`, and a live interrupted task resumes **that task**. It does
not promise restart-safe or cross-replica resume. Multi-replica deployments
that require continuity MUST use sticky routing keyed by `(agent_id,
session_id)`; otherwise a binding miss visibly starts a new remote context as
described above. Durable/cross-replica bindings require a future shared store.

### 9.2 Event mapping (A2A v1.0 → common envelope)

| A2A v1.0 | Common envelope |
|---|---|
| initial `task` | validate/capture `taskId` + `contextId`; map its current status immediately; emit contained current artifacts/status message once, but do not replay `history` |
| initial/direct `message` | emit its agent-authored Parts as `content`, then `done`; retain a returned non-empty `contextId` as a context-only session binding, but create no task binding |
| `SendMessage` returning a terminal or `INPUT_REQUIRED` / `AUTH_REQUIRED` Task | apply the same Task status/artifact rules as an initial streaming `task`, then emit the matching terminal/interrupted event |
| `SendMessage` returning `SUBMITTED` / `WORKING` | invalid request-bound result: do not emit success, best-effort cancel, drop any claimed binding, and fail `turn_failed` (§9.1) |
| initial `task` or `statusUpdate` with `TASK_STATE_SUBMITTED` | validate/capture `taskId` + `contextId`; emit an attached agent-authored status message as `content` when present; otherwise emit no content event and remain non-terminal |
| `statusUpdate` with `TASK_STATE_WORKING` | `content` when an agent-authored status message is present; otherwise no-op |
| `artifactUpdate` | `content`; artifact payloads carried in event `data` (structured Parts) |
| `TASK_STATE_COMPLETED` | `done` (`stop_reason: "stop"`) |
| `TASK_STATE_FAILED` | terminal `error` (`turn_failed`) |
| `TASK_STATE_CANCELED` | `done` (`stop_reason: "cancelled"`) |
| `TASK_STATE_REJECTED` | terminal `error` (`turn_failed`) |
| `TASK_STATE_INPUT_REQUIRED` | current scope: final `content` + `done` (`stop_reason: "input_required"`); see §9.4 |
| `TASK_STATE_AUTH_REQUIRED` | terminal `error` (`turn_failed`) — `/turn` cannot complete an A2A auth challenge |

A `WORKING` update with no agent-authored message is likewise a valid no-op.
`TASK_STATE_UNSPECIFIED`, an unknown state, or a state transition whose ids do
not match the initial Task fails closed as `turn_failed`; state handling never
discards ids merely because no common content event is emitted.

Sequencer rules are the existing ones: the gateway re-stamps `agent_id`,
`run_id`, `sequence`, `segment_index`; remote-supplied values are ignored or
rejected, terminal synthesis is owned by the common sequencer, and there is
exactly one terminal event per run. `input_required` is a terminal `done`
for this run (no permission-continuation cursor). A follow-up `/turn` on the
same `session_id` is a new run that resumes the interrupted A2A task when
§9.1.1 still has a live binding.

For a task lifecycle stream, the first event must be `task`; a direct-message
stream consists of one `message` and then clean closure. A task stream that
closes before a terminal or interrupted state is `turn_failed`, even if content
was already emitted. Task/event ids and context ids must agree with the initial
Task; mismatches fail closed. This validation is also where the backend learns
the `taskId` used by `RunCanceller` and the interrupted-task binding.

### 9.3 Error mapping

| Remote behavior | Normalized error | Northbound |
|---|---|---|
| Connection failure, 5xx, 502/503 | `backend_unavailable` | 503 pre-stream / terminal `error` |
| Connect, response-header, idle-stream, or total-turn timeout (§6) | `backend_timeout` | 504 / terminal `error` |
| JSON-RPC error / 4xx | `turn_failed` | 502 / terminal `error` |
| Mid-stream disconnect | `turn_failed` | terminal `error` (sequencer guarantee) |

Cancellation uses a manager-owned mutable slot per active run, separate from
the immutable snapshot and protected by one atomic/locked transition:

```text
run_id -> { taskId?, cancelRequested, cancelIssued, upstreamDone }
```

- `RunCanceller` with a known `taskId` marks `cancelIssued` and sends exactly
  one force `CancelTask` using the selected interface tenant and live
  credential on an independent five-second cleanup context. With no `taskId`
  yet it atomically marks `cancelRequested` but does not cancel the receive
  context, and returns retryable `backend_unavailable` ("cancellation is not
  ready; retry"). This prevents the common run registry from tombstoning a run
  before a remote task can be canceled.
- The first valid Task binds `taskId` through the same transition. If
  `cancelRequested` is already set, that transition issues exactly one
  `CancelTask` and then cancels the ordinary receive context. Concurrent bind,
  terminal, Admin-cancel, and disconnect paths cannot issue it twice. An
  already-observed terminal state wins and suppresses `CancelTask`; subsequent
  cancels use the common completed-run tombstone behavior.
- A client disconnect or sink-write failure before `taskId` is known marks
  `cancelRequested` and detaches a cleanup receiver for at most five seconds or
  the remaining total-turn deadline, whichever is smaller. It emits no further
  northbound events and exists only to learn the first valid `taskId` and
  cancel it immediately. At that deadline it aborts the upstream, drops the
  claimed binding, records a bounded orphan-risk counter, and never replays the
  turn. If the id is already known, disconnect sends the same exactly-once
  best-effort cancel immediately.
- A cleanup `CancelTask` failure is observable but does not restore a binding
  or alter an already-selected client error. Run slots are process-local and
  are removed when the active run completes or its common tombstone expires.

`Cancellation.Force: true` therefore means force cancellation is supported
once the remote task is bound. A pre-bind Admin request receives the explicit
retryable error while the slot remains armed to cancel automatically upon
binding; it is never reported as a false success.

### 9.4 Capabilities (honest, per Agent)

`Capabilities()` derives from the remote Agent Card plus dialect limits,
never overstates:

- `Executable: true` only when the card has a v1.0 JSON-RPC interface **and**
  security acceptable under §7.1. `Turn.Streaming` follows
  `capabilities.streaming` (`SendStreamingMessage` vs `SendMessage`).
- `Sessions.Resume: true` (same `session_id` continues `contextId` and, when
  §9.1.1 has a live binding, the interrupted `taskId`). Resume is explicitly
  process-local/best-effort within the 24-hour TTL and 1,024-entry cap;
  multi-replica continuity requires sticky routing. `List`,
  `Transcript`, `Durable`: false — A2A defines no list/transcript contract,
  and the interrupted-task map is process-local. `ListTasks` is not mapped
  to `SessionLister`.
- `Permissions.Interactive: false` in current scope. `TASK_STATE_INPUT_REQUIRED` is
  generic "needs input", not a structured approval request; mapping it onto
  the permission broker would fabricate structure the remote cannot honor.
  The turn ends with `stop_reason: "input_required"`; the client resumes by
  sending the follow-up turn on the same `session_id`, and the backend
  attaches the stored `taskId` (§9.1.1). If a future A2A extension defines
  structured approvals, this is revisited.
- `Cancellation.Force: true` (via `CancelTask`, implemented as
  `RunCanceller`); `Graceful: false`.

Optional interfaces: `HealthChecker` (bounded Agent Card fetch — no turn, no
session, no task) is cheap and feeds admin health views; `SessionLister` /
`TranscriptLoader` / `PermissionResolver` are not implemented and fail
closed.

Card-derived execution state is not fetched in the request hot path. The shared
`HTTPRuntimeManager` owns the Agent definition listener lifecycle and snapshots
described in §5.5. `HTTPBackend` resolves its typed execution view from that
manager; `Capabilities` reads `path_b_executable` and Card-derived features from
the same immutable generation. It never builds or publishes a parallel cache.
`HealthChecker` may perform a separate live bounded Card fetch and reports
drift without silently replacing the execution snapshot; the operator
reapplies/updates the Agent to accept a changed Card. It caches the remote
`ETag` and `Last-Modified`, sends `If-None-Match` / `If-Modified-Since`, treats
`304` as healthy/no-drift, and coalesces probes so one execution fingerprint is
fetched at most once per 30 seconds. Concurrent callers share the in-flight
probe. The shared fetch owns an independent 10-second context: cancellation of
one caller stops only that caller's wait and must neither cancel the fetch nor
publish/cache a synthetic unhealthy result for other callers. The health path
never creates unbounded remote traffic.

## 10. Request flows (summary)

```text
Path A (implemented; protocol a2a, no event translation):
  client --A2A JSON-RPC--> AgentRoute(protocol a2a)
    -> ingress governance (VirtualKey, rate limits)
    -> HTTPRuntimeManager.ResolveProxyTarget(agent_id) readiness gate
    -> credential swap (auth_ref → Bearer RoundTripper)
    -> GET /.well-known/agent-card.json served from Agent-owned card
    -> pkg/a2a/proxy body/SSE <-> selected AgentInterface.url (JSON-RPC only)

Path B (implemented; protocol agent, translation):
  client --POST /turn + common envelope--> AgentRoute(protocol agent)
    -> existing dispatch (agent_handler.go)
    -> agentruntime.Registry.Resolve("http") -> HTTPBackend
    -> HTTPRuntimeManager.ResolveExecution(agent_id)
    -> pkg/a2a/client SendStreamingMessage to selected AgentInterface.url
    -> event mapping (§9.2) + sequencer re-stamp
    -> common envelope SSE
```

## 11. Observability and attribution

Path B stamps agent identity on the interaction span through the existing
`ResolveAgentID` attribution bridge and reports the common envelope's event
accounting (event counts, stop reasons) exactly like the ACP/builtin backends.
Path A uses the same identity bridge, records request-level usage, and adds an
`a2a` ingress variant to the per-protocol metrics families. A2A reports no token
usage, so token-based attribution is a gateway-side estimate at best and is not
fabricated into `usage` events.

Every southbound call injects canonical W3C `traceparent` using the current
ingress interaction span id plus the accepted `tracestate`. The remote Agent's
span is therefore a child of the gateway interaction span; the gateway does
not create a separately persisted southbound execution span. Neither path
forwards a raw client `traceparent`, `X-Trace-ID`, or `X-Span-ID`. The outbound
`X-Agent-Depth` is the dispatcher-normalized inbound depth plus one, matching
the value returned by the current ingress trace bridge. An HTTP Agent that
calls the gateway again therefore presents the incremented depth to the
existing `metrics.max_agent_depth` gate instead of resetting the chain.
Invalid/untrusted inbound trace/depth values follow the dispatcher
normalization rules before any outbound header is created.

## 12. Unsupported extensions

The following capabilities are outside the current contract. Adding one
requires a separate design because it changes identity, state, or wire-level
semantics:

- Path A run-level attribution derived from read-only frame inspection;
- session and transcript APIs over future A2A extensions;
- structured permission mapping for a future A2A approval contract;
- a custom southbound dialect;
- A2A ingress that reverse-translates to ACP or builtin Agents;
- REST or gRPC bindings, A2A v0.3 compatibility, or consumed push webhooks;
- multimodal common-turn input beyond text Parts.

## 13. Explicit non-goals and rejected alternatives

- **`runtime.type = "a2a"`** — rejected: folds a dialect into the lifecycle
  axis (§3).
- **User-defined-protocol passthrough as a backend** — rejected: a backend
  that cannot parse events cannot satisfy `ServeTurn` (no envelope, no
  capabilities, no sequencing) and would silently break the control-plane
  guarantees. Byte-level proxying with auth and rate limits is what Caddy's
  `reverse_proxy` already is; entering the agent control plane is only
  justified by turn semantics (Path B) or a standard dialect (Path A).
- **Path A via typed `SendMessage` re-encode** — rejected: that is a third
  translation path and drops extensions. Path A uses `pkg/a2a/proxy`.
- **Official `a2asrv` as Path A** — rejected: Path A is not an A2A executor.
- **Hand-rolling a Path B JSON-RPC/SSE client** — rejected: `a2aclient`
  (`github.com/a2aproject/a2a-go/v2`) exposes `WithJSONRPCTransport` /
  `NewJSONRPCTransport` and no longer requires gRPC. Path A still owns its
  verbatim proxy; Path B does not duplicate the SDK transport.
- **Consuming or exposing A2A push webhooks** — Path B never requests a push
  configuration. Path A rejects the four push-configuration methods and a
  non-null embedded `taskPushNotificationConfig`, and advertises
  `pushNotifications: false`; webhook indirection would bypass request-bound
  governance and add state without benefit.
- **Gateway-owned durable task state / polling reconciliation** — A2A tasks
  belong to the remote service; the gateway does not mirror or reconcile
  them (durable workflows are the upper layer's concern per the control-plane
  boundary). Path B is one request-bound turn; Path A forwards task APIs.
- **A northbound A2A→`/turn` translation path** — `protocol a2a` is the A2A
  ingress; adding a third conversion (A2A client → common envelope) would
  re-create Path B with a worse contract.
- **v0.3 compatibility mode** — rejected. Remotes that only speak v0.3
  are not executable.
- **Agent Card JWS** — remote signatures are not verified and rewritten
  gateway cards are not signed. Rewriting always removes remote signatures;
  signature verification and gateway-managed signing/key rotation are a
  future security feature, not an implied property of Path A or Path B.
- **Durable/cross-replica HTTP session bindings** — bindings are bounded,
  process-local state. Sticky routing is an operator requirement when
  best-effort resume matters; a shared binding store is deferred.

## 14. Remaining design question

HTTP Agent callback identity remains separate from the southbound
`http-agent:<agent_id>` credential used by the gateway to call that Agent. A
future authenticated callback principal may enforce `Agent.resources`
directly, but that requires a cross-runtime control-plane decision and is not
implied by either Path A or Path B.
