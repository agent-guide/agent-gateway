package provider

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ProviderFactory creates a Provider instance from config.
type ProviderFactory func(config ProviderConfig) (Provider, error)

// ErrProviderNameDisabled is returned when a registered provider type is disabled.
var ErrProviderNameDisabled = errors.New("provider name is disabled")

var (
	mu                    sync.RWMutex
	factories             = map[string]ProviderFactory{}
	disabledProviderNames = map[string]struct{}{}
)

// RegisterProviderFactory registers a provider factory by name.
func RegisterProviderFactory(name string, factory ProviderFactory) {
	name = normalizeProviderName(name)
	if name == "" || factory == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	factories[name] = factory
}

// NewProvider creates a provider by name using registered factories.
func NewProvider(config ProviderConfig) (Provider, error) {
	name := normalizeProviderName(config.ProviderName)
	mu.RLock()
	factory, ok := factories[name]
	_, disabled := disabledProviderNames[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", config.ProviderName)
	}
	if disabled {
		return nil, fmt.Errorf("%w: %s", ErrProviderNameDisabled, config.ProviderName)
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
	sort.Strings(names)
	return names
}

// IsProviderNameEnabled reports whether a registered provider type is enabled.
func IsProviderNameEnabled(name string) (bool, bool) {
	name = normalizeProviderName(name)
	mu.RLock()
	defer mu.RUnlock()
	if _, ok := factories[name]; !ok {
		return false, false
	}
	_, disabled := disabledProviderNames[name]
	return !disabled, true
}

// EnableProviderName enables a registered provider type.
func EnableProviderName(name string) error {
	name = normalizeProviderName(name)
	mu.Lock()
	defer mu.Unlock()
	if _, ok := factories[name]; !ok {
		return fmt.Errorf("unknown provider: %s", name)
	}
	delete(disabledProviderNames, name)
	return nil
}

// DisableProviderName disables a registered provider type.
func DisableProviderName(name string) error {
	name = normalizeProviderName(name)
	mu.Lock()
	defer mu.Unlock()
	if _, ok := factories[name]; !ok {
		return fmt.Errorf("unknown provider: %s", name)
	}
	disabledProviderNames[name] = struct{}{}
	return nil
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
