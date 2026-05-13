package configstore

import "context"

// ConfigStore is a schema-bound generic store for one persisted object family.
type ConfigStore interface {
	List(ctx context.Context) ([]any, error)
	ListByTag(ctx context.Context, tag string) ([]any, error)
	ListByTagPrefix(ctx context.Context, tagPrefix string) ([]any, error)

	Create(ctx context.Context, obj any) error
	Update(ctx context.Context, obj any) error
	Delete(ctx context.Context, keyParts ...any) error

	Get(ctx context.Context, keyParts ...any) (any, error)
	GetByIndex(ctx context.Context, indexName string, value any) (any, error)
}

// ConfigStoreBackend registers store schemas and returns schema-bound stores.
type ConfigStoreBackend interface {
	Register(name string, storeSchema StoreSchema) error
	Get(name string) (ConfigStore, error)
}
