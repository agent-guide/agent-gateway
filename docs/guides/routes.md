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

## Related Docs

- [../reference/route-schema-reference.md](../reference/route-schema-reference.md)
- [../reference/caddyfile-reference.md](../reference/caddyfile-reference.md)
- [../architecture/llm-request-flow.md](../architecture/llm-request-flow.md)
