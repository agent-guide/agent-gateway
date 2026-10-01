# Native A2A Ingress Reference

This page is the lookup reference for `kind=agent`, `protocol=a2a` routes. It
describes the implemented A2A Protocol 1.0 HTTP surface.

## Route Requirements

```yaml
id: reviewer-a2a
protocol: a2a
agent_id: remote-reviewer
match_policy:
  host: gateway.example.com
  path_prefix: /a2a/reviewer
  methods: [GET, POST]
auth_policy:
  require_virtual_key: true
```

- the target must be an enabled, proxy-ready `runtime.type=http` Agent;
- `runtime.http.protocol` must be `a2a`;
- `match_policy.host` is required;
- `methods` must be empty or contain both `GET` and `POST`;
- route create/update fails closed when the target is not ready.

## Endpoints

| Request | Authentication | Result |
| --- | --- | --- |
| `GET <prefix>/.well-known/agent-card.json` | public | rewritten Agent Card, `application/json`, `Cache-Control: no-store` |
| `POST <prefix>` | route VirtualKey policy | governed A2A JSON-RPC |

The gateway derives the Card URL from the configured route and the trusted
request authority. It does not trust `X-Forwarded-*` when constructing the
public URL.

## POST Request Contract

- `Content-Type` must be `application/json` or
  `application/json; charset=utf-8`;
- maximum request body is 4 MiB;
- only one JSON-RPC 2.0 request object is accepted;
- `id` is required and must not be `null`; notifications return HTTP `204`;
- `A2A-Version` must be exactly `1.0`, supplied as a header or
  case-insensitively named query service parameter;
- duplicate/conflicting versions and header/query mismatches are rejected;
- an accepted body is forwarded byte-for-byte.

Allowed methods:

- `SendMessage`
- `SendStreamingMessage`
- `GetTask`
- `ListTasks`
- `CancelTask`
- `SubscribeToTask`

Push notification methods, `GetExtendedAgentCard`, batches, and unknown
methods are rejected. A non-null
`params.configuration.taskPushNotificationConfig` on a send request is also
rejected.

If the selected remote interface declares a tenant, the request must carry
that exact tenant. Supplying a tenant when the interface has none is invalid.

## Responses

Non-stream operations require an upstream HTTP `200` response with
`application/json`. The gateway buffers at most 4 MiB and verifies JSON-RPC
version and request ID before committing it.

`SendStreamingMessage` and successful `SubscribeToTask` require upstream HTTP
`200` with `text/event-stream`. Each SSE event is validated before forwarding;
limits are 1 MiB per event and 64 MiB for the stream. CR, LF, and CRLF line
endings are accepted. The first invalid event becomes a gateway JSON-RPC
error; a failure after a committed event terminates the stream and marks the
interaction failed.

Gateway-generated failures use HTTP `200` and preserve a valid request ID:

| Code | Message | Meaning |
| ---: | --- | --- |
| `-32700` | `Parse error` | body is not valid JSON |
| `-32600` | `Invalid Request` | invalid JSON-RPC envelope, missing/null ID, or batch |
| `-32009` | `Version not supported` | invalid or conflicting `A2A-Version` |
| `-32601` | `Method not found` | method is outside the allowlist |
| `-32602` | `Invalid params` | malformed params, tenant mismatch, or push configuration |
| `-32000` | `Server error` | target unavailable or upstream transport/status failure |
| `-32006` | `Invalid agent response` | invalid, oversized, or mismatched upstream response |

After the gateway has accepted a valid single-request envelope and identified
`SendStreamingMessage`, subsequent JSON-RPC rejections are returned as one SSE
`data:` error event. Parse errors and envelope or batch failures occur before a
method is accepted and therefore use `application/json`; other identified
methods, including rejected `SubscribeToTask` requests, also use
`application/json`. HTTP-layer failures occur before JSON-RPC handling:

- `404` for an unknown subpath;
- `405` for a wrong endpoint method;
- `413` for an oversized request body;
- `415` for an unsupported content type;
- the normal dispatcher auth/rate-limit status for failed admission.

## Header Boundary

Southbound requests preserve end-to-end headers such as `A2A-Extensions` but
remove:

- hop-by-hop and connection-nominated headers;
- `Authorization`, `x-api-key`, cookies, and proxy authorization;
- `Forwarded` and all `X-Forwarded-*` headers;
- caller-supplied trace and Agent-depth headers.

The gateway forces `A2A-Version: 1.0`, identity content encoding, trusted trace
context, and incremented Agent depth. When configured, `auth_ref` supplies the
remote bearer credential. Redirects are not followed.

Southbound response headers have hop-by-hop headers, `Set-Cookie`,
`WWW-Authenticate`, and `Proxy-Authenticate` removed.

## Timeouts And Limits

| Layer | Limit |
| --- | ---: |
| connect/TLS | 10 seconds |
| response headers | 30 seconds |
| buffered response or SSE idle | 60 seconds |
| total request | `runtime.http.timeout_seconds`, default 120 seconds |
| request/non-stream response | 4 MiB |
| SSE event | 1 MiB |
| aggregate SSE stream | 64 MiB |

## Related Docs

- [HTTP Agent Guide](../guides/http-agents.md)
- [Route Schema Reference](route-schema-reference.md)
- [HTTP Agent Architecture](../architecture/http-agent-architecture.md)
