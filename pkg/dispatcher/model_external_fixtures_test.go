package dispatcher_test

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

// fixtureProviderModels declares concrete dependencies for route test fixtures.
func fixtureProviderModels(routes []routecore.RouteConfig, backend configstore.ConfigStoreBackend) []modelcatalog.ProviderModel {
	var out []modelcatalog.ProviderModel
	seen := map[[2]string]bool{}
	for _, cfg := range routes {
		r, err := llmroute.NewLLMRouteFromConfig(cfg)
		if err != nil {
			continue
		}
		p := r.TargetPolicy
		if p == nil {
			continue
		}
		for _, t := range p.ModelTargets {
			if t.Kind != "provider-model" {
				continue
			}
			key := [2]string{t.ProviderID, t.UpstreamModel}
			if seen[key] {
				continue
			}
			seen[key] = true
			if backend != nil {
				store, err := backend.Get(schema.StoreProviderModels)
				if err == nil && store != nil {
					if _, err := store.Get(context.Background(), t.ProviderID, t.UpstreamModel); err == nil {
						continue
					}
				}
			}
			out = append(out, modelcatalog.ProviderModel{ProviderID: t.ProviderID, UpstreamModel: t.UpstreamModel, Enabled: true, CapabilityOverrides: &provider.ModelCapabilities{Streaming: true, Tools: true, Vision: true}})
		}
	}
	return out
}
