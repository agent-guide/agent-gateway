# Gateway Bundle YAML Design

## 1. Goal

This document proposes a YAML-based bundle format for managing multiple gateway objects with better operator ergonomics in `agwctl`, while also allowing `agwd` to load the same object schema as read-only static configuration.

The main usability problems this proposal targets are:

- `agwctl` currently manages providers, routes, virtual keys, and managed models one object at a time
- multi-object changes require multiple JSON files and multiple command invocations
- JSON is less convenient than YAML for hand-written operator workflows

This proposal is intentionally limited to gateway object configuration. It does not try to replace every daemon process flag or every runtime state surface.

## 2. Current Constraints

The design must respect the current architecture in the repository:

- static objects and persisted dynamic objects are different configuration sources
- static objects are exposed through Admin API list/get responses as read-only
- dynamic objects are mutated through the Admin API and stored in the config store
- `agwd --config` currently means SQLite config store path, not a generic config file path

That means one YAML file can define the same object schema for two use cases, but the semantics must stay different:

- `agwctl apply` means declarative create-or-update against the dynamic Admin API
- `agwd --static-config` means startup-only read-only static object loading

The proposal must not collapse those two modes into one ambiguous behavior.

## 3. Non-Goals

This proposal does not do the following in its initial version:

- replace the Caddyfile-based static configuration path
- replace SQLite as the dynamic config store
- make credentials or CLI login state part of a checked-in YAML file
- make file-driven config automatically mutable through the Admin API
- define full bidirectional sync semantics
- define automatic deletion of remote objects by default

## 4. Proposal Summary

Introduce a shared YAML bundle schema for a set of gateway objects.

Two commands consume that schema:

```bash
agwctl gateway apply -f gateway.yaml
agwctl gateway validate -f gateway.yaml
agwctl gateway export -o gateway.yaml

agwd --config-store ./data/configstore.db --static-config ./gateway.yaml
```

The same file shape is used in both places, but with two clearly different behaviors:

- `agwctl gateway apply` writes to the dynamic config surface through the Admin API
- `agwd --static-config` loads the file as startup-time static objects that are read-only at runtime

## 5. Naming And CLI Direction

### 5.1 `agwctl`

Add a new top-level gateway subcommand:

```bash
agwctl gateway apply -f gateway.yaml
```

Recommended companion commands:

```bash
agwctl gateway validate -f gateway.yaml
agwctl gateway export -o gateway.yaml
```

Rationale:

- `apply` is the correct verb for create-or-update declarative workflows
- `validate` gives a fast local feedback loop before touching the server
- `export` makes it practical to adopt the bundle format from existing deployed state

### 5.2 `agwd`

Do not reuse `--config` for YAML input.

Current `agwd --config` already means SQLite config store path and should be renamed for clarity:

```bash
agwd --config-store ./data/configstore.db --static-config ./gateway.yaml
```

Recommended CLI changes:

- deprecate `--config`
- introduce `--config-store` with the same current meaning
- introduce `--static-config` for the new YAML bundle file

This avoids overloading one flag with incompatible meanings.

## 6. Configuration Sources And Semantics

After this proposal, standalone daemon configuration would conceptually come from two separate sources:

1. dynamic config store
2. optional static bundle YAML

They should map to existing runtime semantics:

- static bundle objects are equivalent to Caddyfile static objects
- static bundle objects are read-only through the Admin API
- dynamic config store objects remain writable through the Admin API

The precedence model should remain consistent with the existing codebase:

- static objects are a separate source, not just preloaded dynamic rows
- writes against static IDs must fail with read-only errors
- list/get responses should continue surfacing source and read-only metadata

## 7. Bundle Scope

The initial YAML bundle should cover only object families that are configuration-like and stable enough for file-based declaration:

- `providerTypes`
- `llmApiHandlerTypes`
- `providers`
- `managedModels`
- `routes`
- `virtualKeys`

The initial YAML bundle should explicitly exclude:

- persisted upstream credentials
- local `agwctl cliauth` state
- remote CLI login sessions and login status
- ephemeral runtime state

Rationale:

- credentials and login state are operational secrets and runtime state, not good default material for a human-edited bundle file
- mixing checked-in configuration with mutable auth state would make both semantics and security worse

## 8. Bundle Schema Shape

Suggested top-level shape:

```yaml
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle

providerTypes:
  - providerType: openai
    enabled: true

llmApiHandlerTypes:
  - llmApiHandlerType: openai
    enabled: true

providers:
  - id: openai-main
    provider_type: openai
    api_key: ${OPENAI_API_KEY}
    default_model: gpt-4.1

managedModels:
  - provider_id: openai-main
    upstream_model: gpt-4.1
    aliases:
      - chat-default

routes:
  - id: chat-prod
    llm_api: openai
    match:
      path_prefix: /
      methods: [POST]
    auth_policy:
      require_virtual_key: true
    target_policy:
      model_targets:
        - name: chat-default
          provider_id: openai-main
          upstream_model: gpt-4.1
          weight: 100
          default: true

virtualKeys:
  - key: vk-local-test
    tag: local-test
    allowed_routes:
      - chat-prod
```

Notes:

- field naming should stay aligned with the existing Admin API JSON model wherever practical
- the YAML format should be a serialization wrapper over existing runtime object shapes, not a parallel custom DSL
- the file is a bundle of objects, not a full daemon process config

## 9. Apply Semantics

`agwctl gateway apply -f gateway.yaml` should be declarative, but conservative in the first version.

Default behavior:

- create object if it does not exist
- update object if it exists and is writable
- skip object if no change is needed
- fail object if the server reports validation or read-only conflict

Default non-behavior:

- do not delete remote objects that are absent from the file

Rationale:

- create-or-update is the lowest-risk operator expectation for an initial `apply`
- deletion semantics require ownership and drift rules that do not exist yet

Recommended command output should present a plan-like summary:

- `create`
- `update`
- `skip`
- `error`

## 10. Static Load Semantics

`agwd --static-config gateway.yaml` should:

- parse the bundle file at startup
- validate references before the server starts
- initialize static providers, static managed models, static routes, and static virtual keys
- mark those objects as read-only through the normal runtime managers

Operationally, this should be equivalent to static Caddyfile-owned objects:

- visible in Admin API
- not mutable through Admin API
- merged with dynamic config store objects at read time using the same source/read-only model

This proposal does not require static bundle content to be persisted into SQLite.

## 11. Ordering And Validation

The file should be validated as one bundle before writes are attempted.

Recommended validation order:

1. `providerTypes`
2. `llmApiHandlerTypes`
3. `providers`
4. `managedModels`
5. `routes`
6. `virtualKeys`

Validation should include:

- schema validation
- duplicate ID detection inside the bundle
- provider type existence checks
- route target references to provider IDs
- virtual key allowed-route references
- basic route normalization and route definition validation

For `apply`, validation should happen in two phases:

1. local file validation before any network write
2. server-side validation through existing Admin API behavior during create/update

## 12. Secrets And Environment Expansion

The bundle format should support environment-variable expansion for secret-like fields.

Minimum recommended behavior:

- `${ENV_NAME}` expansion for scalar string values

Examples:

```yaml
providers:
  - id: openai-main
    provider_type: openai
    api_key: ${OPENAI_API_KEY}
```

The initial design should avoid adding multiple secret indirection mechanisms at once.

Possible future extensions:

- `api_key_env`
- `api_key_file`
- pluggable secret backends

These should not block the initial YAML bundle feature.

## 13. Error Model

Expected error classes for `apply`:

- file decode errors
- schema or normalization errors
- unresolved object references
- remote validation errors
- attempts to mutate read-only static objects
- partial apply failures

The command should report which object failed and why, using object identity in the message:

- provider ID
- managed model `(provider_id, upstream_model)`
- route ID
- virtual key key

If partial success happens, the summary must say so explicitly.

## 14. Export And Round-Trip Expectations

`agwctl gateway export` should produce a bundle YAML that is intended to be re-applied later.

Initial export scope should match apply scope:

- provider types
- LLM API handler types
- providers
- managed models
- routes
- virtual keys

Recommended defaults:

- exclude secrets by default or redact them where the API does not return them
- include source/read-only metadata only in an optional diagnostic mode, not in the normal re-apply file

The exported file should be optimized for operator editing, not for lossless administrative backup of every API response field.

## 15. Compatibility And Migration

This proposal is additive.

It does not remove:

- existing per-object JSON commands
- existing Admin API CRUD endpoints
- Caddyfile static configuration
- SQLite-backed dynamic configuration

Migration path:

1. existing users can keep using per-object JSON commands
2. operators who want batch workflows can adopt `gateway.yaml`
3. standalone users who want file-backed read-only objects can use `--static-config`

## 16. MVP Boundaries

The first deliverable should include:

- bundle YAML parsing
- local validation
- `agwctl gateway apply -f ...`
- `agwctl gateway validate -f ...`
- `agwd --config-store ... --static-config ...`
- static read-only integration for supported object families

The MVP should not include:

- default prune/delete
- ownership tracking across multiple apply files
- credentials in bundle YAML
- CLI login state in bundle YAML
- full drift reconciliation

## 17. Follow-Up Work

After MVP, likely follow-up items are:

- `agwctl gateway export`
- optional `--prune` with explicit ownership rules
- `diff` or `plan` mode
- better secret indirection
- documentation examples alongside `Caddyfile.example`
- tests covering mixed static + dynamic object behavior in standalone mode

## 18. Recommended Decision

Proceed with the YAML bundle proposal, but keep these two modes explicitly separate:

- `agwctl apply` is declarative dynamic mutation
- `agwd --static-config` is startup-only static configuration

Do not make one YAML file path imply one storage model.

The core design rule is:

- shared object schema
- different runtime semantics

That matches the current architecture and improves UX without weakening the static-versus-dynamic boundary already present in the project.
