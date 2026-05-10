package gateway

import (
	"encoding/json"
	"testing"
	"time"

	configstoresqlite "github.com/agent-guide/agent-gateway/caddy/configstore/sqlite"
	_ "github.com/agent-guide/agent-gateway/caddy/provider/ollama"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

func TestParseAppFromCaddyfile(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		provider local-ollama {
			provider_type ollama
			base_url http://127.0.0.1:11434/v1
			default_model qwen2.5
		}

		config_store sqlite {
			path /tmp/agent-gateway.db
		}

		virtualkey key1 {
			tag admin
			name "Primary virtual key"
			description "configured from caddyfile"
			allowed_route openai-chat
			expires_at 2030-01-02T03:04:05Z
		}

		route openai-chat {
			llm_api openai
			host api.example.test
			path_prefix /tenant-a
			method POST
			require_virtual_key
			target provider local-ollama
		}
	}
	`)

	val, err := parseApp(d, nil)
	if err != nil {
		t.Fatalf("parseApp() error = %v", err)
	}

	appVal, ok := val.(httpcaddyfile.App)
	if !ok {
		t.Fatalf("parseApp() type = %T, want httpcaddyfile.App", val)
	}
	if appVal.Name != "agent_gateway" {
		t.Fatalf("app name = %q, want agent_gateway", appVal.Name)
	}

	var app App
	if err := json.Unmarshal(appVal.Value, &app); err != nil {
		t.Fatalf("unmarshal app json: %v", err)
	}

	if len(app.ConfigStoreRaw) != 1 {
		t.Fatalf("config_store count = %d, want 1", len(app.ConfigStoreRaw))
	}
	if len(app.ProvidersRaw) != 1 {
		t.Fatalf("provider count = %d, want 1", len(app.ProvidersRaw))
	}

	var ollama struct {
		Id           string `json:"id,omitempty"`
		ProviderType string `json:"provider_type,omitempty"`
		BaseURL      string `json:"base_url,omitempty"`
		DefaultModel string `json:"default_model,omitempty"`
	}
	if err := json.Unmarshal(app.ProvidersRaw["local-ollama"], &ollama); err != nil {
		t.Fatalf("unmarshal ollama provider: %v", err)
	}
	if ollama.Id != "local-ollama" {
		t.Fatalf("ollama id = %q, want local-ollama", ollama.Id)
	}
	if ollama.ProviderType != "ollama" {
		t.Fatalf("ollama provider_type = %q, want ollama", ollama.ProviderType)
	}
	if ollama.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Fatalf("ollama base_url = %q", ollama.BaseURL)
	}
	if ollama.DefaultModel != "qwen2.5" {
		t.Fatalf("ollama default_model = %q", ollama.DefaultModel)
	}

	var cfg configstoresqlite.SQLiteConfigStore
	if err := json.Unmarshal(app.ConfigStoreRaw["sqlite"], &cfg); err != nil {
		t.Fatalf("unmarshal sqlite config store: %v", err)
	}
	if cfg.SQLitePath != "/tmp/agent-gateway.db" {
		t.Fatalf("sqlite path = %q, want /tmp/agent-gateway.db", cfg.SQLitePath)
	}
	if len(app.Routes) != 1 {
		t.Fatalf("route count = %d, want 1", len(app.Routes))
	}
	if len(app.VirtualKeys) != 1 {
		t.Fatalf("virtual key count = %d, want 1", len(app.VirtualKeys))
	}

	route := app.Routes[0]
	if route.ID != "openai-chat" {
		t.Fatalf("route id = %q, want openai-chat", route.ID)
	}
	if route.LLMAPI != "openai" {
		t.Fatalf("route llm_api = %q, want openai", route.LLMAPI)
	}
	if route.Match.Host != "api.example.test" || route.Match.PathPrefix != "/tenant-a" {
		t.Fatalf("route match = %#v", route.Match)
	}
	if len(route.Match.Methods) != 1 || route.Match.Methods[0] != "POST" {
		t.Fatalf("route methods = %#v", route.Match.Methods)
	}
	if !route.AuthPolicy.RequireVirtualKey {
		t.Fatal("expected route require_virtual_key to be true")
	}
	if route.TargetPolicy.ProviderTarget.ProviderID != "local-ollama" {
		t.Fatalf("route provider_target = %#v", route.TargetPolicy.ProviderTarget)
	}

	key := app.VirtualKeys[0]
	if key.Key != "key1" {
		t.Fatalf("virtual key = %q, want key1", key.Key)
	}
	if key.Tag != "admin" {
		t.Fatalf("virtual key tag = %q, want admin", key.Tag)
	}
	if key.Name != "Primary virtual key" {
		t.Fatalf("virtual key name = %q", key.Name)
	}
	if len(key.AllowedRouteIDs) != 1 || key.AllowedRouteIDs[0] != "openai-chat" {
		t.Fatalf("virtual key allowed routes = %#v", key.AllowedRouteIDs)
	}
	wantExpiresAt := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	if !key.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("virtual key expires_at = %v, want %v", key.ExpiresAt, wantExpiresAt)
	}
}

func TestParseAppRejectsUnknownConfigStore(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		config_store memory
	}
	`)

	if _, err := parseApp(d, nil); err == nil {
		t.Fatal("expected unsupported config_store type to fail")
	}
}

func TestParseAppRejectsAuthenticatorDirective(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		authenticator codex
	}
	`)

	if _, err := parseApp(d, nil); err == nil {
		t.Fatal("expected authenticator directive to fail")
	}
}

func TestParseAppRejectsDuplicateRouteID(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		route openai-chat {
			target provider ollama
		}
		route openai-chat {
			target provider openai
		}
	}
	`)

	if _, err := parseApp(d, nil); err == nil {
		t.Fatal("expected duplicate route to fail")
	}
}

func TestParseAppRejectsLegacyRouteTargetSyntax(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		route openai-chat {
			target ollama
		}
	}
	`)

	if _, err := parseApp(d, nil); err == nil {
		t.Fatal("expected legacy route target syntax to fail")
	}
}

func TestParseAppRejectsLogicalModelDirective(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		logical_model chat-fast {
			bind openai-main gpt-4.1-mini
		}
	}
	`)

	if _, err := parseApp(d, nil); err == nil {
		t.Fatal("expected logical_model directive to fail")
	}
}

func TestParseAppInlineModelTargetsRegisterStaticManagedModels(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		route openai-chat {
			llm_api openai
			target model chat-fast openai-main gpt-4.1-mini weight 100 default
			target model chat-fast zhipu-main glm-4.5-air weight 50
		}
	}
	`)

	val, err := parseApp(d, nil)
	if err != nil {
		t.Fatalf("parseApp() error = %v", err)
	}

	appVal := val.(httpcaddyfile.App)
	var app App
	if err := json.Unmarshal(appVal.Value, &app); err != nil {
		t.Fatalf("unmarshal app json: %v", err)
	}

	if len(app.Routes) != 1 {
		t.Fatalf("route count = %d, want 1", len(app.Routes))
	}
	if len(app.Models) != 2 {
		t.Fatalf("managed model count = %d, want 2", len(app.Models))
	}
	if app.Routes[0].TargetPolicy.DefaultModel != "chat-fast" {
		t.Fatalf("default model = %q, want chat-fast", app.Routes[0].TargetPolicy.DefaultModel)
	}
	if len(app.Routes[0].TargetPolicy.ModelTargets) != 1 {
		t.Fatalf("target count = %d, want 1", len(app.Routes[0].TargetPolicy.ModelTargets))
	}
	if len(app.Routes[0].TargetPolicy.ModelTargets[0].Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(app.Routes[0].TargetPolicy.ModelTargets[0].Candidates))
	}
	for _, model := range app.Models {
		if !model.Enabled {
			t.Fatalf("managed model defaults = %#v, want enabled", model)
		}
	}
}

func TestParseAppRejectsDuplicateVirtualKey(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		virtualkey key1 {}
		virtualkey key1 {}
	}
	`)

	if _, err := parseApp(d, nil); err == nil {
		t.Fatal("expected duplicate virtualkey to fail")
	}
}

func TestParseVirtualKeySegmentAcceptsEmptyBlock(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	virtualkey key1 {
	}
	`)

	if !d.Next() {
		t.Fatal("expected virtualkey directive")
	}
	key, err := parseVirtualKeySegment(d)
	if err != nil {
		t.Fatalf("parseVirtualKeySegment() error = %v", err)
	}

	if key.Key != "key1" {
		t.Fatalf("virtual key = %q, want key1", key.Key)
	}
	if key.Tag != "" || key.Name != "" || key.Description != "" || key.Disabled || len(key.AllowedRouteIDs) != 0 || key.StatusMessage != "" || !key.ExpiresAt.IsZero() {
		t.Fatalf("unexpected virtual key defaults: %#v", key)
	}
}
