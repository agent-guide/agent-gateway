package connector

import (
	"context"

	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
)

func (m *Manager) Initialize(ctx context.Context, id string) (map[string]any, error) {
	v, ctx, done, err := m.admit(ctx, id, "")
	if err != nil {
		return nil, err
	}
	defer done()
	if v.catalog != nil {
		return map[string]any{"protocolVersion": connectormcp.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "agent-gateway", "version": "dev"}}, nil
	}
	return m.mcpEngine.Initialize(ctx, v.mcpTarget)
}
