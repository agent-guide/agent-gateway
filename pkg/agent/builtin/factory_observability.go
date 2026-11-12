package builtin

import (
	"context"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	mcpservice "github.com/agent-guide/agent-gateway/pkg/connector"
	"github.com/agent-guide/agent-gateway/pkg/connector/einotool"
	"github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/cloudwego/eino/components/model"
)

// Factories receive the same per-call observation as declarative nodes. Wrap
// dependencies rather than the resulting graph, whose internal calls are opaque.
func (h *Host) factoryDeps(agentID string) FactoryDeps {
	if h.observer == nil {
		return FactoryDeps{Models: h.models, Tools: h.tools}
	}
	deps := FactoryDeps{}
	if h.models != nil {
		deps.Models = factoryModels{h.models, h.observer, agentID}
	}
	if h.tools != nil {
		deps.Tools = factoryTools{h.tools, h.observer, agentID}
	}
	return deps
}

type factoryModels struct {
	inner    ChatModelResolver
	observer usage.InteractionObserver
	agentID  string
}

func (r factoryModels) ResolveChatModel(ctx context.Context, routeID, name string, requireTools bool) (model.ToolCallingChatModel, error) {
	m, err := r.inner.ResolveChatModel(ctx, routeID, name, requireTools)
	if err != nil {
		return nil, err
	}
	return newObservedModel(m, r.observer, routeID, r.agentID), nil
}

type factoryTools struct {
	einotool.ToolCaller
	observer usage.InteractionObserver
	agentID  string
}

func (t factoryTools) CallTool(ctx context.Context, id, name string, args map[string]any, progress chan<- mcpservice.UpstreamProgress) (*mcp.ToolResult, error) {
	span, ctx := t.observer.Begin(ctx, childDims(ctx, "mcp", "", t.agentID))
	span.SetExtension(usage.MCPExtension{ServiceID: id, Method: "tools/call", ToolName: name})
	result, err := t.ToolCaller.CallTool(ctx, id, name, args, progress)
	finishFactoryTool(span, result, err)
	return result, err
}

func finishFactoryTool(span usage.InteractionSpan, result *mcp.ToolResult, err error) {
	failed := err != nil || (result != nil && result.IsError)
	if failed {
		span.AddAnnotation("error_type", "tool_call_failed")
		span.SetExtension(usage.MCPExtension{ResultStatus: "error"})
		span.Finish(usage.InteractionOutcome{Success: false, StatusCode: 502})
		return
	}
	span.SetExtension(usage.MCPExtension{ResultStatus: "success"})
	span.Finish(usage.InteractionOutcome{Success: true, StatusCode: 200})
}
