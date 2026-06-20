package configstore

import (
	"errors"
	"fmt"
	"sync"

	"gorm.io/gorm"
)

var (
	ErrStoreAlreadyRegistered = errors.New("config store schema already registered")
	ErrUnknownStoreName       = errors.New("unknown config store")
)

// Backend contains the generic schema registration and store lookup logic
// shared by persisted config store backends.
type Backend struct {
	creator ConfigStoreCreator

	mu     sync.RWMutex
	stores map[string]ConfigStore
}

func NewBackend(creator ConfigStoreCreator) *Backend {
	return &Backend{
		creator: creator,
		stores:  make(map[string]ConfigStore),
	}
}

func (b *Backend) Register(name string, storeSchema StoreSchema) error {
	if b == nil {
		return fmt.Errorf("config store backend is nil")
	}
	if b.creator == nil {
		return fmt.Errorf("config store backend constructor is nil")
	}
	if name == "" {
		return fmt.Errorf("store name is required")
	}
	if err := validateSchema(name, storeSchema); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.stores[name]; exists {
		return fmt.Errorf("%w: %s", ErrStoreAlreadyRegistered, name)
	}
	store, err := b.creator.NewStore(storeSchema)
	if err != nil {
		return err
	}
	b.stores[name] = store
	return nil
}

// UsageDB forwards the usage.SQLDBProvider capability of the underlying creator
// so the generic Backend wrapper stays transparent. Without this, callers that
// type-assert the Backend (or an adapter wrapping it) to a usage SQL DB provider
// would silently fall back to a no-op metrics observer. Returns nil when the
// creator does not expose a usage database.
func (b *Backend) UsageDB() *gorm.DB {
	if b == nil {
		return nil
	}
	provider, ok := b.creator.(interface{ UsageDB() *gorm.DB })
	if !ok {
		return nil
	}
	return provider.UsageDB()
}

func (b *Backend) Get(name string) (ConfigStore, error) {
	if b == nil {
		return nil, fmt.Errorf("config store backend is nil")
	}

	b.mu.RLock()
	store, ok := b.stores[name]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownStoreName, name)
	}
	return store, nil
}

func validateSchema(name string, storeSchema StoreSchema) error {
	if storeSchema.Name == "" {
		return fmt.Errorf("schema name is required for store %q", name)
	}
	if storeSchema.Name != name {
		return fmt.Errorf("schema name %q does not match store name %q", storeSchema.Name, name)
	}
	if storeSchema.Table == "" {
		return fmt.Errorf("schema table is required for store %q", name)
	}
	if len(storeSchema.PrimaryKeyColumns) == 0 {
		return fmt.Errorf("schema primary key columns are required for store %q", name)
	}
	if storeSchema.DataColumn == "" {
		return fmt.Errorf("schema data column is required for store %q", name)
	}
	if storeSchema.Codec == nil {
		return fmt.Errorf("schema codec is required for store %q", name)
	}
	if storeSchema.Metadata == nil {
		return fmt.Errorf("schema metadata is required for store %q", name)
	}
	indexNames := make(map[string]struct{}, len(storeSchema.IndexColumns))
	for _, indexSchema := range storeSchema.IndexColumns {
		if indexSchema.Name == "" {
			return fmt.Errorf("schema index name is required for store %q", name)
		}
		if indexSchema.Column == "" {
			return fmt.Errorf("schema index column is required for store %q index %q", name, indexSchema.Name)
		}
		if _, exists := indexNames[indexSchema.Name]; exists {
			return fmt.Errorf("schema index %q is duplicated for store %q", indexSchema.Name, name)
		}
		indexNames[indexSchema.Name] = struct{}{}
	}
	return nil
}

var _ ConfigStoreBackend = (*Backend)(nil)
