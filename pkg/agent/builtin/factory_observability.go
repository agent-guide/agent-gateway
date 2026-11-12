package builtin

import (
	"context"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
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
	cr.Resolver
	observer usage.InteractionObserver
	agentID  string
}

func (t factoryTools) Acquire(ctx context.Context, id string) (cr.Lease, error) {
	lease, err := t.Resolver.Acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	return factoryLease{Lease: lease, observer: t.observer, agentID: t.agentID}, nil
}

type factoryLease struct {
	cr.Lease
	observer usage.InteractionObserver
	agentID  string
}

func (t factoryLease) CallTool(ctx context.Context, call cr.ToolCall) (cr.CallOutcome, error) {
	descriptor := t.Descriptor()
	span, cctx := t.observer.Begin(ctx, childDims(ctx, "mcp", "", t.agentID))
	span.SetExtension(usage.MCPExtension{ServiceID: descriptor.ConnectorID, ConnectorType: descriptor.Type, Method: "tools/call", ToolName: call.Name})
	outcome, err := t.Lease.CallTool(cctx, call)
	span.SetExtension(usage.MCPExtension{HTTPExecution: outcome.HTTP})
	failed := err != nil || (outcome.Result != nil && outcome.Result.IsError) || outcome.Failure != nil
	if failed {
		category := "tool_call_failed"
		if outcome.Failure != nil {
			category = outcome.Failure.Category
		}
		span.AddAnnotation("error_type", category)
		span.SetExtension(usage.MCPExtension{ResultStatus: "error"})
		span.Finish(usage.InteractionOutcome{Success: false, StatusCode: 502})
	} else {
		span.SetExtension(usage.MCPExtension{ResultStatus: "success"})
		span.Finish(usage.InteractionOutcome{Success: true, StatusCode: 200})
	}
	return outcome, err
}
