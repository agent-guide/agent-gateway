package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

const discoverySessionTTL = 10 * time.Minute

func (m *Manager) discoveryClient(ctx context.Context, id string) (*transport.StreamableHTTPTransport, bool, error) {
	cfg, err := m.Get(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if cfg.Disabled {
		return nil, false, fmt.Errorf("mcp service is disabled")
	}
	if cfg.Transport != TransportStreamableHTTP {
		return nil, false, fmt.Errorf("mcp service transport %q does not support discovery yet", cfg.Transport)
	}

	signature := discoveryConfigSignature(cfg)
	if cached := m.lookupDiscoverySession(id, signature); cached != nil {
		return cached.transport, true, nil
	}

	client := transport.NewStreamableHTTPTransport(cfg.URL, http.DefaultClient)
	applyServiceAuth(client, cfg.AuthConfig)
	initResult, err := initializeService(ctx, client)
	if err != nil {
		return nil, false, err
	}
	m.storeDiscoverySession(id, &discoverySession{
		transport:       client,
		configSignature: signature,
		protocolVersion: initResult.ProtocolVersion,
		initialize:      initResult,
		initializedAt:   time.Now().UTC(),
		lastUsedAt:      time.Now().UTC(),
	})
	return client, false, nil
}

func (m *Manager) lookupDiscoverySession(id string, signature string) *discoverySession {
	if m == nil {
		return nil
	}
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.discoverySession[id]
	if session == nil {
		return nil
	}
	if session.configSignature != signature || session.transport == nil || session.lastUsedAt.Add(discoverySessionTTL).Before(now) {
		delete(m.discoverySession, id)
		if session.transport != nil {
			_ = session.transport.Close()
		}
		return nil
	}
	session.lastUsedAt = now
	return session
}

func (m *Manager) storeDiscoverySession(id string, session *discoverySession) {
	if m == nil || session == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.discoverySession == nil {
		m.discoverySession = make(map[string]*discoverySession)
	}
	if previous := m.discoverySession[id]; previous != nil && previous.transport != nil && previous.transport != session.transport {
		_ = previous.transport.Close()
	}
	m.discoverySession[id] = session
}

func (m *Manager) invalidateDiscoverySession(id string) {
	if m == nil || strings.TrimSpace(id) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.discoverySession == nil {
		return
	}
	if session := m.discoverySession[id]; session != nil && session.transport != nil {
		_ = session.transport.Close()
	}
	delete(m.discoverySession, id)
}

func discoveryConfigSignature(cfg MCPServiceConfig) string {
	var b strings.Builder
	b.WriteString(string(cfg.Transport))
	b.WriteString("\x00")
	b.WriteString(cfg.URL)
	b.WriteString("\x00")
	if cfg.AuthConfig != nil {
		b.WriteString(strings.TrimSpace(cfg.AuthConfig.Type))
		b.WriteString("\x00")
		b.WriteString(cfg.AuthConfig.APIKey)
		b.WriteString("\x00")
		b.WriteString(cfg.AuthConfig.Username)
		b.WriteString("\x00")
		b.WriteString(cfg.AuthConfig.Password)
	}
	return b.String()
}
