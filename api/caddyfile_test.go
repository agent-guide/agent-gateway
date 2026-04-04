package api_test

import (
	"strings"
	"testing"

	api "github.com/agent-guide/caddy-agent-gateway/api"
	_ "github.com/agent-guide/caddy-agent-gateway/api/llmapi/openai"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
	caddyfileadapter "github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

func TestParseLLMAPIRequiresRouteID(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	llm_api openai
	`)

	_, err := api.ParseLLMAPIForTest(httpcaddyfile.Helper{Dispenser: d})
	if err == nil || !strings.Contains(err.Error(), "llm_route_id is required") {
		t.Fatalf("expected llm_route_id is required error, got %v", err)
	}
}

func TestParseLLMAPI(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	llm_api openai {
		llm_route_id chat-prod
	}
	`)

	handler, err := api.ParseLLMAPIForTest(httpcaddyfile.Helper{Dispenser: d})
	if err != nil {
		t.Fatalf("parseLLMAPI() error = %v", err)
	}

	llmHandler, ok := handler.(*api.Handler)
	if !ok {
		t.Fatalf("handler type = %T, want *api.Handler", handler)
	}
	if llmHandler.RouteID != "chat-prod" {
		t.Fatalf("llm_route_id = %q, want chat-prod", llmHandler.RouteID)
	}
}

func TestParseLLMAPIRejectsUnknownSubdirective(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	llm_api openai {
		llm_route_id chat-prod
		model gpt-4.1
	}
	`)

	_, err := api.ParseLLMAPIForTest(httpcaddyfile.Helper{Dispenser: d})
	if err == nil || !strings.Contains(err.Error(), "unknown subdirective: model") {
		t.Fatalf("expected unknown subdirective error, got %v", err)
	}
}

func TestLLMAPIAdaptUsesParentAndChildHandlerNames(t *testing.T) {
	input := []byte(`
		:8080 {
			route /v1/* {
				llm_api openai {
					llm_route_id chat-prod
				}
			}
		}
	`)

	adapter := caddyfileadapter.Adapter{ServerType: httpcaddyfile.ServerType{}}
	adapted, _, err := adapter.Adapt(input, nil)
	if err != nil {
		t.Fatalf("caddy.Adapt() error = %v", err)
	}

	json := string(adapted)
	if !strings.Contains(json, `"handler":"llm_api"`) {
		t.Fatalf("adapted config missing llm_api handler: %s", json)
	}
	if !strings.Contains(json, `"api_handler":{"handler":"openai"}`) {
		t.Fatalf("adapted config missing child openai handler: %s", json)
	}
}
