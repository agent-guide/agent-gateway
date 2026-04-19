package provider

import (
	"errors"
	"testing"
)

func TestProviderRegistryEnableDisableProviderName(t *testing.T) {
	const providerName = "test-registry-provider-name"
	RegisterProviderFactory(providerName, func(cfg ProviderConfig) (Provider, error) {
		return &testConfigurableProvider{cfg: cfg}, nil
	})
	defer func() {
		if err := EnableProviderName(providerName); err != nil {
			t.Fatalf("restore provider name: %v", err)
		}
	}()

	enabled, ok := IsProviderNameEnabled(providerName)
	if !ok {
		t.Fatalf("provider name %q not registered", providerName)
	}
	if !enabled {
		t.Fatalf("provider name %q enabled = false, want true", providerName)
	}

	if err := DisableProviderName(providerName); err != nil {
		t.Fatalf("disable provider name: %v", err)
	}
	enabled, ok = IsProviderNameEnabled(providerName)
	if !ok || enabled {
		t.Fatalf("provider name state after disable: enabled=%v registered=%v", enabled, ok)
	}
	if _, err := NewProvider(ProviderConfig{ProviderName: providerName}); !errors.Is(err, ErrProviderNameDisabled) {
		t.Fatalf("NewProvider error = %v, want ErrProviderNameDisabled", err)
	}

	if err := EnableProviderName(providerName); err != nil {
		t.Fatalf("enable provider name: %v", err)
	}
	if _, err := NewProvider(ProviderConfig{ProviderName: providerName}); err != nil {
		t.Fatalf("NewProvider after enable: %v", err)
	}
}
