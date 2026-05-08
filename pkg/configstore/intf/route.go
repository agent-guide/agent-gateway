package intf

import "context"

// RouteStorer persists gateway route definitions.
type RouteStorer interface {
	ListByTag(ctx context.Context, tag string) ([]any, error)
	ListByTagPrefix(ctx context.Context, tagPrefix string) ([]any, error)
	Create(ctx context.Context, id string, tag string, obj any) error
	Update(ctx context.Context, id string, obj any) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (any, error)
}
