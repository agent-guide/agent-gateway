# Routes

This guide covers how LLM routes work, how they match requests, and how to expose concrete and logical models.

## What A Route Does

A route binds inbound request matching to one target policy and one protocol handler.

At runtime a route decides:

- which requests it matches
- which wire protocol is used
- whether a VirtualKey is required
- which concrete ProviderModels and LogicalModels clients may select

## Route Matching

LLM routes match on:

- `host`
- `path_prefix`
- `methods`

The dispatcher removes the matched `path_prefix` before handing the request to the selected protocol adapter.

Matching prefers a longer path prefix, then a declared host, then declared
methods. Routes with equal scores must not match the same request. For example,
two `/api` routes with methods `[GET, POST]` and `[POST]` conflict. Use disjoint
hosts, paths or methods, or explicitly replace/delete a conflicting route.
The check includes every route kind, static and dynamic entries, and disabled
routes. Static entries do not win ties. A disabled route reserves its match
space and returns a disabled error instead of falling back to a broader route.
Native A2A routes reserve all methods at their match conditions so their own
endpoint can return transport errors.

Ambiguous persisted configuration prevents startup. Use the
[offline recovery guide](architecture-config-recovery.md) to inspect and repair
it without Admin. A Refresh failure in a running process retains the last valid
configuration. Admin route mutations that would introduce a tie return 409.

## Route Protocol

Current LLM route protocols are:

- `openai`
- `anthropic`
- `cc`

MCP uses `protocol mcp`, but MCP route behavior is documented separately.

## Model Targets

All routes use `type: models` and explicitly expose ProviderModel or LogicalModel
references. A ProviderModel route exposes models from one Provider, using their
real upstream model names without aliases. A LogicalModel route exposes logical
references and owns no candidate selection itself; the LogicalModel selects
among registered ProviderModels and owns fallback. A route cannot mix kinds or
use multiple Providers directly. Use separate routes or a LogicalModel instead.

Request `model` selects a route-visible name. An omitted model uses the route's
`default_model`; it never falls back to the Provider's default or bypasses the
route's allowlist. See the [configuration guide](logical-model-routing.md).

Static Caddyfile example:

```caddy
provider_model openai-main gpt-4.1 {
    streaming true
    tools true
}
route openai-chat {
    protocol openai
    path_prefix /
    require_virtual_key
    target model openai-main gpt-4.1
}
```

The first Caddyfile target becomes the default unless `default_model` is set
explicitly. Clients use `model: gpt-4.1` in this example. Static model definitions
are read-only through Admin; edit their source to change them.

## VirtualKey Policy

Routes use `auth_policy.require_virtual_key` to declare whether caller authentication is required.

Accepted request headers:

- `Authorization: Bearer <key>`
- `x-api-key: <key>`

## Choose A Target

Use ProviderModel for a fixed upstream binding. Use LogicalModel for a stable
model identity with candidate selection and fallback. Both require explicit
registration and route exposure; adding a model to the catalog never expands
an existing route's access.

## LLM Request Body Size

The default LLM request body limit is 16 MiB (16,777,216 bytes). It measures the
whole received JSON body, including conversation history, tools, tool results,
and base64 attachments. It applies to OpenAI, Anthropic and Claude Code ingress,
including `count_tokens`. The limit is independent of model context/token limits
and response streaming.

To allow up to 64 MiB in Caddy, configure the HTTP dispatcher:

```caddy
agent_route_dispatcher {
    llm_api openai
    llm_api anthropic
    llm_api cc
    max_llm_request_body_bytes 67108864
}
```

The corresponding Caddy JSON handler property is `max_llm_request_body_bytes`.
For the standalone daemon, use:

```sh
./agwd --max-llm-request-body-bytes 67108864
```

Values are integer byte counts. Omission or `0` selects the 16 MiB default;
negative values are invalid. Each dispatcher instance owns its limit. Configure
it in the HTTP host, independently of persisted routes, Providers and VirtualKeys.
MCP, Agent and Admin body limits retain their separate policies.

Oversized requests receive HTTP 413 with an error such as
`{"error":"LLM request body exceeds 67108864 bytes"}` before upstream execution.
Known oversized lengths are rejected before body reads; unknown-length bodies
are limited while reading. A lower outer proxy/middleware limit can still reject
the request. Increasing this setting does not increase upstream limits.

Choose a limit that fits the host's concurrency and memory budget: the handlers
buffer the request and then decode JSON, so memory per active request can exceed
its received byte size. Reducing history/tool output or attachment size may also
reduce payload size.

## Related Docs

- [../reference/route-schema-reference.md](../reference/route-schema-reference.md)
- [../reference/caddyfile-reference.md](../reference/caddyfile-reference.md)
- [../architecture/llm-request-flow.md](../architecture/llm-request-flow.md)
