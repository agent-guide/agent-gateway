package connector

import (
	"context"
	"fmt"

	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
)

func (m *Manager) ListTools(ctx context.Context, id string) ([]basemcp.Tool, error) {
	v, ctx, done, err := m.admit(ctx, id, "")
	if err != nil {
		return nil, err
	}
	defer done()
	if v.catalog != nil {
		return v.catalog.Tools(), nil
	}
	return m.mcpEngine.ListTools(ctx, v.mcpTarget)
}
func (m *Manager) ListToolsPage(ctx context.Context, id, cursor string) (*basemcp.ToolListResult, error) {
	v, ctx, done, err := m.admit(ctx, id, "")
	if err != nil {
		return nil, err
	}
	defer done()
	if v.catalog != nil {
		if cursor != "" {
			return nil, fmt.Errorf("HTTP tools do not paginate")
		}
		return &basemcp.ToolListResult{Tools: v.catalog.Tools()}, nil
	}
	return m.mcpEngine.ListToolsPage(ctx, v.mcpTarget, cursor)
}

func (m *Manager) ListResources(ctx context.Context, id string) ([]basemcp.Resource, error) {
	result, err := m.ListResourcesPage(ctx, id, "")
	if err != nil {
		return nil, err
	}
	return result.Resources, nil
}
func (m *Manager) ListResourcesPage(ctx context.Context, id, cursor string) (*basemcp.ResourceListResult, error) {
	v, ctx, done, err := m.admit(ctx, id, "")
	if err != nil {
		return nil, err
	}
	defer done()
	return m.mcpEngine.ListResourcesPage(ctx, v.mcpTarget, cursor)
}

func (m *Manager) ListResourceTemplates(ctx context.Context, id string) ([]basemcp.ResourceTemplate, error) {
	result, err := m.ListResourceTemplatesPage(ctx, id, "")
	if err != nil {
		return nil, err
	}
	return result.ResourceTemplates, nil
}
func (m *Manager) ListResourceTemplatesPage(ctx context.Context, id, cursor string) (*basemcp.ResourceTemplateListResult, error) {
	v, ctx, done, err := m.admit(ctx, id, "")
	if err != nil {
		return nil, err
	}
	defer done()
	return m.mcpEngine.ListResourceTemplatesPage(ctx, v.mcpTarget, cursor)
}

func (m *Manager) ListPrompts(ctx context.Context, id string) ([]basemcp.Prompt, error) {
	result, err := m.ListPromptsPage(ctx, id, "")
	if err != nil {
		return nil, err
	}
	return result.Prompts, nil
}
func (m *Manager) ListPromptsPage(ctx context.Context, id, cursor string) (*basemcp.PromptListResult, error) {
	v, ctx, done, err := m.admit(ctx, id, "")
	if err != nil {
		return nil, err
	}
	defer done()
	return m.mcpEngine.ListPromptsPage(ctx, v.mcpTarget, cursor)
}
