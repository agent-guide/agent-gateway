package connector

import (
	"context"
	"fmt"
)

func (m *Manager) Initialize(ctx context.Context, id string) (map[string]any, error) {
	v, ctx, done, err := m.admit(ctx, id, "")
	if err != nil {
		return nil, err
	}
	defer done()
	if v.catalog != nil {
		return map[string]any{"protocolVersion": latestProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "agent-gateway", "version": "dev"}}, nil
	}
	client, _, err := m.discoveryClientConfig(ctx, v.config)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.discoverySession[id]
	if session == nil {
		return nil, fmt.Errorf("mcp service session is not initialized")
	}
	result := session.initialize
	if result.ProtocolVersion == "" {
		result.ProtocolVersion = latestProtocolVersion
	}
	payload := map[string]any{
		"protocolVersion": result.ProtocolVersion,
	}
	if result.Capabilities != nil {
		payload["capabilities"] = result.Capabilities
	}
	if result.ServerInfo != nil {
		payload["serverInfo"] = result.ServerInfo
	}
	if result.Instructions != nil {
		payload["instructions"] = result.Instructions
	}
	_ = client
	return payload, nil
}
