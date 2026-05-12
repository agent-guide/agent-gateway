package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	configstoreintf "github.com/agent-guide/agent-gateway/pkg/configstore/intf"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	credentialmgrscheduler "github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr/scheduler"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type testProvider struct {
	cfg provider.ProviderConfig
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

func (r testGatewayProviderConfigResolver) GetConfig(_ context.Context, providerID string) (provider.ProviderConfig, error) {
	return r.configs[providerID], nil
}

func TestNewRoutedProviderModelTargetRewritesDuringExecution(t *testing.T) {
	route := routepkg.AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		Match:  routepkg.RouteMatch{PathPrefix: "/v1"},
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
		StaticRoutes: []routepkg.AgentRoute{route},
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
		StaticRoutes: []routepkg.AgentRoute{{
			ID:       "chat-prod",
			Disabled: true,
			LLMAPI:   "openai",
			Match:    routepkg.RouteMatch{PathPrefix: "/v1"},
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

type testGatewayModelCatalogResolver struct{}

func (s *testGatewayProviderStore) ListByType(_ context.Context, name string) ([]any, error) {
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

func (s *testGatewayProviderStore) Create(_ context.Context, id string, name string, obj any) (string, error) {
	cfg, ok := obj.(*provider.ProviderConfig)
	if !ok {
		return "", nil
	}
	if s.items == nil {
		s.items = map[string]*provider.ProviderConfig{}
	}
	cloned := *cfg
	cloned.Id = id
	if cloned.ProviderType == "" {
		cloned.ProviderType = name
	}
	s.items[id] = &cloned
	return id, nil
}

func (s *testGatewayProviderStore) Update(ctx context.Context, id string, obj any) error {
	_, err := s.Create(ctx, id, "", obj)
	return err
}

func (s *testGatewayProviderStore) Delete(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

func (s *testGatewayProviderStore) Get(_ context.Context, id string) (string, any, error) {
	item := s.items[id]
	if item == nil {
		return "", nil, configstoreintf.ErrNotFound
	}
	cloned := *item
	tag := cloned.ProviderType
	cloned.ProviderType = ""
	return tag, &cloned, nil
}

func (testGatewayModelCatalogResolver) GetManagedModel(context.Context, string, string) (*modelcatalog.ManagedModel, bool, error) {
	return nil, false, nil
}

func (testGatewayModelCatalogResolver) GetResolvedManagedModel(context.Context, string, string) (*modelcatalog.ResolvedManagedModel, bool, error) {
	return nil, false, nil
}

type testGatewayConfigStore struct {
	providerStore configstoreintf.ProviderConfigStorer
}

func (s *testGatewayConfigStore) GetCredentialStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.CredentialStorer, error) {
	return nil, nil
}

func (s *testGatewayConfigStore) GetProviderConfigStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.ProviderConfigStorer, error) {
	return s.providerStore, nil
}

func (s *testGatewayConfigStore) GetVirtualKeyStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.VirtualKeyStorer, error) {
	return nil, nil
}

func (s *testGatewayConfigStore) GetRouteStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.RouteStorer, error) {
	return nil, nil
}

func (s *testGatewayConfigStore) GetModelStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.ModelStorer, error) {
	return nil, nil
}

func TestBootstrapSyncsDynamicProviderConfigCredentials(t *testing.T) {
	credMgr := credentialmgr.NewManager(nil)
	scheduler := credentialmgrscheduler.NewScheduler(nil)
	if listener, ok := scheduler.(credentialmgr.CredentialLifecycleListener); ok {
		credMgr.AddListener(listener)
	}

	gw := NewAgentGateway()
	if err := gw.Bootstrap(context.Background(), BootstrapOptions{
		ConfigStore: &testGatewayConfigStore{
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

	cred := credMgr.GetCredential("provider-config-api-key:deepseek-test")
	if cred == nil {
		t.Fatal("expected provider config credential to be registered")
	}
	if got := cred.Scope(); got != "id:deepseek-test" {
		t.Fatalf("credential scope = %q, want id:deepseek-test", got)
	}

	picked, err := scheduler.Pick(context.Background(), credentialmgrscheduler.Filter{
		Source:          credentialmgr.SourceAPIKey,
		CredentialScope: "id:deepseek-test",
		Model:           "deepseek-v4-pro",
	}, nil)
	if err != nil {
		t.Fatalf("scheduler Pick returned error: %v", err)
	}
	if picked == nil || picked.ID != "provider-config-api-key:deepseek-test" {
		t.Fatalf("picked credential = %#v, want provider-config-api-key:deepseek-test", picked)
	}
}

func TestDirectProviderRequiresCredentialSelection(t *testing.T) {
	routedProvider := &RoutedProvider{
		route: routepkg.AgentRoute{
			ID:     "chat-prod",
			LLMAPI: "openai",
			TargetPolicy: &routepkg.RouteDirectProviderPolicy{
				ProviderTarget: routepkg.DirectProviderTarget{ProviderID: "openai-main"},
			},
		},
		providerResolver: ProviderResolverFunc(func(context.Context, string) (provider.Provider, error) {
			return testProvider{cfg: provider.ProviderConfig{
				Id:           "openai-main",
				ProviderType: "openai",
				APIKey:       "provider-config-key",
			}}, nil
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

	_, err := routedProvider.Chat(context.Background(), &provider.ChatRequest{Model: "gpt-4.1"})
	if err == nil {
		t.Fatal("Chat returned nil error, want missing credential failure")
	}
	if !strings.Contains(err.Error(), `no credential available for provider "openai-main" model "gpt-4.1"`) {
		t.Fatalf("Chat error = %v, want missing credential error", err)
	}
}
