package intf

import (
	"context"
)

// CredentialStorer abstracts persistence of Credential state across restarts.
type CredentialStorer interface {
	ListByProviderType(ctx context.Context, providerType string) ([]any, error)

	Create(ctx context.Context, id string, providerType string, obj any) (string, error)

	Update(ctx context.Context, id string, obj any) error

	Delete(ctx context.Context, id string) error

	// return (providerType, obj, error)
	Get(ctx context.Context, id string) (string, any, error)
}
