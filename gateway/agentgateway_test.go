package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/gateway/modelcatalog"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
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

func TestResolveRouteExecutionModelTargetRewritesToBinding(t *testing.T) {
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
	exec, err := gw.ResolveRouteExecution(context.Background(), resolvedRoute, routepkg.RouteResolveRequest{})
	if err != nil {
		t.Fatalf("ResolveRouteExecution returned error: %v", err)
	}
	if exec.Provider == nil {
		t.Fatal("ResolveRouteExecution returned nil provider")
	}
	if exec.UpstreamModel != "gpt-4.1-mini" {
		t.Fatalf("UpstreamModel = %q, want gpt-4.1-mini", exec.UpstreamModel)
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
