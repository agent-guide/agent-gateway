package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type testProvider struct {
	cfg provider.ProviderConfig
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

func TestNewRoutedProviderModelTargetRewritesDuringExecution(t *testing.T) {
	route := routepkg.AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		Match:  routepkg.RouteMatch{PathPrefix: "/v1"},
		TargetPolicy: routepkg.RouteTargetPolicy{
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
			TargetPolicy: routepkg.RouteTargetPolicy{
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
