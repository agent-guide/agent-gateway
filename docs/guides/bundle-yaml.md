# Bundle YAML

This guide covers the gateway bundle YAML workflow used by `agwctl` and `agwd`.

## What Bundle YAML Is

Bundle YAML is the declarative configuration format for gateway objects such as:

- provider types
- providers
- provider models
- LLM routes
- VirtualKeys
- MCP connectors
- MCP routes
- registered extension envelopes

It is intentionally for configuration objects, not every runtime operation.

## Main Workflows

### Dynamic Remote Workflow With `agwctl`

Use this when you want to validate, export, and apply configuration against a running gateway:

```bash
./agwctl export -f ./gateway.bundle.yaml
./agwctl validate -f ./gateway.bundle.yaml
./agwctl apply -f ./gateway.bundle.yaml
```

Behavior:

- `export` reads current remote config objects and serializes them as bundle YAML
- `validate` parses and validates the bundle locally
- `apply` creates or updates remote config through the Admin API

### Static Startup Workflow With `agwd`

Use this when you want startup-only read-only static config:

```bash
./agwd --config-store ./data/configstore.db \
  --provider-type openai \
  --static-config ./examples/gateway.static.minimal.yaml
```

Behavior:

- `--static-config` loads the bundle at startup
- `--provider-type` is startup-only provider type availability; repeat it to allow multiple types
- `--credential-refresh-command` selects the external request-time credential refresher; repeat `--credential-refresh-arg` for static arguments (together they default to `agw-auth refresh`)
- loaded objects are treated as static read-only runtime objects
- static bundle objects are not pre-seeded into SQLite as writable rows

## Minimal Bundle Example

```yaml
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle

providers:
  - id: openai-main
    provider_type: openai
    api_key: ${OPENAI_API_KEY}
    default_model: gpt-4.1

providerModels:
  - provider_id: openai-main
    upstream_model: gpt-4.1
    enabled: true
    capability_overrides: {streaming: true, tools: true}

llmRoutes:
  - id: chat-prod
    protocol: openai
    match_policy:
      path_prefix: /
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      type: models
      default_model: gpt-4.1
      model_targets:
        - name: gpt-4.1
          kind: provider-model
          provider_id: openai-main
          upstream_model: gpt-4.1
```

See:

- [examples/gateway.static.minimal.yaml](../../examples/gateway.static.minimal.yaml)
- [examples/gateway.bundle.llm.provider-model.yaml](../../examples/gateway.bundle.llm.provider-model.yaml)

## Model Bundle Example

Use `providerModels` for concrete model facts, `logicalModels` for shared candidate
selection and fallback, and `llmRoutes` with `target_policy.type: models` to expose
explicit references under route-visible names. Apply creates Providers first,
then ProviderModels, LogicalModels and LLM Routes. Each write is independent.

See the [complete example](../../examples/gateway.bundle.llm.logical-model.yaml)
and [configuration guide](logical-model-routing.md).

## Static Versus Dynamic Restrictions

Current static restrictions:

- `agwd --static-config` supports model declarations and references
- `agwd --static-config` supports read-only `providerModels` and `logicalModels`
- `agwd --static-config` does not support `virtualKeys`

Current dynamic workflow behavior:

- `agwctl apply` supports logical-model routes
- `agwctl apply` supports provider models
- VirtualKeys are valid in config-store-backed bundle workflows
- VirtualKeys may include optional request-frequency limits:

```yaml
virtualKeys:
  - id: team-a
    allowed_route_ids: [chat-prod]
    rate_limits:
      llm:
        requests_per_minute: 60
        burst: 10
      mcp:
        requests_per_minute: 120
        burst: 20
      agent:
        requests_per_minute: 20
        burst: 5
```

Each configured rate and burst must be greater than zero. Omitted dimensions
are unlimited. `agent` supplies the unified Agent ingress bucket.

## Schema Notes

Top-level metadata:

- `apiVersion: gateway.agw/v1alpha1`
- `kind: GatewayBundle`

Common top-level sections:

- `providers`
- `providerModels`
- `logicalModels`
- `llmRoutes`
- `virtualKeys`
- `connectors`
- `mcpRoutes`
- `agents`
- `agentRoutes`
- `extensions`

Managed credentials are not included in bundle apply/export. Treat an exported
bundle as gateway configuration, not as a complete backup: back up and restore
credentials separately through the credentials Admin API or manager UI.

## Current Caveat

Some older example files in the repository still use older field names in parts of the bundle examples. For new bundle authoring, follow the current runtime route schema documented here and in the test-backed examples that use `protocol`.

## Related Docs

- [../reference/agwctl-reference.md](../reference/agwctl-reference.md)
- [../design/gateway-bundle-yaml.md](../design/gateway-bundle-yaml.md)
- [logical-model-routing.md](logical-model-routing.md)

## Linked extension configuration

```yaml
extensions:
  - module_id: online.agentguide.community.limiter
    schema_version: 1
    config:
      timeout_ms: 100
      failure_mode: closed
```

Each envelope belongs to a linked module and its schema. Unknown modules fail
local and server validation. All extension envelopes use the existing ConfigStore
and `/admin/extensions/{module_id}`. Use Bundle apply to persist configuration;
`agwd --static-config` rejects non-empty extensions. Caddyfile and Caddy JSON
also reject extension configuration.
Apply orders extensions by declared dependencies before other resources, stops
on extension failure and reports partial results. It has no whole-Bundle rollback.
Each serialized resource shares the Admin 4 MiB received-body boundary.

Factory-declared secret fields use `{"$secret_ref":"env:NAME"}`. They are
classified before ordinary `${NAME}` expansion. Local validation checks syntax
without resolving the reference; server preparation receives resolved secrets
separately. Storage and export preserve the reference token. Preparation or
persistence failure leaves the serving generation active. See the
[custom distribution guide](custom-distribution.md) for module assembly.
