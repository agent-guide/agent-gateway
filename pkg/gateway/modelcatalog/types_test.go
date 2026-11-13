package modelcatalog

import (
	"strings"
	"testing"
)

func TestStoredProviderModelRequiresExplicitEnabled(t *testing.T) {
	for _, field := range []string{"", `,"enabled":null`} {
		_, err := DecodeStoredProviderModel([]byte(`{"provider_id":"p","upstream_model":"m"` + field + `}`))
		if err == nil || !strings.Contains(err.Error(), "enabled must be an explicit boolean") {
			t.Fatalf("missing migration error: %v", err)
		}
	}
	for _, enabled := range []string{"true", "false"} {
		value, err := DecodeStoredProviderModel([]byte(`{"provider_id":"p","upstream_model":"m","enabled":` + enabled + `}`))
		if err != nil {
			t.Fatal(err)
		}
		if value.(*ProviderModel).Enabled != (enabled == "true") {
			t.Fatal("explicit enabled value changed")
		}
	}
}
