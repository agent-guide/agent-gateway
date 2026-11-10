# Routes

This guide covers how LLM routes work, how they match requests, and how to choose between direct-provider and logical-model routing.

## What A Route Does

A route binds inbound request matching to one target policy and one protocol handler.

At runtime a route decides:

- which requests it matches
- which wire protocol is used
- whether a VirtualKey is required
- whether traffic goes straight to one provider or first resolves a logical model target

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

MCP uses `protocol mcp`, but MCP route behavior is documented separately.

## Direct-Provider Mode

Use direct-provider mode when one route should send traffic to one specific provider.

Static `agw` example:

```caddy
route openai-chat {
	protocol openai
	path_prefix /
	require_virtual_key
	target provider openai-main
}
```

In direct-provider mode:

- the route points at one `provider_id`
- the request `model` is treated as the upstream model name
- this is the only route mode accepted in Caddyfile routes and `agwd --static-config`

## Logical-Model Mode

Use logical-model mode when clients should send a route model name and let the gateway resolve it to one concrete provider and upstream model binding.

Logical-model mode is supported through dynamic route management and bundle workflows. It is not accepted in Caddyfile routes.

Typical logical-model route concepts:

- `default_model`
- `model_targets`
- candidate selection strategy
- fallback policy

## VirtualKey Policy

Routes use `auth_policy.require_virtual_key` to declare whether caller authentication is required.

Accepted request headers:

- `Authorization: Bearer <key>`
- `x-api-key: <key>`

## When To Use Which Mode

Use direct-provider mode when:

- you want the simplest static configuration
- one route maps cleanly to one provider
- clients already send the upstream model name

Use logical-model mode when:

- you want one stable route model name for clients
- you want provider or model indirection
- you want candidate selection and fallback behavior at the gateway layer

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
