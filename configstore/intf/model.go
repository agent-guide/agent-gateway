package intf

import "context"

// ModelStorer persists admin-managed model overlays keyed by provider_id + upstream_model.
type ModelStorer interface {
	List(ctx context.Context) ([]any, error)
	Get(ctx context.Context, providerID string, upstreamModel string) (any, bool, error)
	Upsert(ctx context.Context, obj any) error
	Delete(ctx context.Context, providerID string, upstreamModel string) error
}
