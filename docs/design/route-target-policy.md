# Route Target Policy

Capability status: **Implemented** for the runtime described here. This package
is not designated as a protected Enterprise SPI. Additional model-availability
contracts are [Proposed](enterprise-extension-contract.md#121-model-high-availability-boundary).

## Ownership and representation

`pkg/gateway/llmroute` owns `LLMRouteConfig`, `LLMRoute` and the
`RouteTargetPolicy` Go interface. Its current concrete implementations are
`RouteDirectProviderPolicy` and `RouteLogicalModelTargetPolicy`. Common credential
settings live in `RouteTargetPolicyCommon`. `pkg/gateway.RoutedProvider` owns
execution, credential selection and bounded fallback. `modelcatalog` owns
managed-model facts; `pkg/credential` owns credential persistence, refresh and
scheduler state. Protocol handlers own parsing and response serialization.

Stored route JSON is a configuration object with `kind: llm`, `protocol`,
`match_policy`, `auth_policy` and `target_policy`. Policy JSON uses `type` to
identify `direct-provider` or `logical-model`; it does not serialize a Go
interface implementation name. Direct targets use
`target_policy.provider_target.provider_id`. Logical targets use
`target_policy.model_targets`, `default_model`, `model_selector_strategy` and
`fallback`. See the [logical routing guide](../guides/logical-model-routing.md)
for the complete dynamic configuration workflow.

## Resolution

Direct-provider policies select one provider. The request model is the upstream
model, with the provider default used when it is empty. Atomic protocol
requirements are checked against provider-type capabilities. This path does not
require a managed logical-model record or provide cross-model fallback.

Logical-model policies interpret the request model as a route-visible target
name, or use `default_model` when absent. Resolution reads manager snapshots,
excludes candidates already rejected for the request, requires enabled managed
models and providers, and filters generic streaming/tools/vision/embeddings
capabilities and atomic protocol features. No eligible binding is an error.

Selection is implemented in `pkg/gateway/llmroute/policy.go`:

- `priority`: highest priority, with stable configuration order breaking ties;
- `weighted`: positive weights participate in a weighted draw; no positive
  total selects the first eligible candidate;
- `auto`: highest priority tier, with weighted selection within that tier when
  it has positive weights, otherwise the first candidate in the tier.

The resolved target identifies one provider, upstream model, optional logical
model, credential scope and capabilities. `ExecuteChat` and `ExecuteStreamChat`
clone the provider-facing request and its protocol state, rewrite only the
attempt-local model, and preserve the caller's model/protocol state. The ordinary
`Chat` and `StreamChat` methods delegate to these enriched paths.

`CreateResponses` and `StreamResponses` also clone and rewrite the request for
provider execution, but after a successful call or stream opening they write
that clone's model back to the caller's `ResponsesRequest.Model`. Responses
callers must retain the client model separately if they need it after execution;
the Chat request-isolation guarantee does not apply to this field.

## Credentials and fallback

Credential selectors are `round_robin` and `fill_first`; scopes are
`model_custom` and `provider_id`; types are `api_key` and `oauth_token`. Scope
and type ordering belong to route policy. The credential manager may refresh an
expiring OAuth credential through the configured external command before use.

`RoutedProvider` tracks tried candidates, managed credentials and provider-config
authentication attempts. When a retryable execution fails, available managed
credentials can be tried on the same binding. Candidate advancement requires a
logical-model policy with enabled fallback and remaining `fallback.max_num`
(maximum five reselections). Credential unavailability may also advance the
candidate; target/provider resolution errors return immediately. Failure
classification permits model reselection for 429 and 5xx errors; other classified
statuses stop execution. These bounds count model reselections, not a common
budget for every credential attempt and provider-internal retry.

Scheduler feedback records the return of the provider call for a concrete
managed credential. For streams that is stream opening, not final consumption.
Once a stream reader is returned, later read failures do not re-enter the
fallback loop. A complete health/circuit/attempt contract is separate work.

## Static and dynamic configuration

Static Caddyfile and startup Bundle LLM routes must use direct-provider targets.
Logical-model routes and managed models are dynamic resources, configured through
the Admin API or `agwctl apply`. Static `target model` and
`target_policy logical-model` are rejected.

```caddy
route openai-direct {
    protocol openai
    path_prefix /v1
    require_virtual_key
    target provider openai-main
}
```

The Caddyfile also accepts `target_policy direct-provider { ... }`; consult the
[current syntax reference](../reference/caddyfile-reference.md) and
[working example](../../examples/Caddyfile.example). Configuration examples must
use current protocol and route shapes.
