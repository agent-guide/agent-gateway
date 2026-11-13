# Configure concrete and logical models

Use a ProviderModel to expose a registered concrete model. Use a LogicalModel
when clients need a stable model backed by selectable concrete candidates.
Both are exposed through the same `models` route policy.

1. Create Providers with their connection settings.
2. Register ProviderModels by `(provider_id, upstream_model)`, with `enabled: true`.
3. Refresh discovery through `POST /admin/llm/models/providers/{provider_id}/refresh`,
   or supply `capability_overrides` for known model capabilities.
4. Create a LogicalModel when candidate selection or fallback is needed.
5. Create a route with explicit model references and route-visible names.
6. Grant the route to a VirtualKey.

The complete [model Bundle example](../../examples/gateway.bundle.llm.logical-model.yaml)
includes Providers, ProviderModels, a shared LogicalModel, a logical route and
a VirtualKey. Apply it with:

```sh
agwctl validate -f examples/gateway.bundle.llm.logical-model.yaml
agwctl apply -f examples/gateway.bundle.llm.logical-model.yaml
```

The route target policy is:

```yaml
type: models
default_model: chat
model_targets:
  - name: chat
    kind: logical-model
    logical_model_id: chat-default
```

Clients submit `model: chat`. An omitted model uses `chat`.
Other names are rejected even if they exist in the model catalog.

For a concrete route, use this policy instead:

```yaml
type: models
default_model: gpt-4.1
model_targets:
  - kind: provider-model
    provider_id: openai-main
    upstream_model: gpt-4.1
```

ProviderModel routes allow several models from one Provider. The client uses the
real upstream model name; `name` is derived from `upstream_model` when omitted,
and a differing name is rejected. These routes have no candidate fallback.
A route cannot mix ProviderModel and LogicalModel targets or directly expose
multiple Providers. Use a LogicalModel for cross-provider selection or aliases.

Configure `strategy`, candidate weights and
priorities, and `fallback` on the independent LogicalModel.

Reuse the same LogicalModel from additional `openai`, `anthropic` or `cc` routes.
An update to the LogicalModel affects every reference without rewriting routes;
the protocol adapter still filters candidates against the request's requirements.

Management endpoints:

| Resource | Collection | Item identity |
|---|---|---|
| ProviderModel | `/admin/llm/models/provider` | `/{provider_id}/{upstream_model}` |
| LogicalModel | `/admin/llm/models/logical` | `/{id}` |

Collections support GET/POST; items support GET/PUT/DELETE. A PUT body identity
must agree with the path when present. Release references before deletion,
including references in disabled routes or LogicalModels.

Use `agwctl model provider list|get|delete` and
`agwctl model logical list|get|delete` to inspect or remove models. Create and
update them through Admin or Bundle apply; `agwctl export` includes both families.

Credential selection settings remain route-owned. Model targets can select the
ProviderModel's custom credential scope before Provider-scoped credentials.
All route targets resolve registered models; there is no Provider passthrough.
Static Bundles support the same `providerModels`, `logicalModels` and route
references. In Caddyfile, use `provider_model <provider-id> <upstream-model>` and
`target model <provider-id> <upstream-model>`. Logical resources use
`logical_model <id>` with candidates, and routes use
`target logical <name> <logical-model-id>`. Static models are read-only via Admin.
