package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/agent-guide/agent-gateway/internal/statuserr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	configstoreschema "github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	credentialmgrscheduler "github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr/scheduler"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type testProvider struct {
	cfg provider.ProviderConfig
}

type testContextCaptureProvider struct {
	cfg        provider.ProviderConfig
	credential *credentialmgr.Credential
}

type testCredentialAwareProvider struct {
	cfg      provider.ProviderConfig
	attempts *[]string
	failures map[string]error
}

type testGatewayProviderConfigResolver struct {
	configs map[string]provider.ProviderConfig
}

func (p testProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, nil
}

func (p testProvider) StreamChat(context.Context, *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (p testProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{
		ID:           "gpt-4.1-mini",
		DisplayName:  "gpt-4.1-mini",
		Capabilities: provider.ModelCapabilities{Streaming: true},
	}}, nil
}

func (p testProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Streaming: true}
}

func (p testProvider) Config() provider.ProviderConfig {
	return p.cfg
}

func (p *testContextCaptureProvider) Chat(ctx context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.credential, _ = provider.CredentialFromContext(ctx)
	return &provider.ChatResponse{}, nil
}

func (p *testContextCaptureProvider) StreamChat(context.Context, *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (p *testContextCaptureProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}

func (p *testContextCaptureProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func (p *testContextCaptureProvider) Config() provider.ProviderConfig {
	return p.cfg
}

func (p *testCredentialAwareProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	cred, _ := provider.CredentialFromContext(ctx)
	apiKey := ""
	if cred != nil {
		apiKey = cred.APIKey()
	}
	attempt := req.Model + "|" + apiKey
	if p.attempts != nil {
		*p.attempts = append(*p.attempts, attempt)
	}
	if err := p.failures[attempt]; err != nil {
		return nil, err
	}
	return &provider.ChatResponse{}, nil
}

func (p *testCredentialAwareProvider) StreamChat(context.Context, *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (p *testCredentialAwareProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}

func (p *testCredentialAwareProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{}
}

func (p *testCredentialAwareProvider) Config() provider.ProviderConfig {
	return p.cfg
}

func (r testGatewayProviderConfigResolver) GetConfig(_ context.Context, providerID string) (provider.ProviderConfig, error) {
	return r.configs[providerID], nil
}

func TestNewRoutedProviderModelTargetRewritesDuringExecution(t *testing.T) {
	route := routepkg.LLMRoute{
		AgentRouteConfig: routepkg.AgentRouteConfig{
			ID:          "chat-prod",
			Protocol:    routepkg.RouteProtocolOpenAI,
			MatchPolicy: routepkg.RouteMatchPolicy{PathPrefix: "/v1"},
		},
		TargetPolicy: &routepkg.RouteLogicalModelTargetPolicy{
			ModelTargets: []routepkg.RouteModelTarget{{
				Name: "chat-fast",
				Candidates: []routepkg.RouteModelCandidate{{
					ProviderID:    "openai",
					UpstreamModel: "gpt-4.1-mini",
				}},
			}},
			DefaultModel: "chat-fast",
		},
	}
	gw := NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), BootstrapOptions{
		StaticRoutes: []routepkg.LLMRoute{route},
		StaticProviders: map[string]provider.Provider{
			"openai": testProvider{cfg: provider.ProviderConfig{Id: "openai", ProviderType: "openai"}},
		},
		StaticModels: []modelcatalog.ManagedModel{{
			ProviderID:    "openai",
			UpstreamModel: "gpt-4.1-mini",
			Enabled:       true,
		}},
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	resolvedRoute, err := gw.ResolveRoute(context.Background(), req)
	if err != nil {
		t.Fatalf("ResolveRoute returned error: %v", err)
	}
	routedProvider, err := gw.NewRoutedProvider(resolvedRoute, routepkg.RequestRequirements{})
	if err != nil {
		t.Fatalf("NewRoutedProvider returned error: %v", err)
	}
	if routedProvider == nil {
		t.Fatal("NewRoutedProvider returned nil provider")
	}

	chatReq := &provider.ChatRequest{Model: "chat-fast"}
	if _, err := routedProvider.Chat(context.Background(), chatReq); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if chatReq.Model != "gpt-4.1-mini" {
		t.Fatalf("Chat request model = %q, want gpt-4.1-mini", chatReq.Model)
	}
}

func TestResolveRejectsDisabledRoute(t *testing.T) {
	gw := NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), BootstrapOptions{
		StaticRoutes: []routepkg.LLMRoute{{
			AgentRouteConfig: routepkg.AgentRouteConfig{
				ID:          "chat-prod",
				Protocol:    routepkg.RouteProtocolOpenAI,
				Disabled:    true,
				MatchPolicy: routepkg.RouteMatchPolicy{PathPrefix: "/v1"},
			},
			TargetPolicy: &routepkg.RouteDirectProviderPolicy{
				ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "openai"},
			},
		}},
		StaticProviders: map[string]provider.Provider{
			"openai": testProvider{cfg: provider.ProviderConfig{Id: "openai", ProviderType: "openai"}},
		},
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := gw.ResolveRoute(context.Background(), req)
	if err == nil {
		t.Fatal("ResolveRoute returned nil error, want disabled route rejection")
	}
}

type testGatewayProviderStore struct {
	items map[string]*provider.ProviderConfig
}

type testGatewayModelCatalogResolver struct {
	models map[string]modelcatalog.ResolvedManagedModel
}

func (s *testGatewayProviderStore) List(ctx context.Context) ([]any, error) {
	return s.ListByTag(ctx, "")
}

func (s *testGatewayProviderStore) ListByTag(_ context.Context, name string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		if name != "" && item.ProviderType != name {
			continue
		}
		cloned := *item
		out = append(out, &cloned)
	}
	return out, nil
}

func (s *testGatewayProviderStore) ListByTagPrefix(ctx context.Context, tagPrefix string) ([]any, error) {
	return s.ListByTag(ctx, tagPrefix)
}

func (s *testGatewayProviderStore) Create(_ context.Context, obj any) error {
	cfg, ok := obj.(*provider.ProviderConfig)
	if !ok {
		return nil
	}
	if s.items == nil {
		s.items = map[string]*provider.ProviderConfig{}
	}
	cloned := *cfg
	s.items[cloned.Id] = &cloned
	return nil
}

func (s *testGatewayProviderStore) Update(ctx context.Context, obj any) error {
	return s.Create(ctx, obj)
}

func (s *testGatewayProviderStore) Delete(_ context.Context, keyParts ...any) error {
	id, _ := keyParts[0].(string)
	delete(s.items, id)
	return nil
}

func (s *testGatewayProviderStore) Get(_ context.Context, keyParts ...any) (any, error) {
	id, _ := keyParts[0].(string)
	item := s.items[id]
	if item == nil {
		return nil, configstore.ErrNotFound
	}
	cloned := *item
	return &cloned, nil
}

func (s *testGatewayProviderStore) GetByIndex(context.Context, string, any) (any, error) {
	return nil, configstore.ErrNotFound
}

func (testGatewayModelCatalogResolver) GetManagedModel(context.Context, string, string) (*modelcatalog.ManagedModel, bool, error) {
	return nil, false, nil
}

func (r testGatewayModelCatalogResolver) GetResolvedManagedModel(_ context.Context, providerID string, upstreamModel string) (*modelcatalog.ResolvedManagedModel, bool, error) {
	item, ok := r.models[providerID+"/"+upstreamModel]
	if !ok {
		return nil, false, nil
	}
	cloned := item
	return &cloned, true, nil
}

type testGatewayConfigStore struct {
	providerStore configstore.ConfigStore
}

func (s *testGatewayConfigStore) Register(string, configstore.StoreSchema) error {
	return nil
}

func (s *testGatewayConfigStore) Get(name string) (configstore.ConfigStore, error) {
	if name == configstoreschema.StoreProviders {
		return s.providerStore, nil
	}
	return nil, nil
}

func TestBootstrapDoesNotSyncDynamicProviderConfigCredentials(t *testing.T) {
	credMgr := credentialmgr.NewManager(nil)
	scheduler := credentialmgrscheduler.NewScheduler(nil)
	if listener, ok := scheduler.(credentialmgr.CredentialLifecycleListener); ok {
		credMgr.AddListener(listener)
	}

	gw := NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), BootstrapOptions{
		ConfigStoreBackend: &testGatewayConfigStore{
			providerStore: &testGatewayProviderStore{
				items: map[string]*provider.ProviderConfig{
					"deepseek-test": {
						Id:           "deepseek-test",
						ProviderType: "deepseek",
						APIKey:       "deepseek-key",
						BaseURL:      "https://deepseek.example",
					},
				},
			},
		},
		CredentialManager:   credMgr,
		CredentialScheduler: scheduler,
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}

	if cred := credMgr.GetCredential("provider-config-api-key:deepseek-test"); cred != nil {
		t.Fatalf("provider config credential should not be registered, got %#v", cred)
	}
}

func TestDirectProviderFallsBackToProviderConfigAPIKey(t *testing.T) {
	capture := &testContextCaptureProvider{
		cfg: provider.ProviderConfig{
			Id:           "openai-main",
			ProviderType: "openai",
			APIKey:       "provider-config-key",
		},
	}
	routedProvider := &RoutedProvider{
		route: &routepkg.LLMRoute{
			AgentRouteConfig: routepkg.AgentRouteConfig{ID: "chat-prod", Protocol: routepkg.RouteProtocolOpenAI},
			TargetPolicy: &routepkg.RouteDirectProviderPolicy{
				ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "openai-main"},
			},
		},
		providerResolver: ProviderResolverFunc(func(context.Context, string) (provider.Provider, error) {
			return capture, nil
		}),
		modelCatalog: testGatewayModelCatalogResolver{},
		providerConfigs: testGatewayProviderConfigResolver{
			configs: map[string]provider.ProviderConfig{
				"openai-main": {Id: "openai-main", ProviderType: "openai", APIKey: "provider-config-key"},
			},
		},
		credentialMgr: credentialmgr.NewManager(nil),
		scheduler:     credentialmgrscheduler.NewScheduler(nil),
	}

	if _, err := routedProvider.Chat(context.Background(), &provider.ChatRequest{Model: "gpt-4.1"}); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if capture.credential != nil {
		t.Fatalf("credential = %#v, want provider config fallback without managed credential", capture.credential)
	}
}

func TestRoutedProviderInjectsExplicitCredentialValuesIntoContext(t *testing.T) {
	credMgr := credentialmgr.NewManager(nil)
	scheduler := credentialmgrscheduler.NewScheduler(nil)
	if listener, ok := scheduler.(credentialmgr.CredentialLifecycleListener); ok {
		credMgr.AddListener(listener)
	}

	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "cred-openai-managed",
		ProviderType: "openai",
		ProviderID:   "openai-main",
		Scope:        "id:openai-main",
		Type:         credentialmgr.TypeAPIKey,
		Attributes: map[string]string{
			"api_key": "managed-key",
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	capture := &testContextCaptureProvider{
		cfg: provider.ProviderConfig{
			Id:           "openai-main",
			ProviderType: "openai",
		},
	}
	routedProvider := &RoutedProvider{
		route: &routepkg.LLMRoute{
			AgentRouteConfig: routepkg.AgentRouteConfig{ID: "chat-prod", Protocol: routepkg.RouteProtocolOpenAI},
			TargetPolicy: &routepkg.RouteDirectProviderPolicy{
				ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "openai-main"},
			},
		},
		providerResolver: ProviderResolverFunc(func(context.Context, string) (provider.Provider, error) {
			return capture, nil
		}),
		modelCatalog: testGatewayModelCatalogResolver{},
		providerConfigs: testGatewayProviderConfigResolver{
			configs: map[string]provider.ProviderConfig{
				"openai-main": {Id: "openai-main", ProviderType: "openai"},
			},
		},
		credentialMgr: credMgr,
		scheduler:     scheduler,
	}

	if _, err := routedProvider.Chat(context.Background(), &provider.ChatRequest{Model: "gpt-4.1"}); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if capture.credential == nil {
		t.Fatal("credential = nil, want managed credential")
	}
	if capture.credential.APIKey() != "managed-key" {
		t.Fatalf("api key = %q, want managed-key", capture.credential.APIKey())
	}
}

func TestRoutedProviderRetriesAnotherCredentialBeforeModelFallback(t *testing.T) {
	credMgr := credentialmgr.NewManager(nil)
	scheduler := credentialmgrscheduler.NewScheduler(nil)
	if listener, ok := scheduler.(credentialmgr.CredentialLifecycleListener); ok {
		credMgr.AddListener(listener)
	}

	for _, cred := range []*credentialmgr.Credential{
		{
			ID:           "cred-openai-bad",
			ProviderType: "openai",
			ProviderID:   "openai-main",
			Scope:        "id:openai-main",
			Type:         credentialmgr.TypeAPIKey,
			Attributes: map[string]string{
				"api_key": "bad-key",
			},
		},
		{
			ID:           "cred-openai-good",
			ProviderType: "openai",
			ProviderID:   "openai-main",
			Scope:        "id:openai-main",
			Type:         credentialmgr.TypeAPIKey,
			Attributes: map[string]string{
				"api_key": "good-key",
			},
		},
	} {
		if err := credMgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register credential %q: %v", cred.ID, err)
		}
	}

	var attempts []string
	prov := &testCredentialAwareProvider{
		cfg:      provider.ProviderConfig{Id: "openai-main", ProviderType: "openai"},
		attempts: &attempts,
		failures: map[string]error{
			"gpt-4.1|bad-key": statuserr.New(http.StatusServiceUnavailable, "upstream unavailable"),
		},
	}
	routedProvider := &RoutedProvider{
		route: &routepkg.LLMRoute{
			AgentRouteConfig: routepkg.AgentRouteConfig{ID: "chat-prod", Protocol: routepkg.RouteProtocolOpenAI},
			TargetPolicy: &routepkg.RouteDirectProviderPolicy{
				ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "openai-main"},
			},
		},
		providerResolver: ProviderResolverFunc(func(context.Context, string) (provider.Provider, error) {
			return prov, nil
		}),
		modelCatalog: testGatewayModelCatalogResolver{},
		providerConfigs: testGatewayProviderConfigResolver{
			configs: map[string]provider.ProviderConfig{
				"openai-main": {Id: "openai-main", ProviderType: "openai"},
			},
		},
		credentialMgr: credMgr,
		scheduler:     scheduler,
	}

	if _, err := routedProvider.Chat(context.Background(), &provider.ChatRequest{Model: "gpt-4.1"}); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if !slices.Equal(attempts, []string{"gpt-4.1|bad-key", "gpt-4.1|good-key"}) {
		t.Fatalf("attempts = %v, want bad credential retry before success", attempts)
	}
}

func TestRoutedProviderFallsBackToAnotherModelAfterCandidateCredentialsExhausted(t *testing.T) {
	credMgr := credentialmgr.NewManager(nil)
	scheduler := credentialmgrscheduler.NewScheduler(nil)
	if listener, ok := scheduler.(credentialmgr.CredentialLifecycleListener); ok {
		credMgr.AddListener(listener)
	}

	for _, cred := range []*credentialmgr.Credential{
		{
			ID:           "cred-openai-main",
			ProviderType: "openai",
			ProviderID:   "openai-main",
			Scope:        "id:openai-main",
			Type:         credentialmgr.TypeAPIKey,
			Attributes: map[string]string{
				"api_key": "main-key",
			},
		},
		{
			ID:           "cred-openai-backup",
			ProviderType: "openai",
			ProviderID:   "openai-backup",
			Scope:        "id:openai-backup",
			Type:         credentialmgr.TypeAPIKey,
			Attributes: map[string]string{
				"api_key": "backup-key",
			},
		},
	} {
		if err := credMgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register credential %q: %v", cred.ID, err)
		}
	}

	var attempts []string
	providers := map[string]provider.Provider{
		"openai-main": &testCredentialAwareProvider{
			cfg:      provider.ProviderConfig{Id: "openai-main", ProviderType: "openai"},
			attempts: &attempts,
			failures: map[string]error{
				"gpt-4.1|main-key": statuserr.New(http.StatusServiceUnavailable, "main unavailable"),
			},
		},
		"openai-backup": &testCredentialAwareProvider{
			cfg:      provider.ProviderConfig{Id: "openai-backup", ProviderType: "openai"},
			attempts: &attempts,
			failures: map[string]error{},
		},
	}
	routedProvider := &RoutedProvider{
		route: &routepkg.LLMRoute{
			AgentRouteConfig: routepkg.AgentRouteConfig{ID: "chat-prod", Protocol: routepkg.RouteProtocolOpenAI},
			TargetPolicy: &routepkg.RouteLogicalModelTargetPolicy{
				DefaultModel:          "chat-fast",
				ModelSelectorStrategy: routepkg.RouteSelectionStrategyPriority,
				Fallback:              routepkg.RouteFallbackPolicy{Enabled: true, MaxNum: 1},
				ModelTargets: []routepkg.RouteModelTarget{{
					Name: "chat-fast",
					Candidates: []routepkg.RouteModelCandidate{
						{ProviderID: "openai-main", UpstreamModel: "gpt-4.1", Priority: 2},
						{ProviderID: "openai-backup", UpstreamModel: "gpt-4.1-mini", Priority: 1},
					},
				}},
			},
		},
		providerResolver: ProviderResolverFunc(func(_ context.Context, providerID string) (provider.Provider, error) {
			return providers[providerID], nil
		}),
		modelCatalog: testGatewayModelCatalogResolver{
			models: map[string]modelcatalog.ResolvedManagedModel{
				"openai-main/gpt-4.1": {
					ManagedModel: modelcatalog.ManagedModel{
						ProviderID:      "openai-main",
						UpstreamModel:   "gpt-4.1",
						Enabled:         true,
						CredentialScope: credentialmgr.ProviderIDCredentialScope("openai-main"),
					},
				},
				"openai-backup/gpt-4.1-mini": {
					ManagedModel: modelcatalog.ManagedModel{
						ProviderID:      "openai-backup",
						UpstreamModel:   "gpt-4.1-mini",
						Enabled:         true,
						CredentialScope: credentialmgr.ProviderIDCredentialScope("openai-backup"),
					},
				},
			},
		},
		providerConfigs: testGatewayProviderConfigResolver{
			configs: map[string]provider.ProviderConfig{
				"openai-main":   {Id: "openai-main", ProviderType: "openai"},
				"openai-backup": {Id: "openai-backup", ProviderType: "openai"},
			},
		},
		credentialMgr: credMgr,
		scheduler:     scheduler,
	}

	req := &provider.ChatRequest{Model: "chat-fast"}
	if _, err := routedProvider.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if req.Model != "gpt-4.1-mini" {
		t.Fatalf("request model = %q, want fallback model gpt-4.1-mini", req.Model)
	}
	if !slices.Equal(attempts, []string{"gpt-4.1|main-key", "gpt-4.1-mini|backup-key"}) {
		t.Fatalf("attempts = %v, want same candidate exhaustion before model fallback", attempts)
	}
}
