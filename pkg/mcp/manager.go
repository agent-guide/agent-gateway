package mcp

import "context"

// TransportType defines the MCP transport type.
type TransportType string

const (
	TransportStdio     TransportType = "stdio"
	TransportSSE       TransportType = "sse"
	TransportWebSocket TransportType = "websocket"
)

// ClientStatus represents the connection status of an MCP client.
type ClientStatus string

const (
	ClientStatusInactive   ClientStatus = "inactive"
	ClientStatusConnecting ClientStatus = "connecting"
	ClientStatusConnected  ClientStatus = "connected"
	ClientStatusError      ClientStatus = "error"
)

// MCPManager manages MCP client connections.
type MCPManager interface {
	AddClient(ctx context.Context, config ClientConfig) (Client, error)
	RemoveClient(ctx context.Context, clientID string) error
	GetClient(ctx context.Context, clientID string) (Client, error)
	ListClients(ctx context.Context) ([]Client, error)
}

// Manager is the default implementation of MCPManager.
type Manager struct {
	clients map[string]Client
}

// NewManager creates a new MCP Manager.
func NewManager() *Manager {
	return &Manager{clients: make(map[string]Client)}
}

func (m *Manager) AddClient(ctx context.Context, config ClientConfig) (Client, error) {
	// TODO: implement
	return nil, nil
}

func (m *Manager) RemoveClient(ctx context.Context, clientID string) error {
	// TODO: implement
	return nil
}

func (m *Manager) GetClient(ctx context.Context, clientID string) (Client, error) {
	c, ok := m.clients[clientID]
	if !ok {
		return nil, nil
	}
	return c, nil
}

func (m *Manager) ListClients(ctx context.Context) ([]Client, error) {
	clients := make([]Client, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	return clients, nil
}
