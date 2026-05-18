package service

import (
	"context"
	"fmt"

	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

type readResourceResult struct {
	Contents []basemcp.ResourceContent `json:"contents"`
}

func (m *Manager) CallTool(ctx context.Context, id string, name string, args map[string]any) (*basemcp.ToolResult, error) {
	if name == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	var result basemcp.ToolResult
	if err := m.callMethodWithRetry(ctx, id, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (m *Manager) ReadResource(ctx context.Context, id string, uri string) (*basemcp.ResourceReadResult, error) {
	if uri == "" {
		return nil, fmt.Errorf("resource uri is required")
	}
	var result readResourceResult
	if err := m.callMethodWithRetry(ctx, id, "resources/read", map[string]any{
		"uri": uri,
	}, &result); err != nil {
		return nil, err
	}
	return &basemcp.ResourceReadResult{Contents: result.Contents}, nil
}

func (m *Manager) GetPrompt(ctx context.Context, id string, name string, args map[string]any) (*basemcp.PromptResult, error) {
	if name == "" {
		return nil, fmt.Errorf("prompt name is required")
	}
	var result basemcp.PromptResult
	if err := m.callMethodWithRetry(ctx, id, "prompts/get", map[string]any{
		"name":      name,
		"arguments": args,
	}, &result); err != nil {
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
	if err := m.callMethodWithRetry(ctx, id, "completion/complete", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (m *Manager) callMethodWithRetry(ctx context.Context, id string, method string, params map[string]any, dest any) error {
	client, reused, err := m.discoveryClient(ctx, id)
	if err != nil {
		return err
	}
	if err := callMethod(ctx, client, method, params, dest); err == nil {
		return nil
	} else if !reused {
		return err
	}

	m.invalidateDiscoverySession(id)
	client, _, err = m.discoveryClient(ctx, id)
	if err != nil {
		return err
	}
	return callMethod(ctx, client, method, params, dest)
}

func callMethod(ctx context.Context, client *transport.StreamableHTTPTransport, method string, params map[string]any, dest any) error {
	reply, err := client.Do(ctx, &transport.Message{
		JSONRPC: "2.0",
		ID:      3,
		Method:  method,
		Params:  params,
	})
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
