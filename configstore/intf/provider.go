package intf

import (
	"context"
)

// ProviderConfigStorer abstracts persistence of the config of providers.
type ProviderConfigStorer interface {
	// ListByType lists provider configs by provider type name.
	ListByType(ctx context.Context, name string) ([]any, error)

	// Create creates a new provider config, storing the provider type in name.
	Create(ctx context.Context, id string, name string, obj any) (string, error)

	// Update updates an existing provider config.
	Update(ctx context.Context, id string, obj any) error

	Delete(ctx context.Context, id string) error

	// Get returns (provider type name, provider config, error).
	Get(ctx context.Context, id string) (string, any, error)
}
