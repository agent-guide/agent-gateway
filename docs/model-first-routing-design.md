# Model Catalog Design

## Goal

Reframe routing so the primary route target is a logical model, not a provider.

The new design must satisfy these requirements:

1. `Route` is configured around logical models.
2. Each logical model ID maps to one or more concrete `(provider_id, upstream_model)` bindings.
3. A route may either declare logical models plus an optional `default_target_model`, or directly forward to one concrete provider through `target provider`.
4. Providers report their real upstream model list and per-model capabilities.
5. A dedicated `ModelStorer` persists administrator-managed adjustments on top of provider-reported facts.

This document replaces the earlier design that still treated `provider_id` as the primary route target and only used model mapping as an auxiliary bridge.

## Why Model-First

There are two plausible routing abstractions:

- `Provider-first`: `Route -> ProviderTarget -> optional model rewrite`
- `Model-first`: `Route -> LogicalModel -> concrete (provider_id, upstream_model)`

The old codebase was closer to `Provider-first`, because routes used to center execution around provider targets. The current route model has moved to `direct_target` for explicit provider forwarding and `model_policy` for logical-model routing.

That shape is simpler in the short term, but it makes the wrong concept stable at the gateway boundary. Clients and route authors end up coupling to provider choices instead of model intent.

This design chooses `Model-first` for these reasons:

- callers care about a stable logical model contract, not which upstream provider currently serves it
- one logical model should be able to fan out to multiple providers without repeating route configuration
- model capability checks are only accurate at model granularity, not provider granularity
- provider replacement, failover, weighting, and future cost or latency routing should remain implementation details under one logical model ID

`Provider-first` remains useful as an internal execution layer, but it should no longer be the primary route-facing abstraction.

At the same time, the final design must preserve a simple static Caddyfile path for users who only want straightforward provider forwarding without building a model catalog first.

## Problem With The Current Shape

The current repository already has:

- provider-level `ListModels(...)`
- route-level `target_models`
- route-level `direct_target`
- route-level logical model selection

That shape is still provider-centric:

- direct-provider routes still expose provider choice at the route boundary
- model naming is fragmented across routes and providers
- the same logical model family can still be repeated across multiple routes
- capability filtering happens too late and with weak global visibility

The intended future shape is model-centric:

- the route declares "what logical model IDs are targetable"
- the catalog decides "which concrete provider+model candidates can satisfy it"
- runtime selection chooses one concrete binding from that candidate set

## Design Principles

1. Treat logical model IDs as first-class API surface.
2. Keep provider facts separate from administrator intent.
3. Keep route policy separate from concrete upstream binding details.
4. Make provider selection a consequence of model selection, not the other way around.
5. Persist admin overrides explicitly instead of mutating provider snapshots.

## Core Concepts

There are three distinct model layers.

### 1. Upstream Model

This is the real provider-reported model identity.

Examples:

- `openai-main + gpt-4.1`
- `openrouter-main + openai/gpt-4.1`
- `zhipu-main + glm-4.5`

This layer is factual and provider-scoped.

### 2. Managed Concrete Model

This is the administrator-managed view of one upstream model after applying local adjustments.

Examples of adjustments:

- enabled or disabled
- hidden from route selection
- capability corrections
- tags, cost tier, region, operational notes
- membership in one or more logical model IDs

This layer is persisted by `ModelStorer`.

### 3. Logical Model

This is the route-facing model identity used by clients and route policy.

Examples:

- `chat-default`
- `chat-fast`
- `chat-strong`
- `reasoning`

A logical model is resolved at runtime into one concrete candidate binding from its bound set.

## Proposed Package Boundary

Add a new package:

```text
gateway/modelcatalog/
```

This package owns:

- provider model discovery
- admin-managed model overlays
- logical model aggregation
- route-facing catalog queries
- runtime resolution from logical model to concrete binding

It should not live inside `admin/`, because both the admin API and the request path need the same service.

## Data Model

### Provider Snapshot Layer

This is the raw discovery result from `provider.Provider.ListModels(...)`.

```go
package modelcatalog

import (
	"time"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

type ProviderModelSnapshot struct {
	ProviderID    string                        `json:"provider_id"`
	ProviderType  string                        `json:"provider_type"`
	UpstreamModel string                        `json:"upstream_model"`
	DisplayName   string                        `json:"display_name,omitempty"`
	Description   string                        `json:"description,omitempty"`
	Capabilities  provider.ModelCapabilities    `json:"capabilities,omitempty"`
	Status        SnapshotStatus                `json:"status"`
	FetchedAt     time.Time                     `json:"fetched_at"`
	LastError     string                        `json:"last_error,omitempty"`
}

type SnapshotStatus string

const (
	SnapshotStatusOK    SnapshotStatus = "ok"
	SnapshotStatusStale SnapshotStatus = "stale"
	SnapshotStatusError SnapshotStatus = "error"
)
```

Notes:

- this record is scoped by `provider_id + upstream_model`
- it is not user-editable
- stale snapshots remain useful for admin visibility

### Managed Concrete Model Layer

This is the persisted admin overlay for a concrete upstream model.

```go
package modelcatalog

import "github.com/agent-guide/agent-gateway/pkg/llm/provider"

type ManagedModel struct {
	ProviderID            string                        `json:"provider_id"`
	UpstreamModel         string                        `json:"upstream_model"`
	Enabled               bool                          `json:"enabled"`
	LogicalModelIDs       []string                      `json:"logical_model_ids,omitempty"`
	CapabilityOverrides   *provider.ModelCapabilities    `json:"capability_overrides,omitempty"`
	Tags                  []string                      `json:"tags,omitempty"`
	Priority              int                           `json:"priority,omitempty"`
	Weight                int                           `json:"weight,omitempty"`
	OperationalState      string                        `json:"operational_state,omitempty"`
	Note                  string                        `json:"note,omitempty"`
}
```

Notes:

- one concrete upstream model may belong to multiple logical model IDs
- `Enabled=false` removes it from runtime candidate sets
- `Weight` and `Priority` are model-binding attributes, not route-global attributes

### Effective Binding Layer

This is the runtime-ready binding between a logical model and a concrete upstream model.

```go
package modelcatalog

import "github.com/agent-guide/agent-gateway/pkg/llm/provider"

type ModelBinding struct {
	LogicalModelID   string                        `json:"logical_model_id"`
	ProviderID       string                        `json:"provider_id"`
	ProviderType     string                        `json:"provider_type"`
	UpstreamModel    string                        `json:"upstream_model"`
	Capabilities     provider.ModelCapabilities    `json:"capabilities,omitempty"`
	Weight           int                           `json:"weight,omitempty"`
	Priority         int                           `json:"priority,omitempty"`
	Tags             []string                      `json:"tags,omitempty"`
}
```

### Effective Logical Model Layer

This is the route-facing catalog view used by Admin APIs and route validation.

```go
package modelcatalog

import "github.com/agent-guide/agent-gateway/pkg/llm/provider"

type LogicalModel struct {
	ID               string                        `json:"id"`
	DisplayName      string                        `json:"display_name,omitempty"`
	Description      string                        `json:"description,omitempty"`
	GuaranteedCaps   provider.ModelCapabilities    `json:"guaranteed_capabilities,omitempty"`
	Bindings         []ModelBinding                `json:"bindings"`
	DefaultCandidate string                        `json:"default_candidate,omitempty"`
}
```

`GuaranteedCaps` should be conservative:

- boolean capabilities use logical AND across enabled bindings
- numeric capability ceilings use the minimum value across enabled bindings

This avoids overstating route-level guarantees.

## Capability Model

Capability ownership should move to the model layer.

The current provider package exposes `ProviderCapabilities`, but that concept is too coarse for routing correctness. A single provider instance may expose multiple models with different support for tools, vision, streaming, context window, or output limits.

The effective rule is:

- route validation uses `ModelCapabilities`
- logical model aggregation uses `ModelCapabilities`
- runtime candidate filtering uses `ModelCapabilities`
- provider-level capability metadata is summary-only and must not be the final routing authority

Final type direction in `pkg/llm/provider/`:

```go
type ModelCapabilities struct {
	Streaming       bool
	Tools           bool
	Vision          bool
	Embeddings      bool
	ContextWindow   int
	MaxOutputTokens int
}

type ModelInfo struct {
	ID           string
	Name         string
	Description  string
	Capabilities ModelCapabilities
}

type ProviderFeatureSummary struct {
	SupportsChat       bool
	SupportsStreaming  bool
	SupportsEmbeddings bool
	SupportsModelList  bool
}
```

If a provider-level `Capabilities()` method is retained, it should be treated as a coarse summary or fallback inference source only. It should not be used as the final answer to "can this route use this model".

Implementation note:

- the repository currently uses `ProviderCapabilities` in `pkg/llm/provider/provider.go`
- the target direction is to introduce `ModelCapabilities` as the routing authority first
- `ProviderCapabilities` can then be deprecated, retained as a summary type, or renamed to a clearer provider-summary concept in a later refactor

## Route Model Changes

### Route Supports Two Valid Modes

The final route model supports two valid operating modes.

```go
type AgentRoute struct {
	ID          string
	Description string
	Disabled    bool
	LLMAPI      string
	Match       RouteMatch
	ModelPolicy  RouteModelPolicy
	DirectTarget *DirectProviderTarget `json:"direct_target,omitempty"`
	Policy      RoutePolicy
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type RouteModelPolicy struct {
	TargetModels       []string `json:"target_models,omitempty"`
	DefaultTargetModel string   `json:"default_target_model,omitempty"`
}

type DirectProviderTarget struct {
	ProviderID string `json:"provider_id"`
}
```

Mode A: logical-model mode

- `model_policy.target_models` is present
- `model_policy.default_target_model` is optional
- provider/model selection is resolved through the model catalog

Mode B: direct-provider mode

- `model_policy.target_models` is omitted
- `model_policy.default_target_model` is omitted
- `direct_target.provider_id` is required
- the route directly forwards to one provider instance

Direct-provider mode exists specifically for simple Caddyfile-based deployments. It is intentionally constrained:

- each route supports at most one direct provider target
- the directive form is `target provider <provider-id>`
- this mode bypasses logical model selection and catalog binding resolution

The final ownership of route-facing model configuration should be a dedicated `ModelPolicy` block rather than keeping model state buried inside generic route policy.

### Route Validation Rules

Every route must satisfy exactly one route mode.

Logical-model route:

- `llm_api` is required
- `direct_target` must be absent
- `target_models` should normally be non-empty
- if `default_target_model` is set, it must appear in `target_models`
- every `target_models` entry must reference an existing logical model ID

Direct-provider route:

- `llm_api` is required
- `direct_target.provider_id` is required
- `target_models` must be absent
- `default_target_model` must be absent
- the referenced provider must exist and be enabled

Invalid combinations:

- `direct_target` plus any `target_models`
- `direct_target` plus `default_target_model`
- more than one `target provider` directive in one route
- `default_target_model` not present in `target_models`

These rules apply equally to Admin API payloads and Caddyfile parsing.

### Route Resolution Input

`RequestRequirements.Model` should now mean logical model ID.

In logical-model mode, if the client omits a model:

1. use `route.model_policy.default_target_model`
2. if that is empty, the provider may fall back to its own default only when the route intentionally allows model omission
3. otherwise reject the request

In logical-model mode, if the client specifies a model:

1. treat it as a logical model ID
2. validate it against the route's `target_models`
3. resolve it through the catalog into a concrete binding

In direct-provider mode:

1. the route does not validate against logical models
2. the request is sent to `direct_target.provider_id`
3. any request `model` value is treated as the upstream model name and passed through directly
4. if the request omits `model`, the selected provider's own `default_model` may be used

## Provider Contract Changes

The provider layer already has `ListModels(...)`, but it needs to be treated as a first-class input to the catalog.

The final contract is:

```go
type ModelInfo struct {
	Name         string
	DisplayName  string
	Description  string
	Capabilities ModelCapabilities
}
```

Requirements:

- providers return real upstream model names
- capabilities are per-model, not only provider-global
- provider-level `Capabilities()` or similar summary metadata remains optional and non-authoritative
- model routing should prefer per-model capabilities from `ListModels(...)`

If a provider cannot return per-model capability details yet:

- return the model list with best-effort capability inference
- allow `ManagedModel.CapabilityOverrides` to correct it

## ModelStorer

The config store includes a dedicated model store interface:

```go
package intf

type ConfigStorer interface {
	GetCredentialStore(ctx context.Context, decodeConfigObject ConfigObjectDecoder) (CredentialStorer, error)
	GetProviderConfigStore(ctx context.Context, decodeProviderConfig ConfigObjectDecoder) (ProviderConfigStorer, error)
	GetVirtualKeyStore(ctx context.Context, decodeVirtualKey ConfigObjectDecoder) (VirtualKeyStorer, error)
	GetRouteStore(ctx context.Context, decodeRoute ConfigObjectDecoder) (RouteStorer, error)
	GetModelStore(ctx context.Context, decodeModel ConfigObjectDecoder) (ModelStorer, error)
}
```

The model store interface is:

```go
package intf

import "context"

type ModelStorer interface {
	List(ctx context.Context) ([]any, error)
	Get(ctx context.Context, providerID string, upstreamModel string) (any, bool, error)
	Upsert(ctx context.Context, obj any) error
	Delete(ctx context.Context, providerID string, upstreamModel string) error
}
```

Persistence key:

```text
(provider_id, upstream_model)
```

What `ModelStorer` persists:

- admin enable/disable state
- visibility
- logical model memberships
- capability overrides
- weights, priorities, tags, notes

What `ModelStorer` does not persist:

- raw provider snapshot freshness
- fetched timestamps from upstream
- transient health probe state

## Catalog Service

The catalog service interface is:

```go
package modelcatalog

import "context"

type Service interface {
	RefreshProvider(ctx context.Context, providerID string) error
	ListLogicalModels(ctx context.Context, filter CatalogFilter) ([]LogicalModel, error)
	GetLogicalModel(ctx context.Context, id string) (*LogicalModel, error)
	ListManagedModels(ctx context.Context, filter ManagedModelFilter) ([]ManagedConcreteModelView, error)
	ResolveTarget(ctx context.Context, req ResolveRequest) (*ResolvedTarget, error)
}

type ResolveRequest struct {
	RouteID           string
	LLMAPI            string
	LogicalModelID    string
	RequireStreaming  bool
	RequireTools      bool
	RequireVision     bool
	RequireEmbeddings bool
}
```

`ResolveTarget(...)` is the core runtime API for logical-model mode. It should:

1. load the route
2. validate that the logical model ID is allowed by the route
3. load all enabled bindings for that logical model
4. filter bindings by route constraints, request constraints, and per-model capabilities
5. apply selection strategy
6. return the concrete `(provider_id, upstream_model)` binding

## Runtime Resolution Flow

The request path has two variants.

Logical-model mode:

```text
HTTP request
  -> route dispatcher matches AgentRoute
  -> protocol handler parses request
  -> requested model is interpreted as logical model ID
  -> if empty, use route.default_target_model
  -> modelcatalog.ResolveTarget(...)
  -> returns provider_id + upstream_model
  -> gateway resolves provider instance
  -> protocol handler/provider request model is rewritten to upstream_model
  -> provider.Chat(...) or provider.StreamChat(...)
```

Direct-provider mode:

```text
HTTP request
  -> route dispatcher matches AgentRoute
  -> protocol handler parses request
  -> route.direct_target.provider_id selects provider
  -> request model is passed through as upstream model
  -> if request model is empty, provider default_model may apply
  -> provider.Chat(...) or provider.StreamChat(...)
```

Important consequences:

- in logical-model mode, provider selection happens after logical model resolution
- in direct-provider mode, provider selection is explicit and static

## Selection Algorithm

Selection is now performed within the candidate set of one logical model.

The selection algorithm is:

1. start from all enabled bindings of the logical model
2. drop bindings whose provider is disabled or unavailable
3. drop bindings that do not satisfy route/request capability requirements
4. apply `priority` as the first ordering key
5. within the same priority tier, apply weighted choice
6. if the chosen binding fails and route fallback is enabled, try the next candidate

This keeps routing semantics aligned with the existing weighted/failover ideas, but relocates them under logical-model binding resolution.

## Capability Semantics

Capabilities now exist in three scopes:

1. provider-level summary capabilities
2. concrete upstream model capabilities
3. route-level policy requirements

Resolution rules:

- route admission checks use logical model guaranteed capabilities
- final candidate filtering uses concrete binding capabilities
- provider-level summaries are fallback metadata only when per-model details are absent

Example:

- route allows `chat-fast`
- `chat-fast` binds to `openai-main/gpt-4.1-mini` and `zhipu-main/glm-4.5-air`
- route requires streaming
- if one binding loses streaming support, it is removed from the runtime candidate set
- the logical model may remain usable if at least one compatible binding remains

## Admin API Direction

The admin surface includes these API families:

- `GET /admin/models/providers/{provider_id}/discovered`
  - raw provider snapshot view
- `POST /admin/models/providers/{provider_id}/refresh`
  - trigger `ListModels(...)` refresh
- `GET /admin/models/managed`
  - list persisted managed concrete models
- `PUT /admin/models/managed/{provider_id}/{upstream_model}`
  - upsert admin overrides for one concrete model
- `GET /admin/models/logical`
  - computed logical model catalog
- `GET /admin/models/logical/{logical_model_id}`
  - inspect one logical model and its bindings

Route CRUD follows these rules:

- logical-model routes store `target_models` as logical model IDs
- logical-model routes may store `default_target_model`
- direct-provider routes store one `direct_target.provider_id`
- route payloads should reject configurations that mix logical-model mode with direct-provider mode in the same route

## Caddyfile Direction

The final Caddyfile design supports both a model-centric route and a simple direct-provider route.

Model-centric route:

```caddy
route chat {
    llm_api openai
    target model chat-fast default
    target model chat-strong
}
```

Provider/model binding should be configured outside the route, through admin-managed model catalog records or a dedicated model block in static config.

Static logical model binding may be expressed as:

```caddy
logical_model chat-fast {
    bind openai-main gpt-4.1-mini weight 100
    bind zhipu-main glm-4.5-air weight 50
}
```

Simple direct-provider route:

```caddy
route openai-pass-through {
    llm_api openai
    target provider openai-main
}
```

Rules for `target provider`:

- it is intended for simple provider forwarding
- it may appear at most once per route
- when `target provider` is set, `target model` should be omitted
- the route forwards directly to the configured provider and treats request `model` as an upstream model name

This keeps the system model-first at the main abstraction layer while still supporting the simplest possible static provider forwarding configuration.

## Summary

The core change is structural:

- model-centric route: `Route -> LogicalModel -> Concrete (provider_id, upstream_model) binding`
- simple direct route: `Route -> target provider -> Provider`

This gives the system the right long-term ownership boundaries:

- providers report real model facts
- `ModelStorer` persists admin intent
- model-centric routes expose stable logical model IDs
- direct-provider routes remain available for simple static forwarding
- runtime resolves a logical model into one concrete provider/model candidate at execution time when the route uses model-centric mode

That is the final design direction: model-first by default, with an explicit single-provider direct-routing escape hatch for simple Caddyfile deployments.
