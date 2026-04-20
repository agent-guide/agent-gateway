package provider

import (
	"errors"
	"testing"
)

func TestProviderRegistryEnableDisableProviderType(t *testing.T) {
	const providerType = "test-registry-provider-name"
	RegisterProviderFactory(providerType, func(cfg ProviderConfig) (Provider, error) {
		return &testConfigurableProvider{cfg: cfg}, nil
	})
	defer func() {
		if err := EnableProviderType(providerType); err != nil {
			t.Fatalf("restore provider type: %v", err)
		}
	}()

	enabled, ok := IsProviderTypeEnabled(providerType)
	if !ok {
		t.Fatalf("provider type %q not registered", providerType)
	}
	if !enabled {
		t.Fatalf("provider type %q enabled = false, want true", providerType)
	}

	if err := DisableProviderType(providerType); err != nil {
		t.Fatalf("disable provider type: %v", err)
	}
	enabled, ok = IsProviderTypeEnabled(providerType)
	if !ok || enabled {
		t.Fatalf("provider type state after disable: enabled=%v registered=%v", enabled, ok)
	}
	if _, err := NewProvider(ProviderConfig{ProviderType: providerType}); !errors.Is(err, ErrProviderTypeDisabled) {
		t.Fatalf("NewProvider error = %v, want ErrProviderTypeDisabled", err)
	}

	if err := EnableProviderType(providerType); err != nil {
		t.Fatalf("enable provider type: %v", err)
	}
	if _, err := NewProvider(ProviderConfig{ProviderType: providerType}); err != nil {
		t.Fatalf("NewProvider after enable: %v", err)
	}
}
