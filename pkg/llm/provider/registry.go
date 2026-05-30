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

// ErrProviderTypeDisabled is returned when a registered provider type is disabled.
var ErrProviderTypeDisabled = errors.New("provider type is disabled")

type ProviderTypeSetting struct {
	ProviderType string `json:"provider_type"`
	Enabled      bool   `json:"enabled"`
}

var (
	mu                    sync.RWMutex
	factories             = map[string]ProviderFactory{}
	disabledProviderTypes = map[string]struct{}{}
)

// RegisterProviderFactory registers a provider factory by name.
func RegisterProviderFactory(name string, factory ProviderFactory) {
	name = normalizeProviderType(name)
	if name == "" || factory == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	factories[name] = factory
}

// NewProvider creates a provider by name using registered factories.
func NewProvider(config ProviderConfig) (Provider, error) {
	name := normalizeProviderType(config.ProviderType)
	mu.RLock()
	factory, ok := factories[name]
	_, disabled := disabledProviderTypes[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", config.ProviderType)
	}
	if disabled {
		return nil, fmt.Errorf("%w: %s", ErrProviderTypeDisabled, config.ProviderType)
	}
	return factory(config)
}

// ListProviderTypes returns the names of all registered providers.
func ListProviderTypes() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsProviderTypeEnabled reports whether a registered provider type is enabled.
func IsProviderTypeEnabled(name string) (bool, bool) {
	name = normalizeProviderType(name)
	mu.RLock()
	defer mu.RUnlock()
	if _, ok := factories[name]; !ok {
		return false, false
	}
	_, disabled := disabledProviderTypes[name]
	return !disabled, true
}

// EnableAllProviderTypes clears the disabled set so every registered provider
// type is enabled. It is used when no startup provider_types policy is set.
func EnableAllProviderTypes() {
	mu.Lock()
	defer mu.Unlock()
	disabledProviderTypes = map[string]struct{}{}
}

// ConfigureProviderTypes applies startup-only provider type availability.
// If exclusive is true, every registered provider type not listed is disabled.
func ConfigureProviderTypes(settings []ProviderTypeSetting, exclusive bool) error {
	mu.Lock()
	defer mu.Unlock()

	nextDisabled := map[string]struct{}{}
	if !exclusive {
		for name := range disabledProviderTypes {
			nextDisabled[name] = struct{}{}
		}
	} else {
		for name := range factories {
			nextDisabled[name] = struct{}{}
		}
	}
	for _, setting := range settings {
		name := normalizeProviderType(setting.ProviderType)
		if name == "" {
			return fmt.Errorf("provider_type is required")
		}
		if _, ok := factories[name]; !ok {
			return fmt.Errorf("unknown provider: %s", name)
		}
		if setting.Enabled {
			delete(nextDisabled, name)
		} else {
			nextDisabled[name] = struct{}{}
		}
	}
	disabledProviderTypes = nextDisabled
	return nil
}

func normalizeProviderType(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
