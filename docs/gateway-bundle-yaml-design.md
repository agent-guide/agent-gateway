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
- make CLI login state part of a checked-in YAML file
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

Configuration-type objects are:

- `providerTypes`
- `llmApiHandlerTypes`
- `providers`
- `managedModels`
- `routes`
- `virtualKeys`
- `credentials`

Operational-type objects are:

- `cliauth`

The current implemented bundle path covers:

- `providerTypes`
- `llmApiHandlerTypes`
- `providers`
- `managedModels`
- `routes`
- `virtualKeys`

The current implemented bundle path does not yet cover:

- `credentials`
- local `agwctl cliauth` state
- remote CLI login sessions and login status
- ephemeral runtime state

Rationale:

- configuration-type objects should converge on one declarative bundle workflow
- `cliauth` is action-oriented runtime control and should stay command-driven
- credentials are configuration-type objects, but they need separate follow-up design because secret handling and file representation require more care than the initial implemented object families

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

- existing Admin API CRUD endpoints
- Caddyfile static configuration
- SQLite-backed dynamic configuration

It does remove from the recommended CLI path:

- per-object JSON `create` / `update` / `upsert` commands for configuration-type objects

Migration path:

1. configuration-type objects move to `gateway.yaml` as the formal write path
2. operational workflows continue to use command-oriented subcommands
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

## 18. Implementation Plan

This section turns the proposal into a concrete execution sequence that can be implemented incrementally.

The recommended strategy is:

1. define one shared bundle data model and parser first
2. add local validation second
3. add `agwctl gateway validate`
4. add `agwctl gateway apply`
5. add `agwd --static-config`
6. update docs and examples only after the behavior is stable

That order keeps parsing and validation logic shared, reduces rework, and avoids building CLI surfaces before the underlying model is settled.

### 18.1 Phase 1: Shared Bundle Package

Goal:

- define the YAML bundle schema once in a reusable runtime package

Suggested work:

- add a new package such as `pkg/gatewaybundle/`
- define the top-level `GatewayBundle` Go type
- define typed list items for:
  - provider type enablement
  - LLM API handler type enablement
  - provider config
  - managed model
  - route
  - virtual key
- add YAML decode helpers
- add environment-variable expansion for scalar strings

Suggested outputs:

- `LoadFile(path string) (*GatewayBundle, error)`
- `DecodeYAML(data []byte) (*GatewayBundle, error)`
- `ExpandEnv()` or equivalent decode-time expansion behavior

Why first:

- both `agwctl` and `agwd` depend on the same file parser
- this isolates YAML concerns from command wiring

### 18.2 Phase 2: Local Validation

Goal:

- validate a bundle fully before attempting network writes or daemon startup

Suggested work:

- add `Validate()` on the bundle type
- normalize embedded provider, route, managed model, and virtual key objects using existing model helpers where possible
- detect duplicate IDs and invalid references across the bundle
- validate bundle metadata such as `apiVersion` and `kind`

Validation checklist:

- duplicate provider IDs
- duplicate route IDs
- duplicate virtual key keys
- duplicate managed model `(provider_id, upstream_model)`
- unknown provider types
- unknown LLM API handler types
- route references to missing providers
- virtual key references to missing routes
- route definition validation through existing route validation helpers

Suggested outputs:

- a structured error list type for multi-error reporting
- human-readable validation output for CLI use

### 18.3 Phase 3: `agwctl gateway validate`

Goal:

- expose bundle parsing and validation without touching the server

Suggested work:

- add `validate` under `cmd/agwctl/cmd_gateway.go`
- accept `-f, --file`
- load the bundle through the shared package
- print success summary or validation failures

Recommended behavior:

- exit non-zero on any validation error
- print object-qualified errors
- do not require `--addr`, `--user`, or `--password`

Why this phase matters:

- it gives a tight feedback loop before implementing remote mutation
- it makes the bundle format independently testable and usable

### 18.4 Phase 4: `agwctl gateway apply`

Goal:

- support multi-object create-or-update through the existing Admin API

Suggested work:

- add `apply` under `cmd/agwctl/cmd_gateway.go`
- reuse the shared parser and validation logic
- add an internal apply planner/executor, likely in `internal/agwctl/` or `pkg/adminclient/`-adjacent code
- compare desired bundle state with current remote state
- perform ordered create or update operations

Recommended execution order:

1. provider types
2. LLM API handler types
3. providers
4. managed models
5. routes
6. virtual keys

Recommended implementation details:

- fetch current remote state per object family
- compute `create`, `update`, `skip`, `error`
- use semantic equality where needed to avoid noisy updates
- stop on bundle-level validation failure before the first write

Open design choice for this phase:

- whether apply should continue after one object failure or stop immediately

Recommended MVP behavior:

- continue per object family where safe
- return a final non-zero exit code if any object failed
- print a summary with counts and failed object identities

### 18.5 Phase 5: `agwd --static-config`

Goal:

- allow standalone daemon startup with read-only static bundle objects in addition to the dynamic config store

Suggested work:

- update `cmd/agwd/main.go`
- introduce `--config-store`
- keep `--config` as deprecated alias for one transition period if needed
- add `--static-config`
- update `standalone/server.Options`
- load and validate the bundle before gateway bootstrap

Runtime integration work:

- extend standalone bootstrap to pass static providers, managed models, routes, and virtual keys into `gateway.BootstrapOptions`
- align standalone bootstrap behavior with the existing `caddy/gateway.App` static object wiring

Important constraint:

- static bundle objects must remain read-only through Admin API mutation paths

### 18.6 Phase 6: Documentation And Examples

Goal:

- make the feature discoverable and safe to adopt

Suggested work:

- update `README.md`
- update `AGENTS.md` only if command semantics change materially enough to matter for contributors
- add example bundle YAML under `examples/`
- document the difference between:
  - static bundle config
  - dynamic config store
  - existing per-object JSON CRUD

Recommended example files:

- `examples/gateway.bundle.minimal.yaml`
- `examples/gateway.bundle.logical-model.yaml`

## 19. Concrete Step List

This is the recommended execution checklist for actually delivering the feature.

### Step 1: Settle Data Model

- create `pkg/gatewaybundle/`
- define `GatewayBundle`
- define metadata fields: `apiVersion`, `kind`
- define object family fields:
  - `ProviderTypes`
  - `LLMAPIHandlerTypes`
  - `Providers`
  - `ManagedModels`
  - `Routes`
  - `VirtualKeys`
- choose exact YAML field naming and keep it aligned with existing JSON models

Exit criteria:

- one package can load a valid bundle YAML into Go structs

### Step 2: Add Parser And Env Expansion

- wire `gopkg.in/yaml.v3`
- add file read and decode helpers
- add `${ENV_VAR}` expansion
- define behavior for missing env vars

Recommended missing-env policy for MVP:

- fail validation if a referenced env var is missing

Exit criteria:

- bundle file can be parsed deterministically with clear error messages

### Step 3: Add Local Validation

- validate metadata
- validate duplicates
- validate references
- call existing normalize/validate helpers on route and provider-like objects where available
- add tests for valid and invalid bundles

Exit criteria:

- invalid bundles fail without any network or startup side effects

### Step 4: Add `agwctl gateway validate`

- add Cobra command
- add `-f, --file`
- print validation summary
- add CLI tests

Exit criteria:

- operators can validate bundle files locally

### Step 5: Add Apply Planner

- define in-memory apply actions
- fetch remote state
- diff desired versus actual
- classify operations as `create`, `update`, or `skip`
- decide error aggregation format

Exit criteria:

- planner output can be tested independently from command UX

### Step 6: Add `agwctl gateway apply`

- add Cobra command
- call parser, validator, and planner
- execute operations in dependency order
- print summary
- add integration-style tests against test HTTP servers

Exit criteria:

- one command can materialize a multi-object bundle remotely

### Step 7: Refactor `agwd` Flags

- introduce `--config-store`
- keep compatibility path for old `--config` if desired
- add `--static-config`
- update help text and docs

Exit criteria:

- CLI flag semantics are no longer ambiguous

### Step 8: Add Standalone Static Bundle Loading

- load bundle before bootstrap
- validate bundle
- pass static objects into gateway bootstrap
- ensure Admin API exposes them as read-only
- add tests for mixed static + dynamic behavior

Exit criteria:

- `agwd` can start from SQLite plus optional read-only bundle YAML

### Step 9: Add Examples And User Docs

- add example YAML files
- update README command examples
- document migration from per-object JSON files

Exit criteria:

- a new user can discover and use the feature from docs alone

## 20. Suggested Pull Request Breakdown

To keep reviewable changes small, split implementation into a few focused PRs.

Recommended PR sequence:

1. shared bundle package plus parser tests
2. bundle validation plus `agwctl gateway validate`
3. apply planner plus `agwctl gateway apply`
4. `agwd --config-store` and `--static-config`
5. docs, examples, and cleanup

This sequence keeps each PR narrow and reduces the chance of mixing CLI surface design with runtime bootstrap changes.

## 21. Risks And Watchpoints

Implementation should watch for these specific risks:

- silently diverging YAML field names from existing JSON field names
- bundling unsupported object families too early
- treating static bundle objects as pre-seeded dynamic rows instead of true read-only static config
- leaking secrets in export or CLI output
- introducing `apply` deletion semantics before ownership rules are designed
- duplicating validation logic in `agwctl` and `agwd` instead of sharing one package

## 22. Recommended Decision

Proceed with the YAML bundle proposal, but keep these two modes explicitly separate:

- `agwctl apply` is declarative dynamic mutation
- `agwd --static-config` is startup-only static configuration

Do not make one YAML file path imply one storage model.

The core design rule is:

- shared object schema
- different runtime semantics

That matches the current architecture and improves UX without weakening the static-versus-dynamic boundary already present in the project.
