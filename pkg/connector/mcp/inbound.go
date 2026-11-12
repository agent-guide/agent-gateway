package mcp

import (
	"context"
	"fmt"
)

func (m *Engine) Initialize(ctx context.Context, target *Target) (map[string]any, error) {
	_, _, err := m.sessionClient(ctx, target)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.upstreamSessions[target.id]
	if session == nil {
		return nil, fmt.Errorf("MCP upstream session is not initialized")
	}
	result := session.initialize
	if result.ProtocolVersion == "" {
		result.ProtocolVersion = ProtocolVersion
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
	return payload, nil
}
