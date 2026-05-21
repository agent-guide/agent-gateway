package service

import (
	"context"
	"fmt"
	"net/http"

	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

// applyServiceAuth injects authentication headers into HTTP-based transports.
// It is a no-op for non-HTTP transports (e.g. stdio), which handle auth via env vars.

const latestProtocolVersion = "2025-11-25"

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities    any    `json:"capabilities,omitempty"`
	ServerInfo      any    `json:"serverInfo,omitempty"`
	Instructions    any    `json:"instructions,omitempty"`
}

type listToolsResult struct {
	Tools      []basemcp.Tool `json:"tools"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type listResourcesResult struct {
	Resources  []basemcp.Resource `json:"resources"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

type listResourceTemplatesResult struct {
	ResourceTemplates []basemcp.ResourceTemplate `json:"resourceTemplates"`
	NextCursor        string                     `json:"nextCursor,omitempty"`
}

type listPromptsResult struct {
	Prompts    []basemcp.Prompt `json:"prompts"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

func (m *Manager) ListTools(ctx context.Context, id string) ([]basemcp.Tool, error) {
	result, err := m.ListToolsPage(ctx, id, "")
	if err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (m *Manager) ListToolsPage(ctx context.Context, id string, cursor string) (*basemcp.ToolListResult, error) {
	var result listToolsResult
	if err := m.callMethodWithRetry(ctx, id, "tools/list", paginationParams(cursor), &result, nil); err != nil {
		return nil, err
	}
	return &basemcp.ToolListResult{Tools: result.Tools, NextCursor: result.NextCursor}, nil
}

func (m *Manager) ListResources(ctx context.Context, id string) ([]basemcp.Resource, error) {
	result, err := m.ListResourcesPage(ctx, id, "")
	if err != nil {
		return nil, err
	}
	return result.Resources, nil
}

func (m *Manager) ListResourcesPage(ctx context.Context, id string, cursor string) (*basemcp.ResourceListResult, error) {
	var result listResourcesResult
	if err := m.callMethodWithRetry(ctx, id, "resources/list", paginationParams(cursor), &result, nil); err != nil {
		return nil, err
	}
	return &basemcp.ResourceListResult{Resources: result.Resources, NextCursor: result.NextCursor}, nil
}

func (m *Manager) ListResourceTemplates(ctx context.Context, id string) ([]basemcp.ResourceTemplate, error) {
	result, err := m.ListResourceTemplatesPage(ctx, id, "")
	if err != nil {
		return nil, err
	}
	return result.ResourceTemplates, nil
}

func (m *Manager) ListResourceTemplatesPage(ctx context.Context, id string, cursor string) (*basemcp.ResourceTemplateListResult, error) {
	var result listResourceTemplatesResult
	if err := m.callMethodWithRetry(ctx, id, "resources/templates/list", paginationParams(cursor), &result, nil); err != nil {
		return nil, err
	}
	return &basemcp.ResourceTemplateListResult{
		ResourceTemplates: result.ResourceTemplates,
		NextCursor:        result.NextCursor,
	}, nil
}

func (m *Manager) ListPrompts(ctx context.Context, id string) ([]basemcp.Prompt, error) {
	result, err := m.ListPromptsPage(ctx, id, "")
	if err != nil {
		return nil, err
	}
	return result.Prompts, nil
}

func (m *Manager) ListPromptsPage(ctx context.Context, id string, cursor string) (*basemcp.PromptListResult, error) {
	var result listPromptsResult
	if err := m.callMethodWithRetry(ctx, id, "prompts/list", paginationParams(cursor), &result, nil); err != nil {
		return nil, err
	}
	return &basemcp.PromptListResult{Prompts: result.Prompts, NextCursor: result.NextCursor}, nil
}

func initializeService(ctx context.Context, client transport.Caller) (initializeResult, error) {
	reply, err := client.Call(ctx, &transport.Message{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": latestProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "agent-gateway",
				"version": "dev",
			},
		},
	})
	if err != nil {
		return initializeResult{}, err
	}
	if reply == nil {
		return initializeResult{}, fmt.Errorf("mcp service returned no initialize response")
	}
	if reply.Error != nil {
		return initializeResult{}, fmt.Errorf("mcp service initialize failed: %s", reply.Error.Message)
	}

	var result initializeResult
	if err := decodeResult(reply.Result, &result); err != nil {
		return initializeResult{}, fmt.Errorf("decode initialize result: %w", err)
	}
	negotiated := result.ProtocolVersion
	if negotiated == "" {
		negotiated = latestProtocolVersion
		result.ProtocolVersion = negotiated
	}
	// Set the negotiated protocol version as a persistent header on transports
	// that support HTTP header injection (e.g. StreamableHTTPTransport).
	type headerSetter interface{ SetHeader(key, value string) }
	if hs, ok := client.(headerSetter); ok {
		hs.SetHeader("MCP-Protocol-Version", negotiated)
	}

	if _, err := client.Call(ctx, &transport.Message{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}); err != nil {
		return initializeResult{}, fmt.Errorf("send initialized notification: %w", err)
	}
	return result, nil
}

func decodeResult(src any, dest any) error {
	data, err := basemcp.MarshalAny(src)
	if err != nil {
		return err
	}
	return basemcp.UnmarshalAny(data, dest)
}

type headerSetter interface {
	SetHeader(key, value string)
}

func applyServiceAuth(client headerSetter, auth *AuthConfig) {
	if client == nil || auth == nil {
		return
	}
	switch auth.Type {
	case "", "api_key", "bearer", "oauth2":
		if auth.APIKey != "" {
			client.SetHeader("Authorization", "Bearer "+auth.APIKey)
		}
	case "basic":
		req, _ := http.NewRequest(http.MethodGet, "http://localhost", nil)
		req.SetBasicAuth(auth.Username, auth.Password)
		if value := req.Header.Get("Authorization"); value != "" {
			client.SetHeader("Authorization", value)
		}
	}
}

func paginationParams(cursor string) map[string]any {
	if cursor == "" {
		return map[string]any{}
	}
	return map[string]any{"cursor": cursor}
}
