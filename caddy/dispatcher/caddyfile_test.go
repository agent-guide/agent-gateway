package dispatcher

import (
	"strings"
	"testing"

	_ "github.com/agent-guide/agent-gateway/caddy/dispatcher/llmapi/anthropic"
	_ "github.com/agent-guide/agent-gateway/caddy/dispatcher/llmapi/openai"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	caddyfileadapter "github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
)

func TestParseAgentRouteDispatcher(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_route_dispatcher {
		llm_api openai
		llm_api anthropic
		mcp
	}
	`)

	handler, err := parseAgentRouteDispatcher(httpcaddyfile.Helper{Dispenser: d})
	if err != nil {
		t.Fatalf("parseAgentRouteDispatcher() error = %v", err)
	}

	dispatcherHandler, ok := handler.(*AgentRouteDispatcher)
	if !ok {
		t.Fatalf("handler type = %T, want *AgentRouteDispatcher", handler)
	}
	if len(dispatcherHandler.APIHandlersRaw) != 2 {
		t.Fatalf("api handler count = %d, want 2", len(dispatcherHandler.APIHandlersRaw))
	}
	if _, ok := dispatcherHandler.APIHandlersRaw["openai"]; !ok {
		t.Fatal("missing openai api handler")
	}
	if _, ok := dispatcherHandler.APIHandlersRaw["anthropic"]; !ok {
		t.Fatal("missing anthropic api handler")
	}
	if !dispatcherHandler.EnableMCP {
		t.Fatal("expected mcp to be enabled")
	}
}

func TestAgentRouteDispatcherAdaptUsesHandlerType(t *testing.T) {
	input := []byte(`
		:8080 {
			agent_route_dispatcher {
				llm_api openai
				llm_api anthropic
				mcp
			}
		}
	`)

	adapter := caddyfileadapter.Adapter{ServerType: httpcaddyfile.ServerType{}}
	adapted, _, err := adapter.Adapt(input, nil)
	if err != nil {
		t.Fatalf("caddy.Adapt() error = %v", err)
	}

	json := string(adapted)
	if !strings.Contains(json, `"handler":"agent_route_dispatcher"`) {
		t.Fatalf("adapted config missing agent_route_dispatcher handler: %s", json)
	}
	if !strings.Contains(json, `"api_handlers":{"anthropic":{}`) || !strings.Contains(json, `"openai":{}`) {
		t.Fatalf("adapted config missing dispatcher api handlers: %s", json)
	}
	if !strings.Contains(json, `"mcp":true`) {
		t.Fatalf("adapted config missing mcp flag: %s", json)
	}
}

func TestParseAgentRouteDispatcherAllowsMCPOnly(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
	agent_route_dispatcher {
		mcp
	}
	`)

	handler, err := parseAgentRouteDispatcher(httpcaddyfile.Helper{Dispenser: d})
	if err != nil {
		t.Fatalf("parseAgentRouteDispatcher() error = %v", err)
	}

	dispatcherHandler, ok := handler.(*AgentRouteDispatcher)
	if !ok {
		t.Fatalf("handler type = %T, want *AgentRouteDispatcher", handler)
	}
	if !dispatcherHandler.EnableMCP {
		t.Fatal("expected mcp to be enabled")
	}
	if len(dispatcherHandler.APIHandlersRaw) != 0 {
		t.Fatalf("api handler count = %d, want 0", len(dispatcherHandler.APIHandlersRaw))
	}
}
