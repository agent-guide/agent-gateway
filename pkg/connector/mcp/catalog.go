package mcp

import (
	"context"
	"fmt"

	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
)

const (
	maxToolCatalogPages = 100
	maxToolCatalogTools = 10000
)

// ListTools builds a complete internal catalog. Ingress uses ListToolsPage to
// preserve the client's pagination cursor and single-page response contract.
func (m *Engine) ListTools(ctx context.Context, target *Target) ([]basemcp.Tool, error) {
	var tools []basemcp.Tool
	cursor := ""
	seen := map[string]bool{"": true}
	for page := 0; page < maxToolCatalogPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := m.ListToolsPage(ctx, target, cursor)
		if err != nil {
			return nil, err
		}
		if len(result.Tools) > maxToolCatalogTools-len(tools) {
			return nil, fmt.Errorf("MCP tool catalog exceeds %d tools", maxToolCatalogTools)
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			return tools, nil
		}
		if seen[result.NextCursor] {
			return nil, fmt.Errorf("MCP tool catalog repeated pagination cursor")
		}
		cursor = result.NextCursor
		seen[cursor] = true
	}
	return nil, fmt.Errorf("MCP tool catalog exceeds %d pages", maxToolCatalogPages)
}

func (m *Engine) ListToolsPage(ctx context.Context, target *Target, cursor string) (*basemcp.ToolListResult, error) {
	var result listToolsResult
	if err := m.callMethodWithRetry(ctx, target, "tools/list", paginationParams(cursor), &result, nil); err != nil {
		return nil, err
	}
	return &basemcp.ToolListResult{Tools: result.Tools, NextCursor: result.NextCursor}, nil
}

func (m *Engine) ListResources(ctx context.Context, target *Target) ([]basemcp.Resource, error) {
	result, err := m.ListResourcesPage(ctx, target, "")
	if err != nil {
		return nil, err
	}
	return result.Resources, nil
}

func (m *Engine) ListResourcesPage(ctx context.Context, target *Target, cursor string) (*basemcp.ResourceListResult, error) {
	var result listResourcesResult
	if err := m.callMethodWithRetry(ctx, target, "resources/list", paginationParams(cursor), &result, nil); err != nil {
		return nil, err
	}
	return &basemcp.ResourceListResult{Resources: result.Resources, NextCursor: result.NextCursor}, nil
}

func (m *Engine) ListResourceTemplates(ctx context.Context, target *Target) ([]basemcp.ResourceTemplate, error) {
	result, err := m.ListResourceTemplatesPage(ctx, target, "")
	if err != nil {
		return nil, err
	}
	return result.ResourceTemplates, nil
}

func (m *Engine) ListResourceTemplatesPage(ctx context.Context, target *Target, cursor string) (*basemcp.ResourceTemplateListResult, error) {
	var result listResourceTemplatesResult
	if err := m.callMethodWithRetry(ctx, target, "resources/templates/list", paginationParams(cursor), &result, nil); err != nil {
		return nil, err
	}
	return &basemcp.ResourceTemplateListResult{
		ResourceTemplates: result.ResourceTemplates,
		NextCursor:        result.NextCursor,
	}, nil
}

func (m *Engine) ListPrompts(ctx context.Context, target *Target) ([]basemcp.Prompt, error) {
	result, err := m.ListPromptsPage(ctx, target, "")
	if err != nil {
		return nil, err
	}
	return result.Prompts, nil
}

func (m *Engine) ListPromptsPage(ctx context.Context, target *Target, cursor string) (*basemcp.PromptListResult, error) {
	var result listPromptsResult
	if err := m.callMethodWithRetry(ctx, target, "prompts/list", paginationParams(cursor), &result, nil); err != nil {
		return nil, err
	}
	return &basemcp.PromptListResult{Prompts: result.Prompts, NextCursor: result.NextCursor}, nil
}
