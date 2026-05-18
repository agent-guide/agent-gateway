package service

import (
	"context"
	"fmt"
)

func (m *Manager) Initialize(ctx context.Context, id string) (map[string]any, error) {
	client, _, err := m.discoveryClient(ctx, id)
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
