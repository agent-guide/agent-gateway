package connector

import (
	"context"

	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
)

func (m *Manager) CallTool(ctx context.Context, id, name string, args map[string]any, progressCh chan<- connectormcp.UpstreamProgress) (*basemcp.ToolResult, error) {
	lease, err := m.Acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	defer lease.Close()
	call := cr.ToolCall{Name: name, Arguments: args}
	if progressCh != nil {
		call.Progress = func(p cr.Progress) {
			select {
			case progressCh <- connectormcp.UpstreamProgress{ProgressToken: p.ProgressToken, Progress: p.Progress, Total: p.Total, Message: p.Message}:
			default:
			}
		}
	}
	out, err := lease.CallTool(ctx, call)
	return out.Result, err
}

func (m *Manager) ReadResource(ctx context.Context, id, uri string, progress chan<- connectormcp.UpstreamProgress) (*basemcp.ResourceReadResult, error) {
	v, ctx, done, err := m.admit(ctx, id, "")
	if err != nil {
		return nil, err
	}
	defer done()
	return m.mcpEngine.ReadResource(ctx, v.mcpTarget, uri, progress)
}
func (m *Manager) GetPrompt(ctx context.Context, id, name string, args map[string]any, progress chan<- connectormcp.UpstreamProgress) (*basemcp.PromptResult, error) {
	v, ctx, done, err := m.admit(ctx, id, "")
	if err != nil {
		return nil, err
	}
	defer done()
	return m.mcpEngine.GetPrompt(ctx, v.mcpTarget, name, args, progress)
}
func (m *Manager) Complete(ctx context.Context, id string, ref basemcp.CompletionReference, argument basemcp.CompletionArgument, args map[string]string) (*basemcp.CompletionResult, error) {
	v, ctx, done, err := m.admit(ctx, id, "")
	if err != nil {
		return nil, err
	}
	defer done()
	return m.mcpEngine.Complete(ctx, v.mcpTarget, ref, argument, args)
}
