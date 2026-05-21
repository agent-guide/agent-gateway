package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
	"github.com/google/uuid"
)

const discoverySessionTTL = 10 * time.Minute

// SessionState is the lifecycle state of a gateway upstream session.
type SessionState string

const (
	SessionStateConnecting SessionState = "connecting"
	SessionStateReady      SessionState = "ready"
	SessionStateError      SessionState = "error"
	SessionStateClosed     SessionState = "closed"
)

// GatewaySession is the gateway's view of one upstream session for an MCP service.
// It maps a stable gateway-generated ID to the upstream transport session.
type GatewaySession struct {
	ID                string        `json:"id"`
	ServiceID         string        `json:"service_id"`
	UpstreamSessionID string        `json:"upstream_session_id,omitempty"`
	Transport         TransportType `json:"transport"`
	State             SessionState  `json:"state"`
	CreatedAt         time.Time     `json:"created_at"`
	LastUsedAt        time.Time     `json:"last_used_at"`
}

// sessionIDer is implemented by transports that expose an upstream session ID.
type sessionIDer interface {
	SessionID() string
}

// beginGatewaySession creates a new GatewaySession in connecting state for serviceID,
// stores it in the manager, and returns it so the caller can update it after initialization.
func (m *Manager) beginGatewaySession(serviceID string, tr TransportType) *GatewaySession {
	if m == nil {
		return nil
	}
	now := time.Now().UTC()
	gsess := &GatewaySession{
		ID:         uuid.New().String(),
		ServiceID:  serviceID,
		Transport:  tr,
		State:      SessionStateConnecting,
		CreatedAt:  now,
		LastUsedAt: now,
	}
	m.sessionsMu.Lock()
	m.sessions[serviceID] = gsess
	m.sessionsMu.Unlock()
	return gsess
}

// finishGatewaySession transitions gsess to ready, extracts the upstream session ID
// from client if it exposes one, and persists the update.
func (m *Manager) finishGatewaySession(gsess *GatewaySession, client transport.Caller) {
	if m == nil || gsess == nil {
		return
	}
	gsess.State = SessionStateReady
	gsess.LastUsedAt = time.Now().UTC()
	if sider, ok := client.(sessionIDer); ok {
		gsess.UpstreamSessionID = sider.SessionID()
	}
	m.sessionsMu.Lock()
	m.sessions[gsess.ServiceID] = gsess
	m.sessionsMu.Unlock()
}

// setGatewaySessionState updates the state of the active GatewaySession for serviceID.
func (m *Manager) setGatewaySessionState(serviceID string, state SessionState) {
	if m == nil {
		return
	}
	m.sessionsMu.Lock()
	if gsess := m.sessions[serviceID]; gsess != nil {
		gsess.State = state
		gsess.LastUsedAt = time.Now().UTC()
	}
	m.sessionsMu.Unlock()
}

// GetGatewaySession returns a snapshot of the active session for serviceID,
// or nil if no session exists for that service.
func (m *Manager) GetGatewaySession(serviceID string) *GatewaySession {
	if m == nil {
		return nil
	}
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	gsess := m.sessions[serviceID]
	if gsess == nil {
		return nil
	}
	clone := *gsess
	return &clone
}

// ListGatewaySessions returns a snapshot of all active gateway sessions,
// sorted by creation time.
func (m *Manager) ListGatewaySessions() []GatewaySession {
	if m == nil {
		return nil
	}
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	out := make([]GatewaySession, 0, len(m.sessions))
	for _, gsess := range m.sessions {
		out = append(out, *gsess)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (m *Manager) discoveryClient(ctx context.Context, id string) (transport.Caller, bool, error) {
	cfg, err := m.Get(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if cfg.Disabled {
		return nil, false, fmt.Errorf("mcp service is disabled")
	}

	signature := discoveryConfigSignature(cfg)
	if cached := m.lookupDiscoverySession(id, signature); cached != nil {
		return cached.transport, true, nil
	}

	switch cfg.Transport {
	case TransportStreamableHTTP:
		client := transport.NewStreamableHTTPTransport(cfg.URL, http.DefaultClient)
		applyServiceAuth(client, cfg.AuthConfig)
		gsess := m.beginGatewaySession(id, cfg.Transport)
		initResult, err := initializeService(ctx, client)
		if err != nil {
			m.setGatewaySessionState(id, SessionStateError)
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
		m.finishGatewaySession(gsess, client)
		return client, false, nil

	case TransportStdio:
		tr := transport.NewStdioTransport(cfg.Command, cfg.Args, osEnvWithOverrides(cfg.Env))
		if err := tr.Connect(ctx); err != nil {
			return nil, false, fmt.Errorf("stdio: connect: %w", err)
		}
		gsess := m.beginGatewaySession(id, cfg.Transport)
		initResult, err := initializeService(ctx, tr)
		if err != nil {
			m.setGatewaySessionState(id, SessionStateError)
			_ = tr.Close()
			return nil, false, err
		}
		m.storeDiscoverySession(id, &discoverySession{
			transport:       tr,
			configSignature: signature,
			protocolVersion: initResult.ProtocolVersion,
			initialize:      initResult,
			initializedAt:   time.Now().UTC(),
			lastUsedAt:      time.Now().UTC(),
		})
		m.finishGatewaySession(gsess, tr)
		return tr, false, nil

	case TransportSSE:
		postURL := cfg.PostURL
		if postURL == "" {
			postURL = deriveSSEPostURL(cfg.URL)
		}
		client := transport.NewSSETransport(cfg.URL, postURL, http.DefaultClient)
		applyServiceAuth(client, cfg.AuthConfig)
		if err := client.Connect(ctx); err != nil {
			return nil, false, fmt.Errorf("sse: connect: %w", err)
		}
		gsess := m.beginGatewaySession(id, cfg.Transport)
		initResult, err := initializeService(ctx, client)
		if err != nil {
			m.setGatewaySessionState(id, SessionStateError)
			_ = client.Close()
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
		m.finishGatewaySession(gsess, client)
		return client, false, nil

	default:
		return nil, false, fmt.Errorf("mcp service transport %q does not support discovery yet", cfg.Transport)
	}
}

// osEnvWithOverrides merges overrides into the current process environment.
// Existing keys are replaced; new keys are appended. Returns nil (inherit) when
// overrides is empty.
func osEnvWithOverrides(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return nil
	}
	base := os.Environ()
	idx := make(map[string]int, len(base))
	for i, kv := range base {
		if eq := strings.IndexByte(kv, '='); eq >= 0 {
			idx[kv[:eq]] = i
		}
	}
	result := make([]string, len(base))
	copy(result, base)
	for k, v := range overrides {
		if i, ok := idx[k]; ok {
			result[i] = k + "=" + v
		} else {
			result = append(result, k+"="+v)
		}
	}
	return result
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
	m.setGatewaySessionState(id, SessionStateClosed)
}

// deriveSSEPostURL returns a best-effort POST endpoint URL for a legacy SSE MCP
// server. If the stream URL ends with /sse the suffix is replaced with /message;
// otherwise /message is appended to the path.
func deriveSSEPostURL(streamURL string) string {
	if strings.HasSuffix(streamURL, "/sse") {
		return strings.TrimSuffix(streamURL, "/sse") + "/message"
	}
	return strings.TrimRight(streamURL, "/") + "/message"
}

func discoveryConfigSignature(cfg MCPServiceConfig) string {
	var b strings.Builder
	b.WriteString(string(cfg.Transport))
	b.WriteString("\x00")
	writeAuthSignature := func() {
		if cfg.AuthConfig != nil {
			b.WriteString(strings.TrimSpace(cfg.AuthConfig.Type))
			b.WriteString("\x00")
			b.WriteString(cfg.AuthConfig.APIKey)
			b.WriteString("\x00")
			b.WriteString(cfg.AuthConfig.Username)
			b.WriteString("\x00")
			b.WriteString(cfg.AuthConfig.Password)
		}
	}
	switch cfg.Transport {
	case TransportStreamableHTTP:
		b.WriteString(cfg.URL)
		b.WriteString("\x00")
		writeAuthSignature()
	case TransportSSE:
		b.WriteString(cfg.URL)
		b.WriteString("\x00")
		b.WriteString(cfg.PostURL)
		b.WriteString("\x00")
		writeAuthSignature()
	case TransportStdio:
		b.WriteString(cfg.Command)
		b.WriteString("\x00")
		for _, arg := range cfg.Args {
			b.WriteString(arg)
			b.WriteString("\x01")
		}
		b.WriteString("\x00")
		keys := make([]string, 0, len(cfg.Env))
		for k := range cfg.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(cfg.Env[k])
			b.WriteString("\x01")
		}
	}
	return b.String()
}
