package builtin

import (
	"context"
	"testing"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/connector/einotool"
	"github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func TestCustomFactoryProducesChildUsage(t *testing.T) {
	name := t.Name()
	err := RegisterFactory(name, func(ctx context.Context, deps FactoryDeps, def *agent.BuiltinRuntime) (adk.Agent, error) {
		model, err := deps.Models.ResolveChatModel(ctx, def.Model.LLMRouteID, def.Model.Model, true)
		if err != nil {
			return nil, err
		}
		tools, err := einotool.Tools(ctx, deps.Tools, "service", "lookup")
		if err != nil {
			return nil, err
		}
		return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "custom", Model: model, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { factoryMu.Lock(); delete(factories, name); factoryMu.Unlock() })
	model := &fakeChatModel{script: func(input []*schema.Message) *schema.Message {
		if input[len(input)-1].Role != schema.Tool {
			return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call", Type: "function", Function: schema.FunctionCall{Name: "lookup", Arguments: "{}"}}}, ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}}}
		}
		return &schema.Message{Role: schema.Assistant, Content: "done", ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 20, CompletionTokens: 3, TotalTokens: 23}}}
	}}
	a := testAgent("custom-agent")
	a.Runtime.Builtin.Topology = agent.BuiltinTopology{Kind: agent.TopologyKindCustom, Factory: name}
	a.Resources.ConnectorIDs = []string{"service"}
	events := &usage.InMemorySink{}
	observer := usage.NewObserver(events)
	host := NewHost(Config{Agents: &fakeAgentSource{agents: map[string]agent.Agent{a.ID: a}}, Models: &fakeModelResolver{model: model}, Tools: &fakeToolCaller{tools: []mcp.Tool{{Name: "lookup"}}, result: &mcp.ToolResult{}}, Observer: observer})
	parent, ctx := observer.Begin(t.Context(), usage.InteractionDimensions{RouteKind: "agent", RuntimeType: "builtin", AgentID: a.ID, RunID: "run"})
	if err := host.ServeTurn(ctx, a.ID, TurnRequest{Input: "hi"}, (&collectedEvents{}).sink); err != nil {
		t.Fatal(err)
	}
	parent.Finish(usage.InteractionOutcome{Success: true, StatusCode: 200})
	var llm, mcpCount, total int
	for _, raw := range events.Events {
		switch ev := raw.(type) {
		case usage.LLMUsageEvent:
			llm++
			total += ev.TotalTokens
			if ev.AgentID != a.ID || ev.RunID == "" || ev.ParentSpanID == "" || ev.RouteID != a.Runtime.Builtin.Model.LLMRouteID {
				t.Fatalf("LLM correlation = %+v", ev)
			}
		case usage.MCPUsageEvent:
			mcpCount++
			if ev.AgentID != a.ID || ev.RunID == "" || ev.ParentSpanID == "" || ev.ServiceID != "service" || ev.ToolName != "lookup" {
				t.Fatalf("tool correlation = %+v", ev)
			}
		}
	}
	if llm != 2 || mcpCount != 1 || total != 35 {
		t.Fatalf("LLM=%d MCP=%d tokens=%d", llm, mcpCount, total)
	}
}
