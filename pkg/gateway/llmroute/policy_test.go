package llmroute

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/credential"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func testProtocolRequirements(t *testing.T, features ...provider.ProtocolFeature) provider.ProtocolRequirementSet {
	t.Helper()
	values := make(map[provider.ProtocolFeature][]provider.RequirementReason, len(features))
	for _, feature := range features {
		values[feature] = []provider.RequirementReason{"test"}
	}
	set, err := provider.NewProtocolRequirementSet(values)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

type testModelCatalogResolver struct {
	models map[string]modelcatalog.ResolvedProviderModel
}

func (r testModelCatalogResolver) GetProviderModel(_ context.Context, providerID string, upstreamModel string) (*modelcatalog.ProviderModel, bool, error) {
	view, ok := r.models[providerID+"\x00"+upstreamModel]
	if !ok {
		return nil, false, nil
	}
	model := view.ProviderModel
	return &model, true, nil
}

func (r testModelCatalogResolver) GetResolvedProviderModel(_ context.Context, providerID string, upstreamModel string) (*modelcatalog.ResolvedProviderModel, bool, error) {
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

func TestCredentialTypeOrderDefaultsToAPIKeyThenOAuthToken(t *testing.T) {
	got := (RouteTargetPolicyCommon{}).CredentialTypeOrder()
	want := []RouteCredentialType{RouteCredentialTypeAPIKey, RouteCredentialTypeOAuthToken}
	if len(got) != len(want) {
		t.Fatalf("CredentialTypeOrder() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CredentialTypeOrder() = %v, want %v", got, want)
		}
	}
}

func TestResolveTargetFiltersProtocolFeatureCapability(t *testing.T) {
	provider.RegisterProviderTypeCapabilities("native-test", provider.ProviderTypeCapabilities{
		Dialect: provider.ProtocolDialectAnthropic,
		ProtocolFeatures: map[provider.ProtocolFeature]struct{}{
			provider.FeatureAnthropicNativeResponse: {}, provider.FeatureAnthropicNativeHistoryReplay: {},
		},
	})
	route := LLMRoute{
		RouteMetadata: RouteMetadata{ID: "native-route"},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "claude",
			ModelTargets: []RouteModelTarget{{
				Name: "claude",
				Kind: "logical-model", LogicalModelID: "claude",
			}},
		},
	}
	catalog := testModelCatalogResolver{models: map[string]modelcatalog.ResolvedProviderModel{
		"generic\x00model": {ProviderModel: modelcatalog.ProviderModel{ProviderID: "generic", UpstreamModel: "model", Enabled: true}, Capabilities: provider.ModelCapabilities{Tools: true}},
		"native\x00model":  {ProviderModel: modelcatalog.ProviderModel{ProviderID: "native", UpstreamModel: "model", Enabled: true}, Capabilities: provider.ModelCapabilities{Tools: true}},
	}}
	configs := testProviderConfigResolver{configs: map[string]provider.ProviderConfig{
		"generic": {Id: "generic", ProviderType: "generic-test"},
		"native":  {Id: "native", ProviderType: "native-test"},
	}}

	requirements := RequestRequirements{RequireTools: true, ProtocolRequirements: testProtocolRequirements(t, provider.FeatureAnthropicNativeHistoryReplay)}
	target, err := route.ResolveTarget(context.Background(), catalog, configs, requirements)
	if err != nil {
		t.Fatalf("ResolveTarget() error = %v", err)
	}
	if target.ProviderID != "native" {
		t.Fatalf("provider = %q, want native", target.ProviderID)
	}
}

func TestConcreteModelReturnsRequirementGap(t *testing.T) {
	route := LLMRoute{
		RouteMetadata: RouteMetadata{ID: "native-direct"},
		TargetPolicy:  &RouteModelPolicy{DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "generic", UpstreamModel: "gpt-4.1"}}},
	}
	_, err := route.ResolveTarget(context.Background(), testConcreteCatalog("generic", "anthropic", "reasoning", "openai-main"), testProviderConfigResolver{configs: map[string]provider.ProviderConfig{
		"generic": {Id: "generic", ProviderType: "generic-test", DefaultModel: "generic-default"},
	}}, RequestRequirements{ProtocolRequirements: testProtocolRequirements(t, provider.FeatureAnthropicNativeHistoryReplay)})
	var gaps *provider.RequirementGapsError
	if !errors.As(err, &gaps) || len(gaps.Gaps) != 1 {
		t.Fatalf("ResolveTarget() error = %v, want typed requirement gap", err)
	}
	gap := gaps.Gaps[0]
	if gap.Candidate.ProviderID != "generic" || gap.Candidate.ProviderType != "generic-test" || gap.Candidate.UpstreamModel != "gpt-4.1" {
		t.Fatalf("candidate identity = %+v, want complete concrete model identity", gap.Candidate)
	}
}

func TestConcreteAnthropicModelReportsMissingFeature(t *testing.T) {
	route := LLMRoute{
		RouteMetadata: RouteMetadata{ID: "anthropic-direct"},
		TargetPolicy:  &RouteModelPolicy{DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "anthropic", UpstreamModel: "gpt-4.1"}}},
	}
	_, err := route.ResolveTarget(context.Background(), testConcreteCatalog("generic", "anthropic", "reasoning", "openai-main"), testProviderConfigResolver{configs: map[string]provider.ProviderConfig{
		"anthropic": {Id: "anthropic", ProviderType: "anthropic"},
	}}, RequestRequirements{ProtocolRequirements: testProtocolRequirements(t, provider.FeatureAnthropicNativeHistoryReplay)})
	if err == nil || !strings.Contains(err.Error(), string(provider.FeatureAnthropicNativeHistoryReplay)) {
		t.Fatalf("ResolveTarget() error = %v, want missing feature", err)
	}
}

func TestLogicalModelNoEligibleBindingsNamesRequirements(t *testing.T) {
	route := LLMRoute{
		RouteMetadata: RouteMetadata{ID: "tools-route"},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "chat",
			ModelTargets: []RouteModelTarget{{
				Name: "chat",
				Kind: "provider-model", ProviderID: "generic", UpstreamModel: "model",
			}},
		},
	}
	catalog := testModelCatalogResolver{models: map[string]modelcatalog.ResolvedProviderModel{
		"generic\x00model": {
			ProviderModel: modelcatalog.ProviderModel{ProviderID: "generic", UpstreamModel: "model", Enabled: true},
		},
	}}
	configs := testProviderConfigResolver{configs: map[string]provider.ProviderConfig{
		"generic": {Id: "generic", ProviderType: "generic-test"},
	}}

	_, err := route.ResolveTarget(context.Background(), catalog, configs, RequestRequirements{RequireTools: true})
	if err == nil || !strings.Contains(err.Error(), "request requirements: tools") {
		t.Fatalf("ResolveTarget() error = %v, want tools capability diagnostic", err)
	}
}

func TestConcreteModelUsesAtomicReasoningCapability(t *testing.T) {
	provider.RegisterProviderTypeCapabilities("reasoning-test", provider.ProviderTypeCapabilities{
		Dialect:          provider.ProtocolDialectAnthropic,
		ProtocolFeatures: map[provider.ProtocolFeature]struct{}{provider.FeatureAnthropicReasoningReplay: {}},
	})
	route := LLMRoute{
		RouteMetadata: RouteMetadata{ID: "reasoning-direct"},
		TargetPolicy:  &RouteModelPolicy{DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "reasoning", UpstreamModel: "gpt-4.1"}}},
	}
	configs := testProviderConfigResolver{configs: map[string]provider.ProviderConfig{
		"reasoning": {Id: "reasoning", ProviderType: "reasoning-test"},
	}}
	if _, err := route.ResolveTarget(context.Background(), testConcreteCatalog("generic", "anthropic", "reasoning", "openai-main"), configs,
		RequestRequirements{ProtocolRequirements: testProtocolRequirements(t, provider.FeatureAnthropicReasoningReplay)}); err != nil {
		t.Fatalf("reasoning-capable provider rejected: %v", err)
	}
	if _, err := route.ResolveTarget(context.Background(), testConcreteCatalog("generic", "anthropic", "reasoning", "openai-main"), configs,
		RequestRequirements{ProtocolRequirements: testProtocolRequirements(t, provider.FeatureAnthropicNativeHistoryReplay)}); err == nil {
		t.Fatal("reasoning-only provider accepted full Anthropic-native state")
	}
}

func TestAgentRouteResolveTargetUsesRouteDefaultModel(t *testing.T) {
	route := LLMRoute{
		RouteMetadata: RouteMetadata{ID: "chat-prod"},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "gpt-4.1-mini",
			ModelTargets: []RouteModelTarget{{
				Name: "gpt-4.1-mini",
				Kind: "provider-model", ProviderID: "openai-main", UpstreamModel: "gpt-4.1-mini",
			}},
		},
	}

	target, err := route.ResolveTarget(
		context.Background(),
		testModelCatalogResolver{
			models: map[string]modelcatalog.ResolvedProviderModel{
				"openai-main\x00gpt-4.1-mini": {
					ProviderModel: modelcatalog.ProviderModel{
						ProviderID:      "openai-main",
						UpstreamModel:   "gpt-4.1-mini",
						CredentialScope: credential.ProviderIDCredentialScope("openai-main"),
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
	if target.ClientModel != "gpt-4.1-mini" {
		t.Fatalf("LogicalModel = %q, want gpt-4.1-mini", target.LogicalModel)
	}
	if target.ProviderID != "openai-main" {
		t.Fatalf("ProviderID = %q, want openai-main", target.ProviderID)
	}
	if target.UpstreamModel != "gpt-4.1-mini" {
		t.Fatalf("UpstreamModel = %q, want gpt-4.1-mini", target.UpstreamModel)
	}
	if target.CredentialScope != credential.ProviderIDCredentialScope("openai-main") {
		t.Fatalf("CredentialScope = %q, want %q", target.CredentialScope, credential.ProviderIDCredentialScope("openai-main"))
	}
}

func TestAgentRouteResolveTargetRejectsUnknownModel(t *testing.T) {
	route := LLMRoute{
		RouteMetadata: RouteMetadata{ID: "chat-prod"},
		TargetPolicy: &RouteModelPolicy{
			ModelTargets: []RouteModelTarget{{Name: "chat-fast"}},
		},
	}

	if _, err := route.ResolveTarget(
		context.Background(),
		testConcreteCatalog("generic", "anthropic", "reasoning", "openai-main"),
		testProviderConfigResolver{},
		RequestRequirements{Model: "chat-safe"},
	); err == nil {
		t.Fatal("ResolveTarget returned nil error, want unknown model rejection")
	}
}

func TestAgentRouteResolveTargetUsesConcreteModel(t *testing.T) {
	route := LLMRoute{
		RouteMetadata: RouteMetadata{ID: "chat-prod"},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai-main", UpstreamModel: "gpt-4.1"}},
		},
	}

	target, err := route.ResolveTarget(
		context.Background(),
		testModelCatalogResolver{
			models: map[string]modelcatalog.ResolvedProviderModel{
				"openai-main\x00gpt-4.1": {
					ProviderModel: modelcatalog.ProviderModel{
						ProviderID:      "openai-main",
						UpstreamModel:   "gpt-4.1",
						CredentialScope: credential.ProviderIDCredentialScope("tenant-a"),
						Enabled:         true,
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
	if target.LogicalModel != "" {
		t.Fatalf("LogicalModel = %q, want empty in concrete model exposure", target.LogicalModel)
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
	if target.CredentialScope != credential.ProviderIDCredentialScope("tenant-a") {
		t.Fatalf("CredentialScope = %q, want %q", target.CredentialScope, credential.ProviderIDCredentialScope("openai-main"))
	}
}

func TestAgentRouteResolveTargetUsesConcreteModelWhenModelNameIsPresent(t *testing.T) {
	route := LLMRoute{
		RouteMetadata: RouteMetadata{ID: "chat-prod"},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai-main", UpstreamModel: "gpt-4.1"}},
		},
	}

	target, err := route.ResolveTarget(
		context.Background(),
		testConcreteCatalog("generic", "anthropic", "reasoning", "openai-main"),
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
	if target.LogicalModel != "" {
		t.Fatalf("LogicalModel = %q, want empty in concrete model exposure", target.LogicalModel)
	}
}

func TestAgentRouteResolveTargetConcreteModelEmptyModelUsesRouteDefault(t *testing.T) {
	route := LLMRoute{
		RouteMetadata: RouteMetadata{ID: "chat-prod"},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai-main", UpstreamModel: "gpt-4.1"}},
		},
	}

	target, err := route.ResolveTarget(
		context.Background(),
		testConcreteCatalog("generic", "anthropic", "reasoning", "openai-main"),
		testProviderConfigResolver{
			configs: map[string]provider.ProviderConfig{
				"openai-main": {Id: "openai-main", ProviderType: "openai", DefaultModel: "gpt-4.1"},
			},
		},
		RequestRequirements{},
	)
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}
	if target.UpstreamModel != "gpt-4.1" {
		t.Fatalf("UpstreamModel = %q, want the provider default gpt-4.1", target.UpstreamModel)
	}
}

func (r testModelCatalogResolver) GetLogicalModel(_ context.Context, id string) (*modelcatalog.LogicalModel, bool, error) {
	models := map[string]modelcatalog.LogicalModel{
		"claude":  {ID: "claude", Candidates: []modelcatalog.ModelCandidate{{ProviderID: "generic", UpstreamModel: "model", Priority: 1}, {ProviderID: "native", UpstreamModel: "model", Priority: 2}}},
		"fixture": {ID: "fixture", Candidates: []modelcatalog.ModelCandidate{{ProviderID: "generic", UpstreamModel: "model", Priority: 1}, {ProviderID: "fixture", UpstreamModel: "model", Priority: 2}}},
	}
	m, ok := models[id]
	return &m, ok, nil
}

func testConcreteCatalog(ids ...string) testModelCatalogResolver {
	models := map[string]modelcatalog.ResolvedProviderModel{}
	for _, id := range ids {
		models[id+"\x00gpt-4.1"] = modelcatalog.ResolvedProviderModel{ProviderModel: modelcatalog.ProviderModel{ProviderID: id, UpstreamModel: "gpt-4.1", Enabled: true}}
	}
	return testModelCatalogResolver{models: models}
}
