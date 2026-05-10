package openaibase_test

import (
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/deepseek"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/openrouter"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider/zhipu"
)

func TestResponsesProviderSupportMatchesUpstream(t *testing.T) {
	cases := []struct {
		name string
		got  provider.Provider
		want bool
	}{
		{
			name: "openai",
			got:  mustNewProvider(t, openai.New, "openai"),
			want: true,
		},
		{
			name: "openrouter",
			got:  mustNewProvider(t, openrouter.New, "openrouter"),
			want: true,
		},
		{
			name: "deepseek",
			got:  mustNewProvider(t, deepseek.New, "deepseek"),
			want: false,
		},
		{
			name: "zhipu",
			got:  mustNewProvider(t, zhipu.New, "zhipu"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := tc.got.(provider.ResponsesProvider)
			if ok != tc.want {
				t.Fatalf("ResponsesProvider support = %v, want %v", ok, tc.want)
			}
		})
	}
}

func mustNewProvider(t *testing.T, newFn func(provider.ProviderConfig) (provider.Provider, error), providerType string) provider.Provider {
	t.Helper()

	prov, err := newFn(provider.ProviderConfig{ProviderType: providerType})
	if err != nil {
		t.Fatalf("new provider %s: %v", providerType, err)
	}
	return prov
}
