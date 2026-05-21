package service

import (
	"context"
	"fmt"
	"strings"

	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

// UpstreamProgress carries a parsed notifications/progress message from an upstream.
type UpstreamProgress struct {
	ProgressToken any
	Progress      float64
	Total         *float64
	Message       string
}

type readResourceResult struct {
	Contents []basemcp.ResourceContent `json:"contents"`
}

func (m *Manager) CallTool(ctx context.Context, id string, name string, args map[string]any, progressCh chan<- UpstreamProgress) (*basemcp.ToolResult, error) {
	if name == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	var result basemcp.ToolResult
	if err := m.callMethodWithRetry(ctx, id, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	}, &result, progressCh); err != nil {
		return nil, err
	}
	return &result, nil
}

func (m *Manager) ReadResource(ctx context.Context, id string, uri string, progressCh chan<- UpstreamProgress) (*basemcp.ResourceReadResult, error) {
	if uri == "" {
		return nil, fmt.Errorf("resource uri is required")
	}
	var result readResourceResult
	if err := m.callMethodWithRetry(ctx, id, "resources/read", map[string]any{
		"uri": uri,
	}, &result, progressCh); err != nil {
		return nil, err
	}
	return &basemcp.ResourceReadResult{Contents: result.Contents}, nil
}

func (m *Manager) GetPrompt(ctx context.Context, id string, name string, args map[string]any, progressCh chan<- UpstreamProgress) (*basemcp.PromptResult, error) {
	if name == "" {
		return nil, fmt.Errorf("prompt name is required")
	}
	var result basemcp.PromptResult
	if err := m.callMethodWithRetry(ctx, id, "prompts/get", map[string]any{
		"name":      name,
		"arguments": args,
	}, &result, progressCh); err != nil {
		return nil, err
	}
	return &result, nil
}

func (m *Manager) Complete(ctx context.Context, id string, ref basemcp.CompletionReference, argument basemcp.CompletionArgument, args map[string]string) (*basemcp.CompletionResult, error) {
	if ref.Type == "" {
		return nil, fmt.Errorf("completion ref type is required")
	}
	if ref.Type == "ref/prompt" && ref.Name == "" {
		return nil, fmt.Errorf("completion prompt ref name is required")
	}
	if ref.Type == "ref/resource" && ref.URI == "" {
		return nil, fmt.Errorf("completion resource ref uri is required")
	}
	if argument.Name == "" {
		return nil, fmt.Errorf("completion argument name is required")
	}
	var result basemcp.CompletionResult
	params := map[string]any{
		"ref": ref,
		"argument": map[string]any{
			"name":  argument.Name,
			"value": argument.Value,
		},
	}
	if len(args) > 0 {
		params["arguments"] = args
	}
	if err := m.callMethodWithRetry(ctx, id, "completion/complete", params, &result, nil); err != nil {
		return nil, err
	}
	return &result, nil
}

func (m *Manager) callMethodWithRetry(ctx context.Context, id string, method string, params map[string]any, dest any, progressCh chan<- UpstreamProgress) error {
	client, reused, err := m.discoveryClient(ctx, id)
	if err != nil {
		return err
	}
	if err := callMethod(ctx, client, method, params, dest, progressCh); err == nil {
		return nil
	} else if !reused {
		return err
	}

	// Reused session failed — invalidate and reconnect once.
	m.invalidateDiscoverySession(id)
	client, _, err = m.discoveryClient(ctx, id)
	if err != nil {
		m.setGatewaySessionState(id, SessionStateError)
		return err
	}
	if err := callMethod(ctx, client, method, params, dest, progressCh); err != nil {
		m.setGatewaySessionState(id, SessionStateError)
		return err
	}
	return nil
}

func callMethod(ctx context.Context, client transport.Caller, method string, params map[string]any, dest any, progressCh chan<- UpstreamProgress) error {
	msg := &transport.Message{
		JSONRPC: "2.0",
		ID:      3,
		Method:  method,
		Params:  params,
	}

	var reply *transport.Message
	var err error
	if pc, ok := client.(transport.ProgressCaller); ok && progressCh != nil {
		reply, err = pc.CallWithProgress(ctx, msg, func(notification *transport.Message) {
			if n := parseUpstreamProgress(notification); n != nil {
				select {
				case progressCh <- *n:
				default:
				}
			}
		})
	} else {
		reply, err = client.Call(ctx, msg)
	}
	if err != nil {
		return err
	}
	if reply == nil {
		return fmt.Errorf("mcp service returned no response for %s", method)
	}
	if reply.Error != nil {
		return fmt.Errorf("mcp service %s failed: %s", method, reply.Error.Message)
	}
	if dest == nil {
		return nil
	}
	if err := decodeResult(reply.Result, dest); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}

func parseUpstreamProgress(msg *transport.Message) *UpstreamProgress {
	if msg == nil || strings.TrimSpace(msg.Method) != "notifications/progress" {
		return nil
	}
	params, ok := msg.Params.(map[string]any)
	if !ok || params == nil {
		return nil
	}
	token := params["progressToken"]
	if token == nil {
		return nil
	}
	progress, _ := params["progress"].(float64)
	var total *float64
	if t, ok := params["total"].(float64); ok {
		total = &t
	}
	message, _ := params["message"].(string)
	return &UpstreamProgress{
		ProgressToken: token,
		Progress:      progress,
		Total:         total,
		Message:       message,
	}
}
