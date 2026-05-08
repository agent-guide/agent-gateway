# Route Target Policy Design

## Goal

Refactor route target selection around a `TargetPolicy` interface with two concrete policies:

1. `DirectProviderPolicy`
2. `LogicalModelPolicy`

The new design separates:

- route-facing target declaration
- runtime model and credential scheduling
- request execution and fallback control

It also introduces a routed execution wrapper named `RoutedProvider` to replace the current `authManagedProvider`.

## Why Change

The current route target shape mixes two route modes into one struct:

- direct provider routing via `provider_target`
- logical model routing via `model_targets`

That works for simple resolution, but it becomes awkward once route-level behavior needs to express:

- different target kinds with different required fields
- model selection strategy
- credential selection strategy
- credential scope precedence
- credential source precedence
- bounded fallback behavior
- runtime result marking and future request accounting

The design should move from a passive config struct to an explicit policy abstraction.

## Design Principles

1. `TargetPolicy` is a route-level interface, not a bag of optional fields.
2. Direct-provider and logical-model routes are first-class, mutually exclusive policy kinds.
3. Model selection and credential selection are both part of target policy, because they jointly decide the real upstream execution target.
4. Fallback is a runtime concern owned by `RoutedProvider`, not by protocol adapters.
5. `ResolvedTarget` should stay minimal and represent one concrete execution choice, not the full route policy.
6. Credential scheduling and model scheduling must be re-entrant so the runtime can re-select credential or model after failure.
7. The design should support future extensions such as per-route metrics, request accounting, circuit breaking, and adaptive scheduling.

## High-Level Architecture

```text
AgentRoute
  -> TargetPolicy (interface)
      -> DirectProviderPolicy
      -> LogicalModelPolicy
  -> ResolveExecutionPlan(...)
  -> RoutedProvider.Execute(...)
      -> select model candidate if needed
      -> select credential
      -> invoke concrete provider
      -> mark result
      -> decide retry / reselect credential / reselect model
```

### Responsibility Split

`TargetPolicy` owns:

- route configuration shape
- static validation rules
- candidate enumeration rules
- scheduler preferences

`RoutedProvider` owns:

- runtime model selection
- runtime credential selection
- provider invocation
- result marking
- fallback loop
- request-level execution stats

Protocol adapters own:

- wire-format parsing
- conversion between HTTP payload and `provider.ChatRequest`
- conversion between provider response and protocol response

## Route Data Model

`AgentRoute` should hold a policy interface value.

Suggested shape:

```go
type AgentRoute struct {
    ID              string               `json:"id"`
    Description     string               `json:"description,omitempty"`
    Disabled        bool                 `json:"disabled"`
    LLMAPI          string               `json:"llm_api,omitempty"`
    Match           RouteMatch           `json:"match"`
    TargetPolicy    RouteTargetPolicy    `json:"target_policy,omitempty"`
    AuthPolicy      RouteAuthPolicy      `json:"auth_policy"`
    RateLimitPolicy RouteRateLimitPolicy `json:"rate_limit_policy,omitempty"`
    QuotaPolicy     RouteQuotaPolicy     `json:"quota_policy,omitempty"`
    CreatedAt       time.Time            `json:"created_at"`
    UpdatedAt       time.Time            `json:"updated_at"`
}

type RouteTargetPolicy interface {
    PolicyKind() RouteTargetPolicyKind
    Validate() error
    Normalize()
}
```

Because Go interfaces do not unmarshal from JSON directly, persisted route JSON should use a tagged wrapper:

```go
type RouteTargetPolicyEnvelope struct {
    Type string          `json:"type"`
    Raw  json.RawMessage `json:"-"`
}
```

Or more practically:

```go
type RouteTargetPolicy struct {
    Type string          `json:"type"`
    Spec json.RawMessage `json:"spec,omitempty"`
}
```

At decode time:

- `type=direct-provider` -> decode to `DirectProviderPolicy`
- `type=logical-model` -> decode to `LogicalModelPolicy`

This keeps storage stable while still giving runtime an interface-backed model.

## Policy Types

### DirectProviderPolicy

Used when the route always forwards to one provider and the request model remains the upstream model.

```go
type DirectProviderPolicy struct {
    ProviderID            string                        `json:"provider_id"`
    CredentialSelector    RouteCredentialSelectStrategy `json:"credential_selector,omitempty"`
    CredentialScopeOrder  []RouteCredentialScope        `json:"credential_scope_order,omitempty"`
    CredentialSourceOrder []RouteCredentialSource       `json:"credential_source_order,omitempty"`
}
```

Semantics:

- `provider_id` is required
- request `model` is not rewritten by route resolution
- credential selection still happens through `RoutedProvider`
- `credential_scope_order` decides which credential scope families are searched first

### LogicalModelPolicy

Used when the route exposes logical model names and resolves them to one concrete `(provider_id, upstream_model)` binding.

```go
type LogicalModelPolicy struct {
    Models                 []LogicalModelBindingGroup      `json:"models,omitempty"`
    DefaultModel           string                          `json:"default_model,omitempty"`
    ModelSelectorStrategy  RouteModelSelectorStrategy      `json:"model_selector_strategy,omitempty"`
    CredentialSelector     RouteCredentialSelectStrategy   `json:"credential_selector,omitempty"`
    CredentialScopeOrder   []RouteCredentialScope          `json:"credential_scope_order,omitempty"`
    CredentialSourceOrder  []RouteCredentialSource         `json:"credential_source_order,omitempty"`
    Fallback               RouteFallbackPolicy            `json:"fallback,omitempty"`
}

type LogicalModelBindingGroup struct {
    Name       string                  `json:"name"`
    Candidates []LogicalModelCandidate `json:"candidates,omitempty"`
}

type LogicalModelCandidate struct {
    ProviderID    string `json:"provider_id"`
    UpstreamModel string `json:"upstream_model"`
    Priority      int    `json:"priority,omitempty"`
    Weight        int    `json:"weight,omitempty"`
    Default       bool   `json:"default,omitempty"`
}
```

Semantics:

- each route-visible logical model name maps to one candidate set
- `default_model` is optional but recommended for adapters that allow omitted request model
- `model_selector_strategy` applies to the selected logical model's candidate set
- credential scheduling is route-level and consistent across all candidates in the policy
- fallback controls request-time candidate reselection bounds

## Types And Constants

This design should not redefine string enums that already exist in the codebase with the same semantics.

### Reuse Existing Definitions

The following existing definitions should be reused directly:

- credential sources:
  - `credentialmgr.SourceAPIKey`
  - `credentialmgr.SourceCLIAuthToken`
- provider-id scope helpers:
  - `credentialmgr.CredentialScopeProviderIDPrefix`
  - `credentialmgr.ProviderIDCredentialScope(...)`
- managed model credential scope field:
  - `modelcatalog.ManagedModel.CredentialScope`
- credential scheduling behaviors already implemented in scheduler:
  - `scheduler.RoundRobinSelector`
  - `scheduler.FillFirstSelector`

Implication:

- route config may still use the strings `api_key`, `cliauth_token`, `round_robin`, and `fill_first`
- but the route-layer design should map them onto existing credential-manager and scheduler semantics instead of introducing duplicate route-only constants unless implementation needs a thin adapter type

### New Or Updated Route-Level Types

The following concepts are route-policy concepts and should live in `gateway/route`:

```go
type RouteTargetPolicyKind string

const (
    RouteTargetPolicyKindDirectProvider RouteTargetPolicyKind = "direct-provider"
    RouteTargetPolicyKindLogicalModel   RouteTargetPolicyKind = "logical-model"
)

type RouteModelSelectorStrategy string

const (
    RouteModelSelectorPriority RouteModelSelectorStrategy = "priority"
    RouteModelSelectorWeighted RouteModelSelectorStrategy = "weighted"
    RouteModelSelectorAuto     RouteModelSelectorStrategy = "auto"
)

type RouteCredentialScope string

const (
    RouteCredentialScopeModelCustom RouteCredentialScope = "model_custom"
    RouteCredentialScopeProviderID  RouteCredentialScope = "provider_id"
)
```

### Existing Route Type That Should Be Updated

`gateway/route/types.go` already defines:

```go
type RouteSelectionStrategy string
```

Current values include `auto`, `weighted`, `failover`, and `conditional`.

Under this design, that existing type should be updated rather than paralleled by a second competing route strategy enum:

- keep `auto`
- keep `weighted`
- add `priority`
- remove `failover` from the new target-policy design surface
- remove `conditional` from the new target-policy design surface unless another route feature still needs it

Recommendation:

- either rename `RouteSelectionStrategy` to `RouteModelSelectorStrategy`
- or keep the existing type name and update its allowed values to match the new semantics

The important constraint is to avoid having both `RouteSelectionStrategy` and `RouteModelSelectorStrategy` coexist with overlapping meanings.

## Selection Semantics

### Model Selector Strategy

`priority`

- ignore `weight`
- sort by `priority` descending
- choose the highest-priority candidate
- if multiple candidates share the same highest priority, use a stable deterministic tiebreaker
- recommended tiebreaker: preserve config order

`weighted`

- ignore `priority`
- choose across all candidates purely by `weight`
- candidates with `weight <= 0` are not eligible unless they are the only candidates

`auto`

- first group by `priority`
- select only from the highest-priority tier
- within that tier, choose by `weight`
- if all candidates in the best tier have invalid or zero weight, use stable config order within the best tier

Recommendation:

- default `auto` for logical-model routes
- use `priority` for strict primary-backup routing
- use `weighted` for homogeneous multi-provider spreading

### Credential Selector Strategy

`round_robin`

- distribute traffic across eligible credentials in the same source and scope
- preferred when multiple equivalent credentials exist

`fill_first`

- keep using one eligible credential until it becomes unavailable or unsuitable
- preferred when session stickiness or quota concentration is desirable

### Credential Scope Order

`credential_scope_order` defines how `RoutedProvider` searches candidate credential pools.

Suggested meanings:

- `model_custom`: prefer credentials using the `CredentialScope` declared on the resolved managed model metadata
- `provider_id`: prefer credentials scoped to the provider regardless of model

Important constraint:

- scope resolution must be based on the concrete candidate actually being executed, not only on the route's logical model name

Recommended concrete runtime scope expansion for a chosen candidate:

1. if `model_custom`, resolve the chosen candidate through the managed model metadata and read its `CredentialScope`
2. if `provider_id`, try `ProviderIDCredentialScope(provider_id)`

If the chosen candidate does not map to a managed model with a usable `CredentialScope`, `model_custom` should be treated as unresolved for that attempt and the runtime should continue to the next configured scope.

This design intentionally defines scope precedence, not the exact scheduler storage schema.

### Credential Source Order

`credential_source_order` defines which source families are tried first within each scope.

Example:

- `[api_key, cliauth_token]`: prefer managed API key credentials, then CLI-auth credentials
- `[cliauth_token, api_key]`: prefer CLI-auth credentials first

Provider config `api_key` participates in normal credential scheduling and source ordering. A static provider `api_key` is just one concrete credential source entry, not an implicit final fallback outside the scheduler.

## Fallback Design

### Config Shape

```go
type RouteFallbackPolicy struct {
    Enabled bool `json:"enabled,omitempty"`
    MaxNum  int  `json:"max_num,omitempty"`
}
```

Semantics:

- `enabled=false`: do not reselect model after execution failure
- `max_num`: maximum number of model-level fallback attempts per request
- `max_num` counts model reselections, not credential reselections inside one chosen candidate

Recommended defaults:

- `enabled=true` for logical-model routes
- `max_num=1` by default
- `max_num` must be bounded, for example `0..5`

### Runtime Fallback Loop

For logical-model routes, one request should follow this high-level loop:

1. resolve the logical model requested by the client
2. select one concrete candidate according to `model_selector_strategy`
3. select credential for that concrete candidate according to `credential_scope_order`, `credential_source_order`, and `credential_selector`
4. if no eligible credential exists for that candidate, mark candidate as temporarily unusable for this request and reselect another candidate
5. invoke provider request
6. if request succeeds, record success and return
7. if request fails, classify the failure
8. decide whether to:
   - reselect credential on the same candidate
   - reselect model candidate
   - stop and return error
9. enforce `fallback.max_num`

Important behavior:

- credential absence before provider invocation should count as candidate rejection, not as a provider failure
- credential reselection should not consume `fallback.max_num`
- model reselection should consume `fallback.max_num`

## Failure Classification

`RoutedProvider` should classify failures into at least three buckets:

1. `RetrySameCredential`
2. `ReselectCredential`
3. `ReselectModel`

Recommended approach:

- use a normalized gateway-level error classification first when provider adapters can expose structured failure reasons
- fall back to generic HTTP status and transport error heuristics when no structured classification is available
- avoid coupling `RoutedProvider` directly to provider-specific raw error payload parsing

This gives a practical phased path:

1. define a small gateway-owned typed error surface such as `auth_invalid`, `auth_exhausted`, `model_unavailable`, `provider_unavailable`, `rate_limited`, `bad_request`
2. let provider adapters optionally map upstream-specific failures into those normalized reasons
3. let `RoutedProvider` make fallback decisions from the normalized reason first, and from HTTP status only as a fallback

Suggested heuristics:

### Reselect Credential

Use when the failure likely belongs to the credential, not the model.

Examples:

- credential expired
- credential revoked
- upstream says invalid API key
- upstream says auth token invalid
- upstream says account quota exhausted for this credential

### Reselect Model

Use when the failure likely belongs to the provider-model target, capacity, or route candidate.

Examples:

- provider endpoint unavailable
- model overloaded
- upstream returns retryable 5xx after credential looks healthy
- upstream returns model-not-found for that provider binding
- scheduler cannot find any more usable credential under the current candidate

### Stop Immediately

Use when fallback should not hide caller-visible issues.

Examples:

- invalid client request
- route model not found
- virtual key forbidden
- unsupported capability

This classification should be centralized in `RoutedProvider`, but provider adapters should be allowed to contribute normalized typed errors so the runtime does not depend only on status code guessing.

## RoutedProvider Design

`authManagedProvider` is too narrow because it only wraps credential picking around one provider call. The new runtime needs a richer execution object.

Suggested direction:

```go
type RoutedProvider struct {
    route             *route.AgentRoute
    policy            route.RouteTargetPolicy
    providerResolver  ProviderResolver
    credentialMgr     *credentialmgr.Manager
    scheduler         sched.CredentialScheduler
    modelCatalog      *modelcatalog.Service
    statsRecorder     RequestStatsRecorder
}
```

Suggested responsibilities:

- resolve concrete provider candidate for one request
- choose credential according to route policy
- inject selected credential into context
- rewrite request model for logical-model routes
- execute `Chat`, `StreamChat`, `Embedding`, `CreateResponses`, or `StreamResponses`
- classify errors
- mark credential result
- mark candidate result
- perform bounded fallback
- expose future hooks for request accounting and metrics

Suggested helper methods:

```go
func (p *RoutedProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error)
func (p *RoutedProvider) StreamChat(ctx context.Context, req *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error)
func (p *RoutedProvider) resolveExecutionPlan(ctx context.Context, reqModel string) (*ExecutionPlan, error)
func (p *RoutedProvider) selectCandidate(ctx context.Context, state *ExecutionState) (*ResolvedCandidate, error)
func (p *RoutedProvider) selectCredential(ctx context.Context, cand *ResolvedCandidate, state *ExecutionState) (context.Context, *credentialmgr.ManagedCredential, error)
func (p *RoutedProvider) classifyFailure(err error, cand *ResolvedCandidate, cred *credentialmgr.ManagedCredential) FailureAction
func (p *RoutedProvider) markResult(ctx context.Context, res AttemptResult)
```

### Why `RoutedProvider` Belongs in `gateway`

This object is not only a provider wrapper. It coordinates route policy, credential scheduling, provider resolution, and request execution. That is gateway runtime logic, not pure provider package logic.

Placing it in `gateway` keeps ownership aligned with:

- route policy semantics
- provider resolution
- model catalog integration
- per-request fallback state

The provider package should remain focused on upstream provider adapters and shared credential-context helpers.

## Execution State Model

A request-scoped execution state should be maintained during fallback.

Suggested fields:

```go
type ExecutionState struct {
    LogicalModel            string
    TriedCandidates         map[string]int
    TriedCredentials        map[string]int
    ModelFallbackCount      int
    CredentialRetryCount    int
    LastCandidateID         string
    LastCredentialID        string
}
```

Candidate identity should be derived from at least:

- `provider_id`
- `upstream_model`

This prevents one request from repeatedly selecting the same failing target.

## Caddyfile Design

The Caddyfile should expose one explicit `target_policy` block with typed submode.

### Direct Provider

```caddy
route openai-direct {
    llm_api openai
    path_prefix /
    require_virtual_key

    target_policy direct-provider {
        provider openai-main

        credential_selector round_robin
        credential_scope_order provider_id
        credential_source_order api_key cliauth_token
    }
}
```

### Logical Model

```caddy
route openai-chat {
    llm_api openai
    path_prefix /
    require_virtual_key

    target_policy logical-model {
        model chat-default openai-main gpt-4.1 weight 100 priority 100 default
        model chat-default openai-backup gpt-4.1 weight 20 priority 100
        model chat-default openrouter-main openai/gpt-4.1 weight 10 priority 80

        model_selector_strategy auto
        credential_selector round_robin
        credential_scope_order model_custom provider_id
        credential_source_order api_key cliauth_token

        fallback {
            enabled true
            max_num 2
        }
    }
}
```

Parsing rules:

- `target_policy <kind> { ... }` is required
- `kind` must be `direct-provider` or `logical-model`
- direct-provider block may not define `model ...`
- logical-model block may not define `provider ...`

## JSON Storage Shape

Routes can still be persisted as JSON blobs with a tagged policy object.

### Direct Provider JSON

```json
{
  "id": "openai-direct",
  "llm_api": "openai",
  "match": {
    "path_prefix": "/"
  },
  "auth_policy": {
    "require_virtual_key": true
  },
  "target_policy": {
    "type": "direct-provider",
    "provider_id": "openai-main",
    "credential_selector": "round_robin",
    "credential_scope_order": ["provider_id"],
    "credential_source_order": ["api_key", "cliauth_token"]
  }
}
```

### Logical Model JSON

```json
{
  "id": "openai-chat",
  "llm_api": "openai",
  "match": {
    "path_prefix": "/"
  },
  "auth_policy": {
    "require_virtual_key": true
  },
  "target_policy": {
    "type": "logical-model",
    "default_model": "chat-default",
    "model_selector_strategy": "auto",
    "credential_selector": "round_robin",
    "credential_scope_order": ["model_custom", "provider_id"],
    "credential_source_order": ["api_key", "cliauth_token"],
    "fallback": {
      "enabled": true,
      "max_num": 2
    },
    "models": [
      {
        "name": "chat-default",
        "candidates": [
          {
            "provider_id": "openai-main",
            "upstream_model": "gpt-4.1",
            "priority": 100,
            "weight": 100,
            "default": true
          },
          {
            "provider_id": "openai-backup",
            "upstream_model": "gpt-4.1",
            "priority": 100,
            "weight": 20
          }
        ]
      }
    ]
  }
}
```

## Validation Rules

### Common

1. `target_policy.type` is required.
2. `target_policy.type` must be `direct-provider` or `logical-model`.
3. `credential_selector` must be valid when present.
4. `credential_scope_order` must not contain duplicates.
5. `credential_source_order` must not contain duplicates.
6. `credential_source_order` should not be empty.

### DirectProviderPolicy

1. `provider_id` is required.
2. `credential_scope_order` should default to `[provider_id]`.
3. `credential_scope_order` must not contain `model_custom` unless future direct-provider model scoping is explicitly supported.

### LogicalModelPolicy

1. `models` must not be empty.
2. every logical model group must have a unique `name`.
3. every logical model group must contain at least one candidate.
4. every candidate must define `provider_id` and `upstream_model`.
5. `model_selector_strategy` must be one of `priority`, `weighted`, or `auto`.
6. `fallback.max_num` must be `>= 0`.
7. if `default_model` is set, it must exist in `models`.
8. `credential_scope_order` should default to `[model_custom, provider_id]`.
9. if `weighted` or `auto` is used, at least one eligible candidate in each logical model should have `weight > 0`.

## Runtime Resolution Model

`ResolvedTarget` should remain concrete and policy-free.

Suggested shape:

```go
type ResolvedTarget struct {
    LogicalModel  string
    ProviderID    string
    ProviderType  string
    UpstreamModel string
    Capabilities  provider.ModelCapabilities
}
```

Notes:

- `LogicalModel` is optional for direct-provider routes
- route policy should not be copied into `ResolvedTarget`
- `ResolvedTarget` describes one selected execution binding, not the full decision tree

## Migration Guidance

This change is not just a field rename. It changes route semantics and runtime ownership.

Recommended migration order:

1. add tagged policy decoding and validation in `gateway/route/types.go`
2. add Caddyfile parsing for `target_policy direct-provider` and `target_policy logical-model`
3. keep old stored route format unsupported in the new path unless explicit migration logic is added
4. introduce `gateway.RoutedProvider` alongside existing `authManagedProvider`
5. switch route execution to `RoutedProvider`
6. remove or narrow `authManagedProvider` after runtime migration is complete

## Implementation Scope

This design should drive changes in:

- `docs/route-target-policy-design.md`
- `gateway/route/types.go`
- `gateway/route/validate.go`
- `gateway/route/resolve.go`
- `gateway/caddyfile.go`
- `gateway/providerresolver.go`
- new gateway runtime files for `RoutedProvider`
- possible cleanup in `pkg/llm/provider/staticcredential.go`

This design does not require immediate changes in:

- `llm/credentialmgr` storage schema
- provider protocol adapters
- admin API transport shape beyond route JSON serialization changes

## Confirmed Decisions

The following points are fixed for implementation:

1. `model_custom` scope is resolved from the managed model metadata's `CredentialScope` field.
2. Provider config `api_key` participates in normal scheduling and source ordering; it is not an implicit final fallback.
3. Failure classification should use normalized typed reasons when available, with HTTP status as a fallback.
4. Higher `priority` value means higher priority.
5. Candidate tie-breaking should preserve config order.
