package builtin

import (
	"context"

	"io"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
)

// Inner LLM and MCP calls of a builtin turn get their own child interaction
// spans so they produce llm_usage_events / mcp_usage_events exactly like
// route-dispatched traffic, parented under the turn span and stamped with the
// agent id (§5.7.6). routeProtocol marks them as host-internal traffic.
const builtinInternalProtocol = "builtin"

func childDims(ctx context.Context, kind, routeID, agentID string) usage.InteractionDimensions {
	dims := usage.InteractionDimensions{
		RouteID:       routeID,
		RouteKind:     kind,
		RouteProtocol: builtinInternalProtocol,
		AgentID:       agentID,
	}
	if parent, ok := usage.DimensionsFromContext(ctx); ok {
		dims.TraceID = parent.TraceID
		dims.ParentSpanID = parent.SpanID
		dims.AgentDepth = parent.AgentDepth
		dims.VirtualKeyID = parent.VirtualKeyID
		dims.RuntimeType = parent.RuntimeType
		dims.RunID = parent.RunID
	}
	return dims
}

// observedModel begins a child "llm" span around every model call. The
// RoutedProvider underneath folds provider, credential, and token detail into
// that span through the usual SetExtension path.
type observedModel struct {
	inner    einomodel.ToolCallingChatModel
	observer usage.InteractionObserver
	routeID  string
	agentID  string
}

func newObservedModel(inner einomodel.ToolCallingChatModel, observer usage.InteractionObserver, routeID, agentID string) einomodel.ToolCallingChatModel {
	if observer == nil {
		return inner
	}
	return &observedModel{inner: inner, observer: observer, routeID: routeID, agentID: agentID}
}

func (m *observedModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	span, cctx := m.observer.Begin(ctx, childDims(ctx, "llm", m.routeID, m.agentID))
	out, err := m.inner.Generate(cctx, input, opts...)
	if err != nil {
		span.AddAnnotation("error_type", "provider_request_failed")
		span.Finish(usage.InteractionOutcome{Success: false, StatusCode: 502})
		return nil, err
	}
	var tokens modelTokenUsage
	tokens.add(out)
	span.SetExtension(tokens.extension())
	span.Finish(usage.InteractionOutcome{Success: true, StatusCode: 200})
	return out, nil
}

func (m *observedModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	span, cctx := m.observer.Begin(ctx, childDims(ctx, "llm", m.routeID, m.agentID))
	stream, err := m.inner.Stream(cctx, input, opts...)
	if err != nil {
		span.AddAnnotation("error_type", "provider_stream_failed")
		span.Finish(usage.InteractionOutcome{Success: false, StatusCode: 502})
		return nil, err
	}
	// Streaming usage arrives on the final chunk's ResponseMeta; relay chunks
	// through a pipe so the span can capture it and finish when the stream
	// drains. The pipe goroutine exits when either side closes.
	sr, sw := schema.Pipe[*schema.Message](8)
	go func() {
		defer stream.Close()
		defer sw.Close()
		var tokens modelTokenUsage
		success := true
		for {
			msg, recvErr := stream.Recv()
			if recvErr == io.EOF {
				break
			}
			if recvErr != nil {
				success = false
				sw.Send(nil, recvErr)
				break
			}
			tokens.add(msg)
			if closed := sw.Send(msg, nil); closed {
				break
			}
		}
		span.SetExtension(tokens.extension())
		if success {
			span.Finish(usage.InteractionOutcome{Success: true, StatusCode: 200})
		} else {
			span.AddAnnotation("error_type", "provider_stream_failed")
			span.Finish(usage.InteractionOutcome{Success: false, StatusCode: 502})
		}
	}()
	return sr, nil
}

// Stream usage fields are cumulative snapshots and may arrive separately (for
// example, Anthropic input at message_start and output at message_delta).
// Keep each field's high water mark; summing snapshots would double-count.
type modelTokenUsage struct {
	input, output, total, cached, reasoning int
	seen                                    bool
}

func (u *modelTokenUsage) add(msg *schema.Message) {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return
	}
	v := msg.ResponseMeta.Usage
	u.seen = true
	u.input = max(u.input, v.PromptTokens)
	u.output = max(u.output, v.CompletionTokens)
	u.total = max(u.total, v.TotalTokens, u.input+u.output)
	u.cached = max(u.cached, v.PromptTokenDetails.CachedTokens)
	u.reasoning = max(u.reasoning, v.CompletionTokensDetails.ReasoningTokens)
}

func (u modelTokenUsage) extension() usage.LLMExtension {
	if !u.seen {
		return usage.LLMExtension{}
	}
	return usage.LLMExtension{
		InputTokens: usage.Int(u.input), OutputTokens: usage.Int(u.output), TotalTokens: usage.Int(u.total),
		CachedTokens: usage.Int(u.cached), ReasoningTokens: usage.Int(u.reasoning),
		UsageFinalized: usage.Bool(u.total > 0),
	}
}

func (m *observedModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &observedModel{inner: bound, observer: m.observer, routeID: m.routeID, agentID: m.agentID}, nil
}

// observedTool begins a child "mcp" span around every tool execution.
type observedTool struct {
	inner     tool.InvokableTool
	observer  usage.InteractionObserver
	serviceID string
	toolName  string
	agentID   string
}

func newObservedTool(inner tool.BaseTool, observer usage.InteractionObserver, serviceID, toolName, agentID string) tool.BaseTool {
	invokable, ok := inner.(tool.InvokableTool)
	if !ok || observer == nil {
		return inner
	}
	return &observedTool{inner: invokable, observer: observer, serviceID: serviceID, toolName: toolName, agentID: agentID}
}

func (t *observedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.inner.Info(ctx)
}

func (t *observedTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	span, cctx := t.observer.Begin(ctx, childDims(ctx, "mcp", "", t.agentID))
	span.SetExtension(usage.MCPExtension{
		ServiceID: t.serviceID,
		Method:    "tools/call",
		ToolName:  t.toolName,
	})
	out, err := t.inner.InvokableRun(cctx, argumentsInJSON, opts...)
	if err != nil {
		span.SetExtension(usage.MCPExtension{ResultStatus: "error"})
		span.AddAnnotation("error_type", "tool_call_failed")
		span.Finish(usage.InteractionOutcome{Success: false, StatusCode: 502})
		return "", err
	}
	span.SetExtension(usage.MCPExtension{ResultStatus: "success"})
	span.Finish(usage.InteractionOutcome{Success: true, StatusCode: 200})
	return out, nil
}
