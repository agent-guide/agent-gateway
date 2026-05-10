# Model-First Routing Architecture And Technical Specification

## 1. Scope

This document defines the current model-first routing architecture in `agent-gateway`.

It is a runtime design and technical specification document, not a proposal. It describes the implemented ownership boundaries, route semantics, model catalog behavior, and administrative control surface used by the gateway today.

The current Go module path is:

- `github.com/agent-guide/agent-gateway`

## 2. Architectural Position

Model-first routing sits between the route layer and the provider layer.

The gateway runtime uses these responsibilities:

- `pkg/gateway/route`
  - owns the route data model and route target policy
- `pkg/gateway/modelcatalog`
  - owns provider model discovery and managed model overlays
- `pkg/gateway`
  - owns route resolution, provider resolution, and virtual key validation
- `pkg/dispatcher`
  - owns transport-facing request parsing and protocol response translation

The route-facing abstraction is the logical route model name. The provider-facing abstraction is the concrete `(provider_id, upstream_model)` binding.

## 3. Core Routing Contract

The system supports two valid route target modes:

- logical-model mode
- direct-provider mode

Logical-model mode is the primary route contract. In this mode:

- clients send a route model name in the request `model`
- the route resolves that route model name into one concrete provider/model candidate
- the gateway rewrites the upstream request model before invoking the provider

Direct-provider mode is the escape hatch for simple forwarding. In this mode:

- the route explicitly selects one provider
- the request `model` is treated as the upstream model name
- no logical model lookup is performed

## 4. Route Data Model

The route runtime type is `pkg/gateway/route.AgentRoute`.

The target policy is `pkg/gateway/route.RouteTargetPolicy`.

Current route target policy kinds:

- `direct-provider`
- `logical-model`

Current route target policy fields of record are:

- `type`
- `default_model`
- `model_targets`
- `provider_target`
- `model_selector_strategy`
- `credential_selector`
- `credential_scope_order`
- `credential_source_order`
- `fallback`

Important normalization rules in `pkg/gateway/route`:

- `provider_id` and `provider_target.provider_id` are normalized to the same effective value
- logical-model routes use `model_targets`
- policy kind is inferred from the effective direct provider target or model target set when `type` is omitted

## 5. Logical-Model Mode

In logical-model mode, each route model name is represented by one `RouteModelTarget`.

`RouteModelTarget` contains:

- `name`
- `strategy`
- `default_candidate`
- `candidates`

Each `RouteModelCandidate` contains:

- `provider_id`
- `upstream_model`
- `weight`
- `priority`
- `default`

The route model name is the stable client-facing identifier. A route may expose one or more route model names, and each route model name may bind to one or more concrete provider/model candidates.

Current semantics:

- the incoming request `model` is interpreted as a route model name
- the route model name must exist in the matched route's `target_policy.model_targets`
- one concrete candidate is selected from that route model target's candidate set
- the selected candidate determines both provider resolution and upstream model rewrite

If the request omits `model`, `target_policy.default_model` is used when present.

## 6. Direct-Provider Mode

In direct-provider mode, the route uses `target_policy.provider_target.provider_id`.

Current semantics:

- the matched route selects exactly one provider
- the request `model` is passed through as the upstream model name
- if the request omits `model`, the selected provider's own `default_model` may apply

This mode intentionally bypasses logical-model resolution. It is valid for simple static deployments and for routes that intentionally expose upstream model naming directly.

## 7. Route Validation Rules

Every route must normalize into exactly one effective target mode.

Logical-model route requirements:

- `llm_api` must be set
- `target_policy.model_targets` must be non-empty
- `target_policy.provider_target.provider_id` must be empty
- `target_policy.default_model`, when set, must refer to an existing route model target name

Direct-provider route requirements:

- `llm_api` must be set
- `target_policy.provider_target.provider_id` must be set
- `target_policy.model_targets` must be empty
- `target_policy.default_model` must be empty

Invalid mixed-mode configurations are rejected by the route validation path. The gateway does not support one route acting as both a direct-provider route and a logical-model route at the same time.

## 8. Runtime Request Flow

### 8.1 Logical-Model Mode

```text
HTTP request
  -> route dispatcher matches AgentRoute
  -> protocol handler parses request
  -> request model is interpreted as route model name
  -> route default_model may fill an omitted request model
  -> route target candidate set is selected from target_policy.model_targets
  -> one concrete candidate is resolved
  -> provider instance is resolved
  -> request model is rewritten to candidate.upstream_model
  -> provider.Chat(...) or provider.StreamChat(...)
```

### 8.2 Direct-Provider Mode

```text
HTTP request
  -> route dispatcher matches AgentRoute
  -> protocol handler parses request
  -> route target_policy.provider_target.provider_id selects provider
  -> request model is treated as upstream model name
  -> provider.Chat(...) or provider.StreamChat(...)
```

The key design rule is that protocol parsing happens before provider execution, while concrete provider selection depends on the matched route mode.

## 9. Model Catalog Responsibilities

The model catalog runtime is `pkg/gateway/modelcatalog`.

It owns:

- upstream model discovery through provider `ListModels(...)`
- in-memory provider model snapshots
- persisted managed model overlays
- resolved managed model views for administration and validation

It does not own route matching or transport parsing.

The model catalog is shared infrastructure for the gateway runtime and the Admin API.

## 10. Model Catalog Data Model

### 10.1 Provider Snapshot

`ProviderModelSnapshot` is the discovered upstream model record.

Current fields:

- `provider_id`
- `provider_type`
- `upstream_model`
- `display_name`
- `description`
- `capabilities`
- `status`
- `fetched_at`
- `last_error`

Current snapshot statuses:

- `ok`
- `error`

Snapshots are runtime discovery facts. They are not the administrator-owned source of truth for enablement or overrides.

### 10.2 Managed Model

`ManagedModel` is the persisted administrative overlay for one concrete upstream model.

Current fields:

- `provider_id`
- `upstream_model`
- `credential_scope`
- `enabled`
- `capability_overrides`

Persistence key:

- `(provider_id, upstream_model)`

`ManagedModel` is stored through the config store model backend and may be supplied from static configuration or dynamic persisted records.

### 10.3 Resolved Managed Model

`ResolvedManagedModel` combines:

- the managed model overlay
- the latest provider snapshot, when available
- the effective capabilities after override application

This is the model catalog's canonical administrative read model.

## 11. Capability Semantics

Per-model capability data is authoritative for model-aware decisions.

The provider package still exposes provider-level capability summaries, but route and model correctness rely on `provider.ModelCapabilities` attached to concrete models.

Current capability resolution rules:

- provider discovery may supply per-model capabilities directly
- when discovery data is incomplete, provider summary capability metadata may be used as fallback inference
- `ManagedModel.CapabilityOverrides` replaces discovered capability values for the managed model view

The effective capability model is therefore:

- discovered capability facts from provider model listing
- corrected, when necessary, by managed model overrides

## 12. Provider Discovery Contract

The model catalog uses `provider.Provider.ListModels(...)` as its discovery entrypoint.

Provider requirements in the current architecture:

- return upstream model identifiers that can be used in real requests
- return per-model capability data when available
- expose a stable provider config through `Provider.Config()`

When provider discovery fails:

- the catalog records an error snapshot for that provider
- administrative visibility is preserved through `last_error`
- the failure does not mutate persisted managed model records

## 13. Config Store Contract

The model catalog persists managed model overlays through `pkg/configstore/intf.ModelStorer`.

The config store entrypoint remains `ConfigStorer`, which vends `GetModelStore(...)`.

Managed model persistence is limited to administrator-owned state such as:

- enabled or disabled state
- credential scope
- capability overrides

Transient runtime state is not persisted in the model store. Provider discovery freshness stays in runtime memory as snapshot state.

## 14. Administrative APIs

The gateway Admin API exposes model catalog operations under `/admin/models/...`.

Current API families:

- `GET /admin/models/providers/{provider_id}/discovered`
  - list provider snapshots for one provider
- `POST /admin/models/providers/{provider_id}/refresh`
  - refresh provider model discovery
- `GET /admin/models/managed`
  - list managed models
- `GET /admin/models/managed/{provider_id}/{upstream_model}`
  - read one managed model
- `POST /admin/models/managed`
  - create one managed model
- `PUT /admin/models/managed/{provider_id}/{upstream_model}`
  - update one managed model
- `DELETE /admin/models/managed/{provider_id}/{upstream_model}`
  - delete one managed model
- `GET /admin/models/logical`
  - logical model view endpoint family registered by the admin surface

The gateway Admin API does not expose Caddy server management APIs. Model catalog administration is part of the gateway control plane, not the Caddy control plane.

## 15. Static Configuration Shape

Static route configuration in the Caddy-based runtime lives in the global `agent_gateway` block.

Current route target directives support both modes:

- logical-model mode through `target model ...`
- direct-provider mode through `target provider <provider-id>`

Logical-model route example:

```caddy
route openai-chat {
    llm_api openai
    path_prefix /
    require_virtual_key
    target model chat-default openai-main gpt-4.1 weight 100 default
    target model chat-fast openai-main gpt-4.1-mini weight 100
    target model chat-fast zhipu-main glm-4.5-air weight 50
}
```

Direct-provider route example:

```caddy
route openai-pass-through {
    llm_api openai
    path_prefix /
    require_virtual_key
    target provider openai-main
}
```

These directives compile into the normalized `RouteTargetPolicy` shape described above.

## 16. Naming And Terminology Rules

Use the following terms consistently in code and documentation:

- `agent-gateway`
  - project and module name
- `agent_gateway`
  - Caddy app module ID
- `AgentRoute`
  - route runtime object
- `VirtualKey`
  - caller-facing gateway key
- `ManagedModel`
  - persisted administrative overlay for one upstream model
- `ProviderModelSnapshot`
  - discovered upstream model fact record
- `RouteModelTarget`
  - route-local logical model target
- `DirectProviderTarget`
  - direct provider forwarding target

Do not reintroduce:

- legacy repository naming
- legacy wording that treats the gateway as a Caddy-only product
- route terminology that conflates logical route models with upstream provider model names

## 17. Compatibility Rules

The route model retains compatibility fields during normalization:

- `provider_id`
  - normalized into `provider_target.provider_id`
- `models`
  - normalized into `model_targets`

New code and new documentation must use the normalized shape:

- `target_policy.provider_target.provider_id`
- `target_policy.model_targets`
- `target_policy.default_model`

## 18. Summary

The current routing architecture is model-first at the route boundary and provider-concrete at execution time.

Its stable design rules are:

- clients target route model names in logical-model mode
- routes may intentionally bypass that through explicit direct-provider mode
- providers remain the execution backend, not the primary route abstraction
- model discovery facts and managed model overrides are separate concerns
- the model catalog is shared runtime infrastructure, not an isolated admin-only subsystem

This is the current `agent-gateway` architecture and the normative technical specification for model-first routing in the repository.
