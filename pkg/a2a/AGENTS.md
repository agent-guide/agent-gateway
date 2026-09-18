# pkg/a2a — AGENTS.md

Scope: A2A Protocol 1.0 JSON-RPC as a lower protocol package used by HTTP
Agent Path A (governed proxy) and Path B (translating `HTTPBackend`). Paths
are repository-root relative; the root `AGENTS.md` rules apply. The
authoritative product design is
[`docs/design/http-agent-runtime.md`](../../docs/design/http-agent-runtime.md).

Status: **Path B implemented**. `card/`, `jsonrpc/`, and the JSON-RPC-only
official SDK wrapper in `client/` are linked to the gateway `HTTPBackend`;
Path A's `proxy/` remains design-only.

## Boundaries

- `pkg/a2a` is a protocol package, analogous to `pkg/acp` and `pkg/mcp`. It
  is not an HTTP Agent runtime package.
- Do not import `pkg/agent`, `pkg/dispatcher`, `pkg/gateway`, or
  `pkg/credential`.
- Do not implement `agentruntime.Backend`, dispatcher handlers, VirtualKey
  checks, or `auth_ref` resolution here. Path B's `HTTPBackend` lives in
  `pkg/gateway`; Path A's handler lives in `pkg/dispatcher`.
- Callers inject an `http.RoundTripper` / `*http.Client` that already carries
  live southbound auth behavior. This package never sees VirtualKey material,
  credential ids, or resolved secrets. The caller's RoundTripper resolves and
  refreshes auth on each operation; Bearer auth is attached only to the selected
  interface, not the public Card GET, and is never forwarded across redirects.
  This package does inject `A2A-Version: 1.0` on southbound HTTP.

## Package shape

```text
pkg/a2a/
  card/      fetch, parse ordered JSONRPC interfaces and structured security
             alternatives, rewrite helper; no credential policy
  jsonrpc/   Path A helpers: method allowlist, SSE split, frame peek,
             service-parameter parse/inject — not a second Path B client
  client/    wrap official a2aclient locked to JSON-RPC
  proxy/     HTTP-terminated body/SSE pipe + A2A-Version enforcement
```

- Path B uses `card/` + `client/`.
- Path A uses `card/` + `proxy/` (optional read-only frame peek from
  `jsonrpc/`; never re-encode the body).
- Path A must not turn a JSON-RPC request into a typed `SendMessage` and
  re-marshal the response.

## Protocol invariants

- Lock **A2A Protocol 1.0**. Do not dual-stack v0.3 method names, kebab-case
  task states, `kind` event discriminators, or the pre-v1.0 Agent Card shape.
- P0 speaks **JSON-RPC 2.0 only**. If `supportedInterfaces` has no JSON-RPC
  entry, fail closed. Do not add REST or gRPC bindings here.
- `runtime.http.card_url` is the absolute discovery URL; it is not the service
  endpoint. Fetch it without redirects. `card/` returns ordered candidates
  whose `protocolBinding` is exactly `"JSONRPC"`, whose Major.Minor version is
  `1.0`, and whose URL is valid; it also classifies Card-level security
  alternatives as anonymous, single HTTP Bearer, or unsupported. The gateway
  manager, not this package, skips cross-origin candidates, applies `auth_ref`
  and credential-owner policy, and selects the first surviving interface and
  security alternative. Require HTTPS except loopback HTTP in
  tests/development.
- Agent Card well-known path is `/.well-known/agent-card.json`. v1.0 JSON
  security fields are `securitySchemes` and `securityRequirements` (not
  v0.3 `security`). Parsed and gateway-served interfaces use the exact
  `protocolBinding` wire value `"JSONRPC"`; never serialize `"JSON-RPC"`.
- JSON-RPC methods are v1.0 PascalCase (`SendMessage`,
  `SendStreamingMessage`, `GetTask`, `CancelTask`, `SubscribeToTask`, …).
- The SDK Go type stores content in `Part.Content`, but its custom JSON codec
  flattens that union to exactly one top-level `text` / `raw` / `data` / `url`
  member. Never emit or expect a wire-level `content` wrapper.
- Stream events are discriminated by JSON member name (`statusUpdate`,
  `artifactUpdate`), not `kind`.
- `proxy/` copies body and SSE with flush and body caps. It must not fetch or
  forward the remote Agent Card; the dispatcher serves the Agent-owned card.
- Path A owns exactly `POST <route-prefix>` as its JSON-RPC endpoint (root
  routes use `POST /`). The served Card advertises that exact absolute URL.
  Branch before the existing `/turn` endpoint matcher and reject additional
  path segments/REST operation paths. Route validation requires an empty method
  matcher or both GET and POST. Card GET is public discovery; JSON-RPC POST
  retains route VirtualKey/rate-limit admission.
- Path A's exact P0 method allowlist is `SendMessage`,
  `SendStreamingMessage`, `GetTask`, `ListTasks`, `CancelTask`, and
  `SubscribeToTask`. Reject batches, notifications, unknown methods,
  `GetExtendedAgentCard`, `GetTaskPushNotificationConfig`,
  `CreateTaskPushNotificationConfig`, `ListTaskPushNotificationConfigs`, and
  `DeleteTaskPushNotificationConfig`. Also reject a non-null
  `params.configuration.taskPushNotificationConfig` on either send method
  using a bounded read-only parse; accepted body bytes are still forwarded
  unchanged. Served Cards force `pushNotifications` and `extendedAgentCard`
  false.
- The SDK's JSON-RPC method constants are under its Go `internal/` tree and
  cannot be imported here. `jsonrpc/` defines the v1.0 wire strings locally;
  fake-server contract tests assert the literal method emitted by each Path B
  SDK operation used by the gateway so an SDK rename cannot pass silently.
- `card/` rewrite helper takes caller-supplied public URL and
  `securitySchemes` / `securityRequirements` and selected interface tenant;
  it does not invent VirtualKey policy.
- P0 southbound security is HTTP Bearer only. Security requirements are an OR
  of alternatives, each containing an AND of schemes. `card/` parses scheme
  references and returns ordered structured alternatives without accepting an
  `auth_ref` or credential resolver. `HTTPRuntimeManager` decides whether
  anonymous is usable with no auth or one HTTP Bearer alternative is usable
  with an exact-owner credential. Unsupported alternatives do not invalidate a
  satisfiable one; dangling scheme names and multi-scheme AND alternatives are
  classified unsupported in P0.
- Inject `A2A-Version: 1.0` on southbound HTTP. Path A accepts `1.0` from
  either the header or a query service parameter whose key is matched
  case-insensitively; values remain case-sensitive. Missing/empty/other
  versions, duplicate conflicts, and header/query mismatches fail before
  forwarding. Remove every case variant of the version query southbound and
  force the header to `1.0`.
- Preserve case-sensitive `A2A-Extensions` values and other end-to-end
  extension headers while stripping hop-by-hop headers, ingress credentials,
  cookies, forwarding headers, and raw client trace/depth compatibility
  headers. The caller-supplied transport injects the selected credential,
  normalized W3C trace context, and trusted incremented `X-Agent-Depth`.
  Strip response hop-by-hop headers, `Set-Cookie`, `WWW-Authenticate`, and
  `Proxy-Authenticate`; do not synthesize an `A2A-Extensions` echo.
- A route-local `<prefix>/.well-known/agent-card.json` is a directly
  configured Card URL. Standard origin-level discovery requires a dedicated
  hostname/root AgentRoute; multiple Agents on one origin use direct Card URLs
  or a registry.
- Path A copies the selected interface tenant into its served Card and
  validates the exact tenant in each request envelope without re-encoding it.
- P0 does not verify remote Agent Card JWS signatures and does not sign the
  rewritten gateway Card. Rewriting must omit remote `signatures`; never
  present an invalidated remote signature as gateway-owned.
- Use four bounded timeout layers: 10s connect/TLS, 30s response header, 60s
  response/SSE idle, and `runtime.http.timeout_seconds` total (120s when zero).
  Health Card fetches use a 10s total. Definition-prepare Card fetches use
  `min(4s, remaining listener budget)` within the existing 5s total prepare
  deadline. Failed definition fetches recover through manager-owned backed-off
  Recommits; unrelated generations inherit the bounded failure without network
  I/O while the retry timer is pending, and retries never mutate a committed
  generation in place. Idle activity never extends total time.
- Enforce 1 MiB Agent Card, 4 MiB JSON-RPC request/non-stream response, 1 MiB
  SSE event, and 64 MiB aggregate SSE response limits. Disable automatic
  compression and accept only absent/`identity` response encoding; limits count
  the identity bytes, including SSE comments/heartbeats. These constants are
  execution-fingerprint inputs. SSE reads must be chunk-bounded before
  appending to an event buffer; a newline-free upstream line must not allocate
  beyond the per-event limit before rejection.
- Path B's guarding transport enforces design §6.1 before the SDK consumes a
  response. Card and non-stream JSON-RPC require HTTP 200 plus
  `application/json`; streaming requires HTTP 200 plus `text/event-stream`.
  MIME parameters are empty or exactly `charset=utf-8`. JSON bodies contain
  exactly one object and EOF; every non-heartbeat SSE dispatch has data that
  decodes to one matching-id JSON-RPC result-or-error object. Do not rely on
  SDK MIME, status, envelope, or trailing-input tolerance.
- Path A follows design §8.4 exactly: transport failures before an envelope use
  HTTP errors; identified requests use JSON-RPC errors with id echo (or one SSE
  error event for `SendStreamingMessage`); notifications return empty `204`;
  committed-stream timeout/disconnect/limit failures abort without synthesizing
  a frame.
- Health Card fetches retain ETag/Last-Modified validators, use conditional
  requests, coalesce concurrent probes, and fetch at most once per execution
  fingerprint per 30 seconds. The shared fetch has an independent 10-second
  context; one caller's cancellation stops only its wait and is never cached as
  runtime unhealthiness.
- `pkg/gateway.HTTPRuntimeManager`, not this protocol package or
  `HTTPBackend`, owns the immutable Card-derived snapshot shared by Path A and
  Path B. Path A resolves a ready proxy target through `AgentGateway` and
  never performs a config-store/Card fetch or reaches into the backend in the
  request hot path.

## Official SDK

- Pin `github.com/a2aproject/a2a-go/v2` at v2.5.0 and depend on its `a2a`
  package for v1.0 domain types.
- Path B uses `github.com/a2aproject/a2a-go/v2/a2aclient` with
  `WithDefaultsDisabled` and `WithJSONRPCTransport` / `NewJSONRPCTransport`.
  JSON-RPC is public. Do not import `a2agrpc`. Do not leave default REST
  transport enabled.
- The v2.5.0 module graph includes unversioned
  `github.com/a2aproject/a2a-go v0.3.15`; that metadata presence is permitted,
  but no package from it may enter the compile dependency graph. Do not import
  or link `a2acompat/a2av0`, `a2apb/v0`, or any other v0.3 compatibility path.
- Construct Path B clients from the selected `AgentInterface` with ambient
  tenant propagation disabled. Leave request tenant empty so the SDK's
  interface-bound decorator injects exactly the selected interface tenant; an
  inbound context must not override or invent it.
- Do not hand-roll a Path B JSON-RPC/SSE client.
- Path B leaves `returnImmediately` false. If synchronous `SendMessage` still
  returns `SUBMITTED` or `WORKING`, validate/capture ids only for a bounded
  best-effort `CancelTask`, return `turn_failed`, and never allow sequencer
  terminal synthesis to turn it into success.
- Must not use `a2asrv` (that is an A2A executor; Path A is a proxy).

## Tests

Own an in-process JSON-RPC/SSE fake server covering card fetch, send, stream,
cancel, all four timeout layers, disconnect, OR-of-AND Bearer security
classification followed by manager-owned selection, ordered interface skipping,
exact `"JSONRPC"`, interface origin/tenant, all four size limits, exact §6.1
status/MIME/identity encoding and JSON-RPC/SSE framing, and `A2A-Version`.
Path A tests assert the §8.4 error matrix,
body-level equality on the exact-prefix JSON-RPC path, accept header-only and
case-insensitive query-key 1.0, retain extension headers, strip credentials /
hop-by-hop headers, omit rewritten-card signatures, exercise the six-method
allowlist, reject dedicated and embedded push configuration, and reject
missing/0.3/conflicting versions. Path B tests live with `HTTPBackend` and the
common sequencer, including literal SDK-emitted method names, initial
Task/direct Message mapping, premature closure,
trace/depth and exact selected-interface tenant propagation,
`input_required` follow-ups that carry `taskId`,
whole-session atomic binding claims, independent 24h TTL/1,024-entry LRU,
visible binding-reset metadata, and execution-fingerprint retirement. An
omitted northbound `session_id` gets a new gateway session id before it is used
as a binding key. Pre-send failures restore the exact claimed binding; failures
after the SDK operation begins drop ambiguous state. Cover `SUBMITTED`
with/without content, no-op `WORKING`,
unknown states, synchronous nonterminal cleanup/failure, pre-bind cancellation
and disconnect races with exactly-once `CancelTask`, and `AUTH_REQUIRED`
retaining context while clearing task resume.
