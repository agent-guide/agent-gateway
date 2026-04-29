# Model Catalog Design

## Goal

Add a model catalog layer so route creation does not depend on a raw provider model list.

The catalog must combine:

- provider-reported upstream models
- user configuration and visibility rules
- route-facing logical model names

The design should reuse existing concepts already present in the codebase:

- `provider.Provider.ListModels(...)`
- `route.RoutePolicy.AllowedModels`
- `route.RouteTarget.ModelMap`

## Current Gap

Today the repository has the pieces below, but they are not composed into a full model selection flow.

- Providers can report models through `ListModels(...)`.
- Routes can restrict models through `allowed_models`.
- Targets already have `model_map`, which is the right place to map a route-facing model to a provider-specific model.

What is still missing:

- a model catalog service that aggregates provider models plus user overlays
- admin APIs to inspect and manage that catalog
- route-time validation against the catalog
- runtime rewriting from route model name to upstream provider model

## Design Principles

1. Keep provider facts separate from user intent.
2. Keep route-facing model names separate from upstream provider model IDs.
3. Do not store a flat global model list directly on routes.
4. Make route creation consume a computed catalog view, not raw provider responses.

## Three Layers

### 1. Provider Model Snapshot

This is the fact layer. It is derived from a specific provider instance by calling `ListModels(...)`.

Suggested shape:

```go
package modelcatalog

import (
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

type ProviderModelSnapshot struct {
	ProviderID    string                       `json:"provider_id"`
	ProviderType  string                       `json:"provider_type"`
	UpstreamModel string                       `json:"upstream_model"`
	DisplayName   string                       `json:"display_name,omitempty"`
	Description   string                       `json:"description,omitempty"`
	Capabilities  provider.ProviderCapabilities `json:"capabilities,omitempty"`
	Status        SnapshotStatus               `json:"status"`
	FetchedAt     time.Time                    `json:"fetched_at"`
	LastError     string                       `json:"last_error,omitempty"`
}

type SnapshotStatus string

const (
	SnapshotStatusOK    SnapshotStatus = "ok"
	SnapshotStatusStale SnapshotStatus = "stale"
	SnapshotStatusError SnapshotStatus = "error"
)
```

Notes:

- This object should be scoped by `provider_id`, not just `provider_type`.
- The same provider type may expose different models depending on base URL, account, region, or auth source.
- A snapshot may be stale; stale data is still useful for admin UX.

### 2. Model Overlay

This is the user-controlled layer. It does not replace snapshot data; it modifies how the snapshot is exposed to route authors.

Suggested shape:

```go
package modelcatalog

import "github.com/agent-guide/caddy-agent-gateway/llm/provider"

type ModelOverlay struct {
	ProviderID            string                        `json:"provider_id"`
	UpstreamModel         string                        `json:"upstream_model"`
	Enabled               *bool                         `json:"enabled,omitempty"`
	VisibleInRoutePicker  *bool                         `json:"visible_in_route_picker,omitempty"`
	RouteModelName        string                        `json:"route_model_name,omitempty"`
	Tags                  []string                      `json:"tags,omitempty"`
	CapabilityOverrides   *provider.ProviderCapabilities `json:"capability_overrides,omitempty"`
	Preferred             *bool                         `json:"preferred,omitempty"`
	Note                  string                        `json:"note,omitempty"`
}
```

Typical use cases:

- hide internal or legacy upstream models
- expose a friendly logical name like `chat-fast`
- normalize different provider model IDs under one route-facing name
- manually correct capability flags when upstream metadata is weak

### 3. Effective Route Model

This is the computed view used by the Admin API and route creation UI.

Suggested shape:

```go
package modelcatalog

import "github.com/agent-guide/caddy-agent-gateway/llm/provider"

type EffectiveRouteModel struct {
	RouteModelName string                      `json:"route_model_name"`
	DisplayName    string                      `json:"display_name,omitempty"`
	Capabilities   provider.ProviderCapabilities `json:"capabilities,omitempty"`
	Coverage       CoverageLevel               `json:"coverage"`
	Selectable     bool                        `json:"selectable"`
	Bindings       []EffectiveModelBinding     `json:"bindings"`
	Tags           []string                    `json:"tags,omitempty"`
}

type EffectiveModelBinding struct {
	ProviderID     string                        `json:"provider_id"`
	ProviderType   string                        `json:"provider_type"`
	UpstreamModel  string                        `json:"upstream_model"`
	Capabilities   provider.ProviderCapabilities `json:"capabilities,omitempty"`
	Preferred      bool                          `json:"preferred,omitempty"`
	Visible        bool                          `json:"visible"`
	SnapshotStatus SnapshotStatus                `json:"snapshot_status"`
}

type CoverageLevel string

const (
	CoverageAll     CoverageLevel = "all"
	CoveragePartial CoverageLevel = "partial"
	CoverageNone    CoverageLevel = "none"
)
```

This is not the persistence source of truth. It is a computed response.

## Route Integration

### Route should keep logical model names

`RoutePolicy.AllowedModels` should continue to store route-facing model names.

Example:

```json
{
  "policy": {
    "allowed_models": ["chat-fast", "chat-strong"]
  }
}
```

### Target should map logical names to upstream names

`RouteTarget.ModelMap` should be the execution bridge:

```json
{
  "provider_id": "openai-main",
  "model_map": {
    "chat-fast": "gpt-4o-mini",
    "chat-strong": "gpt-4.1"
  }
}
```

For another target:

```json
{
  "provider_id": "zhipu-main",
  "model_map": {
    "chat-fast": "glm-4.5-air",
    "chat-strong": "glm-4.5"
  }
}
```

This keeps the route definition stable even when providers use different model IDs.

## Aggregation Rules

The catalog service should compute effective route models using these rules.

1. Read selected provider configs from `ProviderManager`.
2. Resolve each provider instance and fetch its snapshot.
3. Load overlay records for `(provider_id, upstream_model)`.
4. Discard models that are explicitly disabled.
5. Derive `route_model_name` using:
   - `overlay.route_model_name`, if present
   - otherwise `upstream_model`
6. Group models by `route_model_name`.
7. Merge capabilities conservatively.
8. Compute coverage against the input provider set.

Capability merge guidance:

- `Streaming`, `Tools`, `Vision`, `Embeddings`: use logical AND for route-level defaults, because the route picker should not overstate support.
- `ContextWindow`, `MaxOutputTokens`: use the minimum of participating bindings when presenting route-wide guaranteed support.

Coverage guidance:

- `all`: every selected provider contributes a visible binding for the route model
- `partial`: only some selected providers contribute a binding
- `none`: no active binding remains after filtering

## Service Boundary

Add a dedicated service rather than embedding the logic in `admin/`.

Suggested package:

```text
gateway/modelcatalog/
```

Suggested interface:

```go
package modelcatalog

import "context"

type Service interface {
	RefreshProvider(ctx context.Context, providerID string) ([]ProviderModelSnapshot, error)
	ListProviderModels(ctx context.Context, providerID string) ([]ProviderModelView, error)
	UpsertOverlay(ctx context.Context, overlay ModelOverlay) error
	GetOverlay(ctx context.Context, providerID, upstreamModel string) (ModelOverlay, error)
	ListEffectiveRouteModels(ctx context.Context, req EffectiveRouteModelRequest) ([]EffectiveRouteModel, error)
}

type EffectiveRouteModelRequest struct {
	ProviderIDs []string `json:"provider_ids,omitempty"`
	LLMAPI      string   `json:"llm_api,omitempty"`
	OnlyVisible bool     `json:"only_visible,omitempty"`
}
```

`LLMAPI` is useful because some model sets may be valid for one protocol surface but not another.

## Persistence

Store snapshots and overlays separately.

Suggested config store extensions:

```go
package intf

import "context"

type ModelCatalogStorer interface {
	ListProviderSnapshots(ctx context.Context, providerID string) ([]any, error)
	ReplaceProviderSnapshots(ctx context.Context, providerID string, items []any) error

	GetOverlay(ctx context.Context, providerID, upstreamModel string) (any, error)
	ListOverlaysByProvider(ctx context.Context, providerID string) ([]any, error)
	UpsertOverlay(ctx context.Context, providerID, upstreamModel string, item any) error
	DeleteOverlay(ctx context.Context, providerID, upstreamModel string) error
}
```

Implementation notes:

- overlays are durable configuration
- snapshots may be durable but refreshable
- snapshot replacement should be atomic per provider

## Admin API Draft

### Provider-scoped model inspection

List raw models plus overlay state:

```text
GET /admin/providers/{id}/models
```

Suggested query params:

- `refresh=true` to trigger a synchronous refresh before returning

Suggested response:

```json
{
  "provider_id": "openai-main",
  "items": [
    {
      "upstream_model": "gpt-4o-mini",
      "display_name": "GPT-4o mini",
      "status": "ok",
      "fetched_at": "2026-04-28T09:00:00Z",
      "capabilities": {
        "streaming": true,
        "tools": true,
        "vision": true
      },
      "overlay": {
        "route_model_name": "chat-fast",
        "visible_in_route_picker": true,
        "preferred": true
      }
    }
  ]
}
```

### Provider-scoped overlay update

Upsert per-model overlay:

```text
PUT /admin/providers/{id}/models/{upstream_model}
```

Request body:

```json
{
  "enabled": true,
  "visible_in_route_picker": true,
  "route_model_name": "chat-fast",
  "preferred": true,
  "tags": ["default", "fast"]
}
```

Delete overlay:

```text
DELETE /admin/providers/{id}/models/{upstream_model}/overlay
```

### Aggregated route picker catalog

Return route-facing selectable models computed across multiple providers:

```text
GET /admin/model_catalog?provider_ids=openai-main,zhipu-main&llm_api=openai
```

Suggested response:

```json
{
  "items": [
    {
      "route_model_name": "chat-fast",
      "coverage": "all",
      "selectable": true,
      "bindings": [
        {
          "provider_id": "openai-main",
          "provider_type": "openai",
          "upstream_model": "gpt-4o-mini",
          "visible": true,
          "snapshot_status": "ok"
        },
        {
          "provider_id": "zhipu-main",
          "provider_type": "zhipu",
          "upstream_model": "glm-4.5-air",
          "visible": true,
          "snapshot_status": "ok"
        }
      ]
    }
  ]
}
```

## Route Validation Changes

When creating or updating a route:

1. collect referenced `provider_id`s from targets
2. query the effective route model catalog for those providers
3. validate every `policy.allowed_models` entry against catalog output
4. validate that each target can resolve each allowed route model through:
   - `target.model_map[routeModel]`, or
   - implicit same-name fallback if the route model equals an upstream model

This validation belongs in the route management layer, not only in the frontend.

## Runtime Execution Changes

The current execution flow validates the route and then resolves a provider, but it does not yet apply `target.model_map` during execution.

To support logical route models, runtime should produce an execution plan:

```go
package gateway

import (
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

type ResolvedExecution struct {
	Provider      provider.Provider
	ProviderID    string
	Target        routepkg.RouteTarget
	RouteModel    string
	UpstreamModel string
}
```

Execution flow should become:

1. parse request model from wire format
2. validate route policy against the logical route model
3. select target
4. map `route model -> upstream model` using `target.model_map`
5. mutate `ChatRequest.Model` to the upstream model
6. execute the provider request

Without this step, `model_map` remains config-only and does not affect requests.

## Recommended Implementation Order

1. Add `gateway/modelcatalog` package and service interfaces.
2. Add config-store support for model snapshots and overlays.
3. Add admin APIs for provider model inspection and overlay updates.
4. Add aggregated `/admin/model_catalog`.
5. Add route create/update validation against the catalog.
6. Add runtime model rewriting through `RouteTarget.ModelMap`.

## Non-Goals For First Iteration

- global pricing catalog
- tokenizer-accurate compatibility guarantees
- protocol-specific model aliases beyond route logical naming
- automatic route migration when upstream models disappear

Those can be added later without changing the core layering above.
