package gateway

import (
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
)

func TestRoutedProviderListsOnlyRouteExposedModelNames(t *testing.T) {
	for _, defaultModel := range []string{"", "gpt-4.1"} {
		t.Run("default="+defaultModel, func(t *testing.T) {
			// No catalog, provider resolver or credentials: listing the published
			// exposure must not depend on any upstream being available.
			routed := &RoutedProvider{route: &llmroute.LLMRoute{TargetPolicy: &llmroute.RouteModelPolicy{
				DefaultModel: defaultModel,
				ModelTargets: []llmroute.RouteModelTarget{
					{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "p", UpstreamModel: "gpt-4.1"},
					{Name: "gpt-4.1-mini", Kind: "provider-model", ProviderID: "p", UpstreamModel: "gpt-4.1-mini"},
				},
			}}}
			models, err := routed.ListModels(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != 2 || models[0].ID != "gpt-4.1" || models[0].Name != "gpt-4.1" || models[1].ID != "gpt-4.1-mini" {
				t.Fatalf("unexpected exposed models: %+v", models)
			}
		})
	}
}
