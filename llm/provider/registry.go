package provider

import (
	"fmt"
	"sync"
)

// ProviderFactory creates a Provider instance from config.
type ProviderFactory func(config ProviderConfig) (Provider, error)

var (
	mu        sync.RWMutex
	factories = map[string]ProviderFactory{}
)

// RegisterProviderFactory registers a provider factory by name.
func RegisterProviderFactory(name string, factory ProviderFactory) {
	mu.Lock()
	defer mu.Unlock()
	factories[name] = factory
}

// NewProvider creates a provider by name using registered factories.
func NewProvider(config ProviderConfig) (Provider, error) {
	mu.RLock()
	factory, ok := factories[config.ProviderName]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", config.ProviderName)
	}
	return factory(config)
}

// ListProviderNames returns the names of all registered providers.
func ListProviderNames() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	return names
}
