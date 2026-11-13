# Route target policy

Capability status: **Implemented**. This package is not a protected Enterprise SPI.

`pkg/gateway/llmroute` owns `LLMRoute` and `RouteModelPolicy`.
`LLMRoute.TargetPolicy` is a concrete `*RouteModelPolicy`; a missing or null
policy stays nil and fails definition validation. The shared
`routecore.RouteConfig` retains raw target JSON for all route kinds.
Concrete policy decoding validates the discriminator and rejects unknown, removed
and duplicate fields without interface dispatch. `modelcatalog` owns
ProviderModel facts, independent LogicalModel definitions and in-memory snapshots.
`pkg/gateway.RoutedProvider` owns execution, credential selection and bounded
request-time fallback. Protocol handlers own parsing and response serialization.

All policies use `type: models` for explicit concrete/logical model exposure.
A policy owns only `model_targets`, `default_model` and common credential settings.
Each target stores its route-visible name and either a ProviderModel compound
reference or LogicalModel ID. Candidate selection and fallback belong to
LogicalModel. There is no Provider-only route target.
See [model routing](model-first-routing.md) for the resource contracts and
[the guide](../guides/logical-model-routing.md) for configuration.

Route matching uses path-prefix length, host specificity, then method
specificity. Equal-score intersecting matches are rejected across all route
kinds and configuration sources, including disabled routes. Native A2A accepts
all methods while retaining declared method specificity. Refresh retains the
last valid generation on failure. Existing conflicts require offline repair.

`ExecuteChat` and `ExecuteStreamChat` clone and rewrite attempt-local requests,
preserving caller model and protocol state. The resolved execution identifies
the actual Provider, upstream model, client model, optional LogicalModel ID,
protocol features and credential attribution. Ordinary provider methods delegate
to these enriched paths. Responses execution retains its existing mutation
contract: after successful execution/opening it writes the attempt model back to
`ResponsesRequest.Model`; callers retain their client model separately.

Credential selectors remain `round_robin` and `fill_first`; scopes are
`model_custom` and `provider_id`; types are `api_key` and `oauth_token`. Scope
and type ordering belong to the route. ProviderModel carries its custom scope.
The credential manager may refresh an expiring OAuth credential through the
configured external command before execution.

Retryable 429 and 5xx failures can exhaust credentials on the same binding,
then advance candidates only within the selected LogicalModel's enabled fallback
budget (maximum five reselections). Credential unavailability may also advance.
Provider resolution errors return immediately. Credential retries and Provider
network retries are not a shared global attempt budget. Feedback currently
records provider-call return; stream opening is not final stream consumption.
Once a stream reader is returned, read failures cannot re-enter fallback.

Caddyfile, Caddy JSON and startup Bundles accept explicit ProviderModel and
LogicalModel declarations. Static model resources are read-only through Admin.
Dynamic model resources use Admin and Bundle apply. Both execute through the same
catalog and enforce the same explicit route exposure.
