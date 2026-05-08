package intf

import "context"

type VirtualKeyStorer interface {
	ListByTag(ctx context.Context, tag string) ([]any, error)

	Create(ctx context.Context, key string, tag string, obj any) error

	Update(ctx context.Context, key string, obj any) error

	Delete(ctx context.Context, key string) error

	Get(ctx context.Context, key string) (any, error)
}
