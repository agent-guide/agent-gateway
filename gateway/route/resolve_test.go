package route

import (
	"context"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/gateway/modelcatalog"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

type testModelCatalogResolver struct {
	models map[string]modelcatalog.ResolvedManagedModel
}

func (r testModelCatalogResolver) GetManagedModel(_ context.Context, providerID string, upstreamModel string) (*modelcatalog.ManagedModel, bool, error) {
	view, ok := r.models[providerID+"\x00"+upstreamModel]
	if !ok {
		return nil, false, nil
	}
	model := view.ManagedModel
	return &model, true, nil
}

func (r testModelCatalogResolver) GetResolvedManagedModel(_ context.Context, providerID string, upstreamModel string) (*modelcatalog.ResolvedManagedModel, bool, error) {
	view, ok := r.models[providerID+"\x00"+upstreamModel]
	if !ok {
		return nil, false, nil
	}
	return &view, true, nil
}

type testProviderConfigResolver struct {
	configs map[string]provider.ProviderConfig
}

func (r testProviderConfigResolver) GetConfig(_ context.Context, providerID string) (provider.ProviderConfig, error) {
	return r.configs[providerID], nil
}

func TestAgentRouteResolveTargetUsesRouteDefaultModel(t *testing.T) {
	route := AgentRoute{
		ID: "chat-prod",
		TargetPolicy: RouteTargetPolicy{
			DefaultModel: "chat-fast",
			ModelTargets: []RouteModelTarget{{
				Name: "chat-fast",
				Candidates: []RouteModelCandidate{{
					ProviderID:    "openai-main",
					UpstreamModel: "gpt-4.1-mini",
				}},
			}},
		},
	}

	target, err := route.ResolveTarget(
		context.Background(),
		testModelCatalogResolver{
			models: map[string]modelcatalog.ResolvedManagedModel{
				"openai-main\x00gpt-4.1-mini": {
					ManagedModel: modelcatalog.ManagedModel{
						ProviderID:      "openai-main",
						UpstreamModel:   "gpt-4.1-mini",
						CredentialScope: credentialmgr.ProviderIDCredentialScope("openai-main"),
						Enabled:         true,
					},
					Capabilities: provider.ModelCapabilities{Streaming: true},
				},
			},
		},
		testProviderConfigResolver{
			configs: map[string]provider.ProviderConfig{
				"openai-main": {Id: "openai-main", ProviderType: "openai"},
			},
		},
		RequestRequirements{RequireStreaming: true},
	)
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}
	if target.Model != "chat-fast" {
		t.Fatalf("Model = %q, want chat-fast", target.Model)
	}
	if target.ProviderID != "openai-main" {
		t.Fatalf("ProviderID = %q, want openai-main", target.ProviderID)
	}
	if target.UpstreamModel != "gpt-4.1-mini" {
		t.Fatalf("UpstreamModel = %q, want gpt-4.1-mini", target.UpstreamModel)
	}
	if target.CredentialScope != credentialmgr.ProviderIDCredentialScope("openai-main") {
		t.Fatalf("CredentialScope = %q, want %q", target.CredentialScope, credentialmgr.ProviderIDCredentialScope("openai-main"))
	}
}

func TestAgentRouteResolveTargetRejectsUnknownModel(t *testing.T) {
	route := AgentRoute{
		ID: "chat-prod",
		TargetPolicy: RouteTargetPolicy{
			ModelTargets: []RouteModelTarget{{Name: "chat-fast"}},
		},
	}

	if _, err := route.ResolveTarget(
		context.Background(),
		testModelCatalogResolver{},
		testProviderConfigResolver{},
		RequestRequirements{Model: "chat-safe"},
	); err == nil {
		t.Fatal("ResolveTarget returned nil error, want unknown model rejection")
	}
}

func TestAgentRouteResolveTargetUsesDirectProvider(t *testing.T) {
	route := AgentRoute{
		ID: "chat-prod",
		TargetPolicy: RouteTargetPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "openai-main"},
		},
	}

	target, err := route.ResolveTarget(
		context.Background(),
		testModelCatalogResolver{
			models: map[string]modelcatalog.ResolvedManagedModel{
				"openai-main\x00gpt-4.1": {
					ManagedModel: modelcatalog.ManagedModel{
						ProviderID:      "openai-main",
						UpstreamModel:   "gpt-4.1",
						CredentialScope: credentialmgr.ProviderIDCredentialScope("tenant-a"),
					},
				},
			},
		},
		testProviderConfigResolver{
			configs: map[string]provider.ProviderConfig{
				"openai-main": {Id: "openai-main", ProviderType: "openai"},
			},
		},
		RequestRequirements{Model: "gpt-4.1"},
	)
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}
	if target.Model != "" {
		t.Fatalf("Model = %q, want empty in direct-provider mode", target.Model)
	}
	if target.ProviderID != "openai-main" {
		t.Fatalf("ProviderID = %q, want openai-main", target.ProviderID)
	}
	if target.ProviderType != "openai" {
		t.Fatalf("ProviderType = %q, want openai", target.ProviderType)
	}
	if target.UpstreamModel != "gpt-4.1" {
		t.Fatalf("UpstreamModel = %q, want gpt-4.1", target.UpstreamModel)
	}
	if target.CredentialScope != credentialmgr.ProviderIDCredentialScope("openai-main") {
		t.Fatalf("CredentialScope = %q, want %q", target.CredentialScope, credentialmgr.ProviderIDCredentialScope("openai-main"))
	}
}

func TestAgentRouteResolveTargetPrefersDirectProviderOverModelTargets(t *testing.T) {
	route := AgentRoute{
		ID: "chat-prod",
		TargetPolicy: RouteTargetPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "openai-main"},
			DefaultModel:   "chat-fast",
			ModelTargets: []RouteModelTarget{{
				Name: "chat-fast",
				Candidates: []RouteModelCandidate{{
					ProviderID:    "ignored-provider",
					UpstreamModel: "ignored-model",
				}},
			}},
		},
	}

	target, err := route.ResolveTarget(
		context.Background(),
		testModelCatalogResolver{},
		testProviderConfigResolver{
			configs: map[string]provider.ProviderConfig{
				"openai-main": {Id: "openai-main", ProviderType: "openai"},
			},
		},
		RequestRequirements{Model: "gpt-4.1"},
	)
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}
	if target.ProviderID != "openai-main" {
		t.Fatalf("ProviderID = %q, want openai-main", target.ProviderID)
	}
	if target.UpstreamModel != "gpt-4.1" {
		t.Fatalf("UpstreamModel = %q, want gpt-4.1", target.UpstreamModel)
	}
	if target.Model != "" {
		t.Fatalf("Model = %q, want empty in direct-provider mode", target.Model)
	}
}
