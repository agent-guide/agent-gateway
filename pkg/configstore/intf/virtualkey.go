package intf

import "context"

type VirtualKeyStorer interface {
	ListByTag(ctx context.Context, tag string) ([]any, error)

	Create(ctx context.Context, id string, tag string, obj any) error

	Update(ctx context.Context, id string, obj any) error

	Delete(ctx context.Context, id string) error

	Get(ctx context.Context, id string) (any, error)

	GetByKey(ctx context.Context, key string) (any, error)
}
