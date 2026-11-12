package mcp

import (
	"context"
	"fmt"
	"net/http"

	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

const ProtocolVersion = "2025-11-25"

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

func initializeUpstream(ctx context.Context, client transport.Caller) (initializeResult, error) {
	reply, err := client.Call(ctx, &transport.Message{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": ProtocolVersion,
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
		return initializeResult{}, fmt.Errorf("MCP upstream returned no initialize response")
	}
	if reply.Error != nil {
		return initializeResult{}, fmt.Errorf("MCP upstream initialize failed: %s", reply.Error.Message)
	}

	var result initializeResult
	if err := decodeResult(reply.Result, &result); err != nil {
		return initializeResult{}, fmt.Errorf("decode initialize result: %w", err)
	}
	negotiated := result.ProtocolVersion
	if negotiated == "" {
		negotiated = ProtocolVersion
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

func applyAuth(client headerSetter, auth *AuthConfig) {
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
