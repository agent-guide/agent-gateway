package builtin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/connector/einotool"
	"github.com/agent-guide/agent-gateway/pkg/connector/execution"
	"github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
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
	host := NewHost(Config{Agents: &fakeAgentSource{agents: map[string]agent.Agent{a.ID: a}}, Models: &fakeModelResolver{model: model}, Tools: &fakeToolResolver{tools: []mcp.Tool{{Name: "lookup"}}, result: &mcp.ToolResult{}}, Observer: observer})
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

type factoryTestResolver struct{ lease execution.Lease }

func (r factoryTestResolver) Acquire(context.Context, string) (execution.Lease, error) {
	return r.lease, nil
}

type factoryTestLease struct {
	outcome execution.CallOutcome
	revoked bool
	closed  bool
}

func (*factoryTestLease) Descriptor() execution.Descriptor {
	return execution.Descriptor{ConnectorID: "orders", Type: "http_api", Generation: "generation-1", Incarnation: "incarnation-1"}
}
func (*factoryTestLease) ListTools(context.Context) (execution.ToolCatalog, error) {
	return execution.ToolCatalog{Generation: "catalog-1", Tools: []mcp.Tool{{Name: "lookup"}}}, nil
}
func (l *factoryTestLease) Close() error { l.closed = true; return nil }
func (l *factoryTestLease) CallTool(context.Context, execution.ToolCall) (execution.CallOutcome, error) {
	if l.revoked {
		return execution.CallOutcome{}, &execution.CallError{Category: "generation_unavailable", Message: "retired"}
	}
	return l.outcome, nil
}
func TestFactoryLeasePreservesOutcomeAndRevocation(t *testing.T) {
	source := &factoryTestLease{outcome: execution.CallOutcome{Result: &mcp.ToolResult{IsError: true}, HTTP: &execution.HTTPExecution{Method: "POST"}, Failure: &execution.ExecutionFailure{Category: "timeout", OutcomeUncertain: true}}}
	events := &usage.InMemorySink{}
	wrapper := factoryTools{Resolver: factoryTestResolver{source}, observer: usage.NewObserver(events), agentID: "agent"}
	lease, err := wrapper.Acquire(t.Context(), "orders")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Descriptor() != source.Descriptor() {
		t.Fatal("descriptor identity lost")
	}
	catalog, err := lease.ListTools(t.Context())
	if err != nil || catalog.Generation != "catalog-1" {
		t.Fatalf("catalog identity lost: %#v %v", catalog, err)
	}
	out, err := lease.CallTool(t.Context(), execution.ToolCall{Name: "lookup"})
	if err != nil || out.Result != source.outcome.Result || out.HTTP != source.outcome.HTTP || out.Failure != source.outcome.Failure {
		t.Fatalf("outcome lost: %#v %v", out, err)
	}
	if len(events.Events) != 1 {
		t.Fatalf("duplicate/missing usage: %d", len(events.Events))
	}
	event := events.Events[0].(usage.MCPUsageEvent)
	if event.ConnectorType != "http_api" || !reflect.DeepEqual(event.HTTPExecution, out.HTTP) || event.ServiceID != "orders" || event.ResultStatus != "error" {
		t.Fatalf("observation lost: %#v", event)
	}
	source.revoked = true
	_, err = lease.CallTool(t.Context(), execution.ToolCall{Name: "lookup"})
	var callErr *execution.CallError
	if !errors.As(err, &callErr) || callErr.Category != "generation_unavailable" {
		t.Fatalf("revoked lease executed: %v", err)
	}
	if err = lease.Close(); err != nil || !source.closed {
		t.Fatalf("close not delegated: %v", err)
	}
}

func TestObservedToolPreservesHTTPFailureCategory(t *testing.T) {
	for _, category := range []string{"timeout", "cancelled", "egress_denied", "response_mapping", "network_error"} {
		t.Run(category, func(t *testing.T) {
			source := &factoryTestLease{outcome: execution.CallOutcome{
				Result:  &mcp.ToolResult{IsError: true, Content: []any{map[string]any{"type": "text", "text": "write outcome is unconfirmed; do not automatically retry"}}},
				HTTP:    &execution.HTTPExecution{Method: "POST"},
				Failure: &execution.ExecutionFailure{Category: category, OutcomeUncertain: true},
			}}
			tools, err := einotool.Tools(t.Context(), factoryTestResolver{source}, "orders", "lookup")
			if err != nil {
				t.Fatal(err)
			}
			events := &usage.InMemorySink{}
			observed := newObservedTool(tools[0], usage.NewObserver(events), "orders", "lookup", "agent").(tool.InvokableTool)
			text, err := observed.InvokableRun(t.Context(), "{}")
			if err != nil || text != "write outcome is unconfirmed; do not automatically retry" {
				t.Fatalf("tool failure content lost: text=%q err=%v", text, err)
			}
			if len(events.Events) != 1 {
				t.Fatalf("events=%d", len(events.Events))
			}
			event := events.Events[0].(usage.MCPUsageEvent)
			if event.ErrorType != category || event.Success || event.ResultStatus != "error" || !reflect.DeepEqual(event.HTTPExecution, source.outcome.HTTP) {
				t.Fatalf("HTTP failure metadata lost: %+v", event)
			}
		})
	}
}
