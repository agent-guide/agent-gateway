package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	configstoresqlite "github.com/agent-guide/caddy-agent-gateway/configstore/sqlite"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth"
	_ "github.com/agent-guide/caddy-agent-gateway/llm/provider/ollama"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

func init() {
	caddy.RegisterModule(testAuthenticatorModule{})
}

type testAuthenticatorModule struct {
	Foo string `json:"foo,omitempty"`
}

func (testAuthenticatorModule) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "llm.authenticators.test",
		New: func() caddy.Module { return new(testAuthenticatorModule) },
	}
}

func (m *testAuthenticatorModule) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "foo":
				if !d.NextArg() {
					return d.ArgErr()
				}
				m.Foo = d.Val()
			default:
				return d.Errf("unknown subdirective: %s", d.Val())
			}
		}
	}
	return nil
}

func (testAuthenticatorModule) Provider() string { return "test" }

func (testAuthenticatorModule) Login(context.Context) (*cliauth.Credential, error) {
	return nil, nil
}

func (testAuthenticatorModule) RefreshLead(context.Context, *cliauth.Credential) (*cliauth.Credential, error) {
	return nil, nil
}

var _ cliauth.Authenticator = (*testAuthenticatorModule)(nil)

func TestParseAppFromCaddyfile(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		provider local-ollama {
			provider_name ollama
			base_url http://127.0.0.1:11434/v1
			default_model qwen2.5
		}

		config_store sqlite {
			path /tmp/caddy-agent-gateway.db
		}

		authenticator test {
			foo bar
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
			allowed_model gpt-4.1
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
		ProviderName string `json:"provider_name,omitempty"`
		BaseURL      string `json:"base_url,omitempty"`
		DefaultModel string `json:"default_model,omitempty"`
	}
	if err := json.Unmarshal(app.ProvidersRaw["local-ollama"], &ollama); err != nil {
		t.Fatalf("unmarshal ollama provider: %v", err)
	}
	if ollama.Id != "local-ollama" {
		t.Fatalf("ollama id = %q, want local-ollama", ollama.Id)
	}
	if ollama.ProviderName != "ollama" {
		t.Fatalf("ollama provider_name = %q, want ollama", ollama.ProviderName)
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
	if cfg.SQLitePath != "/tmp/caddy-agent-gateway.db" {
		t.Fatalf("sqlite path = %q, want /tmp/caddy-agent-gateway.db", cfg.SQLitePath)
	}
	if len(app.AuthenticatorsRaw) != 1 {
		t.Fatalf("authenticator count = %d, want 1", len(app.AuthenticatorsRaw))
	}
	if len(app.Routes) != 1 {
		t.Fatalf("route count = %d, want 1", len(app.Routes))
	}
	if len(app.VirtualKeys) != 1 {
		t.Fatalf("virtual key count = %d, want 1", len(app.VirtualKeys))
	}

	var codex struct {
		Foo string `json:"foo,omitempty"`
	}
	if err := json.Unmarshal(app.AuthenticatorsRaw["test"], &codex); err != nil {
		t.Fatalf("unmarshal test authenticator: %v", err)
	}
	if codex.Foo != "bar" {
		t.Fatalf("unexpected test authenticator config: %+v", codex)
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
	if !route.Policy.Auth.RequireVirtualKey {
		t.Fatal("expected route require_virtual_key to be true")
	}
	if len(route.Policy.AllowedModels) != 1 || route.Policy.AllowedModels[0] != "gpt-4.1" {
		t.Fatalf("route allowed_models = %#v", route.Policy.AllowedModels)
	}
	if len(route.Targets) != 1 || route.Targets[0].ProviderRef != "local-ollama" {
		t.Fatalf("route targets = %#v", route.Targets)
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
