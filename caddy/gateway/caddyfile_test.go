package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	configstoresqlite "github.com/agent-guide/agent-gateway/caddy/configstore/sqlite"
	_ "github.com/agent-guide/agent-gateway/caddy/provider/ollama"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
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

	var cfg configstoresqlite.SQLiteConfigStoreBackend
	if err := json.Unmarshal(app.ConfigStoreRaw["sqlite"], &cfg); err != nil {
		t.Fatalf("unmarshal sqlite config store: %v", err)
	}
	if cfg.SQLitePath != "/tmp/agent-gateway.db" {
		t.Fatalf("sqlite path = %q, want /tmp/agent-gateway.db", cfg.SQLitePath)
	}
	if len(app.Routes) != 1 {
		t.Fatalf("route count = %d, want 1", len(app.Routes))
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
	directPolicy, ok := routepkg.DirectProviderPolicyOf(route.TargetPolicy)
	if !ok || directPolicy.ProviderTarget.ProviderID != "local-ollama" {
		t.Fatalf("route target_policy = %#v", route.TargetPolicy)
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

func TestParseAppRejectsTargetModel(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		route openai-chat {
			llm_api openai
			target model chat-fast openai-main gpt-4.1-mini weight 100 default
		}
	}
	`)

	_, err := parseApp(d, nil)
	if err == nil {
		t.Fatal("expected target model to fail")
	}
	if !strings.Contains(err.Error(), "target model is no longer supported in the Caddyfile") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseAppRejectsLogicalTargetPolicy(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		route openai-chat {
			llm_api openai
			target_policy logical-model {
				model chat-fast openai-main gpt-4.1-mini
			}
		}
	}
	`)

	_, err := parseApp(d, nil)
	if err == nil {
		t.Fatal("expected logical-model target_policy to fail")
	}
	if !strings.Contains(err.Error(), "target_policy logical-model is no longer supported in the Caddyfile") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseAppRejectsVirtualKeyDirective(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_gateway {
		virtualkey key1 {}
	}
	`)

	_, err := parseApp(d, nil)
	if err == nil {
		t.Fatal("expected virtualkey directive to fail")
	}
	if !strings.Contains(err.Error(), "virtualkey is no longer supported in the Caddyfile") {
		t.Fatalf("unexpected error: %v", err)
	}
}
