package configstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

type ConfigStoreCreator interface {
	NewStore(storeSchema StoreSchema) (ConfigStore, error)
}

type ConfigStoreCreatorFactory func(context.Context, json.RawMessage, *zap.Logger) (ConfigStoreCreator, error)

var configstoreCreatorFactories = struct {
	sync.RWMutex
	byName map[string]ConfigStoreCreatorFactory
}{
	byName: make(map[string]ConfigStoreCreatorFactory),
}

func RegisterConfigStoreCreatorFactory(name string, factory ConfigStoreCreatorFactory) {
	if name == "" {
		panic("config store backend factory name is required")
	}
	if factory == nil {
		panic("config store backend factory is nil")
	}

	configstoreCreatorFactories.Lock()
	defer configstoreCreatorFactories.Unlock()
	if _, exists := configstoreCreatorFactories.byName[name]; exists {
		panic("config store backend factory already registered: " + name)
	}
	configstoreCreatorFactories.byName[name] = factory
}

func OpenBackend(ctx context.Context, name string, cfg any, logger *zap.Logger) (ConfigStoreBackend, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config store backend %q config: %w", name, err)
	}
	return OpenBackendJSON(ctx, name, raw, logger)
}

func OpenBackendJSON(ctx context.Context, name string, raw json.RawMessage, logger *zap.Logger) (ConfigStoreBackend, error) {
	configstoreCreatorFactories.RLock()
	creatorFactory, ok := configstoreCreatorFactories.byName[name]
	configstoreCreatorFactories.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown config store backend type %q", name)
	}

	factory, err := creatorFactory(ctx, raw, logger)
	if err != nil {
		return nil, err
	}
	return NewBackend(factory), nil
}
