package openaibase

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/internal/httpclient"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

func TestBaseUsesEmbeddedProviderConfig(t *testing.T) {
	var hit bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.URL.Path != "/models" {
			t.Fatalf("request path = %q, want /models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	}))
	defer server.Close()

	cfg := provider.ProviderConfig{
		APIKey:       "test-key",
		BaseURL:      "http://127.0.0.1:1",
		Network:      httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
		AuthStrategy: provider.AuthStrategyManagedAPIKeyFirst,
	}
	base := NewBase(cfg)

	base.BaseURL = server.URL
	models, err := base.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if !hit {
		t.Fatal("server was not called")
	}
	if len(models) != 1 || models[0].ID != "test-model" {
		t.Fatalf("models = %#v, want test-model", models)
	}
}
