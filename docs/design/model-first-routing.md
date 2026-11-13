# Model resources and LLM routing

Capability status: **Implemented**.

## Ownership

LLM ingress and model scheduling have independent lifecycles:

```text
LLM Route -> ProviderModel -> Provider
          -> LogicalModel -> candidate ProviderModels -> Providers
```

A Provider owns connection and upstream network configuration. A ProviderModel
identifies one concrete `(provider_id, upstream_model)` and owns its enabled
state, capability overrides and optional credential scope. Discovery snapshots
supply observed model facts; they are not automatically exposed to clients.
ProviderModel is the concrete model resource, not a logical alias.

A LogicalModel owns its stable `id`, description, disabled state, candidate
ProviderModel references, selection strategy and bounded candidate fallback.
Candidates reference the ProviderModel compound identity and carry `weight` and
`priority`. LogicalModels cannot contain other LogicalModels. The same definition
can serve OpenAI, Anthropic and CC ingress without copying scheduling policy.

An LLM Route owns matching, protocol, VirtualKey requirements, credential
selection, explicit model exposure and a default route-visible model name. It
never owns candidates, weights, priorities or fallback.

## Route contracts

`target_policy.type: models` contains `model_targets` and optional `default_model`.
Each target has a unique route-visible `name` and exactly one reference:

- `kind: provider-model`: `provider_id` and `upstream_model`;
- `kind: logical-model`: `logical_model_id`.

A route exposes exactly one target kind. ProviderModel routes bind one Provider
and may expose several of its models; each target's `name` must equal
`upstream_model` and can be omitted on input to derive it automatically. Concrete
aliases and cross-provider targets are rejected. LogicalModel routes reference
independent LogicalModels under route-visible names; their candidates may span
Providers. Mixing the two kinds is rejected, including in disabled routes.

Request `model` selects a target's name. If omitted, `default_model` selects that
name. Adding a catalog model never broadens the route's explicit allowlist.
Cross-provider selection, fallback and stable aliases belong to LogicalModels.

All LLM routes resolve explicit models. ProviderModel fixes both the Provider
and upstream model; request `model` cannot override that binding or select an
unexposed upstream model. An omitted model uses the route's `default_model`;
Provider defaults never select a gateway route target.

Caddyfile, adapted Caddy JSON and standalone startup Bundles support independent
ProviderModels and LogicalModels. Static definitions are read-only through Admin;
edit their source and reload/restart. Static/dynamic identity collisions are
rejected rather than silently shadowed. Bundle `providerModels` and `logicalModels`
feed the same model catalog snapshots used by dynamic management.

## Execution

Protocol parsing derives generic capability and atomic protocol requirements.
A concrete target resolves one enabled ProviderModel and Provider. A logical
target reads its independent definition and filters its candidate ProviderModels
by enabled state, Provider availability and those same requirements. Resolution
rewrites an attempt-local request, preserving the caller's route-visible model.
Usage distinguishes the route, client model, logical model ID when applicable,
Provider and upstream model.

Selection strategies are `priority` (highest priority, stable configuration order
for ties), `weighted` (positive weights; first eligible candidate when all weights
are zero), and `auto` (highest priority tier, then weighted selection within it).
The default is `auto`. Each LogicalModel has 1–1024 candidates; weights range
from 0 to 1,000,000. Negative weights and duplicate concrete bindings are invalid.

LogicalModel `fallback.enabled` and `fallback.max_num` permit up to five candidate
reselections inside a live request. Existing credential retries occur on the same
binding first. Only retryable failures advance candidates. A returned stream
reader ends this retry boundary; later stream failures never restart generation.
Provider network retries remain Provider-owned.

Discovery refresh is explicit through Admin. Request-time model resolution reads
in-memory snapshots, never ConfigStore or discovery APIs. Model changes publish
defensive copies; failed refresh retains the last validated generation.

## Integrity and persistence

`provider_models` uses SQLite `model_configs` with the concrete compound key;
`logical_models` uses a separate `logical_models` table keyed by `id`.
The physical concrete model table is unchanged. The current API and Bundle names
are ProviderModel, `/admin/llm/models/provider`, and `providerModels`.

Composed manager mutations share the control-plane coordinator. Route writes
require referenced models, and LogicalModel writes require their candidate
ProviderModels and Providers. Disabled references still prevent deletion.
ProviderModel deletion is blocked by concrete route targets and logical
candidates. LogicalModel deletion is blocked by route references. Provider
deletion is blocked by exposed concrete models, logical
candidates and credentials; otherwise its unreferenced ProviderModels are removed
with it in one transaction.

Resource writes and Bundle apply remain independent and sequential. Apply order
is Providers, ProviderModels, LogicalModels, then LLM Routes. Static models are validated before serving. No cross-resource
publication transaction or group recovery API is introduced.

## Cutover

The removed `direct-provider` target and `provider_target` require explicit offline conversion with a model allowlist. The old inline `type: logical-model`, route `model_selector_strategy`, route
`fallback`, and target `candidates`/strategy/default-candidate fields are rejected.
There are no runtime aliases or dual reads. Use the stopped-service offline
`extract-models` repair operation to create independent definitions and replace
route targets atomically. See [recovery](../guides/architecture-config-recovery.md).
