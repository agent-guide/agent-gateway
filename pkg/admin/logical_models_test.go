package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func TestModelResourceAdminLifecycle(t *testing.T) {
	creator, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = creator.Close() })
	backend := configstore.NewBackend(creator)
	if err := schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	g := gateway.NewAgentGateway()
	if err := g.Bootstrap(t.Context(), gateway.BootstrapOptions{ConfigStoreBackend: backend}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: "p", ProviderType: "openai"}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(g, nil)
	request := func(method, path, body string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s = %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
	}
	request(http.MethodPost, "/admin/llm/models/provider", `{"provider_id":"p","upstream_model":"m","enabled":true}`, 201)
	request(http.MethodPost, "/admin/llm/models/logical", `{"id":"chat","candidates":[{"provider_id":"p","upstream_model":"m"}]}`, 201)
	request(http.MethodGet, "/admin/llm/models/logical", "", 200)
	request(http.MethodGet, "/admin/llm/models/logical/chat", "", 200)
	request(http.MethodPut, "/admin/llm/models/logical/chat", `{"id":"other","candidates":[{"provider_id":"p","upstream_model":"m"}]}`, 400)
	request(http.MethodPut, "/admin/llm/models/logical/chat", `{"disabled":true,"candidates":[{"provider_id":"p","upstream_model":"m"}]}`, 200)
	request(http.MethodDelete, "/admin/llm/models/provider/p/m", "", 409)
	request(http.MethodPost, "/admin/llm/routes", `{"id":"r","protocol":"openai","match_policy":{"path_prefix":"/r"},"target_policy":{"type":"models","model_targets":[{"name":"default","kind":"logical-model","logical_model_id":"chat"}]}}`, 201)
	request(http.MethodDelete, "/admin/llm/models/logical/chat", "", 409)
	request(http.MethodDelete, "/admin/llm/routes/r", "", 200)
	request(http.MethodDelete, "/admin/llm/models/logical/chat", "", 200)
	request(http.MethodDelete, "/admin/llm/models/provider/p/m", "", 200)
	request(http.MethodGet, "/admin/llm/models/logical/chat", "", 404)
	request(http.MethodPut, "/admin/llm/models/logical/chat", `{"candidates":[{"provider_id":"p","upstream_model":"missing"}]}`, 400)
	request(http.MethodPost, "/admin/llm/routes", `{"id":"legacy","protocol":"openai","target_policy":{"type":"logical-model","model_targets":[]}}`, 400)
}
