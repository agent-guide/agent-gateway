package connector

import (
	"context"
	"fmt"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
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

// GatewaySession is the gateway's view of one upstream session for an MCP connector.
// It maps a stable gateway-generated ID to the upstream transport session.
type GatewaySession struct {
	ID                string        `json:"id"`
	ConnectorID       string        `json:"connector_id"`
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

// beginGatewaySession creates a new GatewaySession in connecting state for connectorID,
// stores it in the manager, and returns it so the caller can update it after initialization.
func (m *Manager) beginGatewaySession(connectorID string, tr TransportType) *GatewaySession {
	if m == nil {
		return nil
	}
	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()
	if m.closed {
		return nil
	}
	now := time.Now().UTC()
	gsess := &GatewaySession{
		ID:          uuid.New().String(),
		ConnectorID: connectorID,
		Transport:   tr,
		State:       SessionStateConnecting,
		CreatedAt:   now,
		LastUsedAt:  now,
	}
	m.sessionsMu.Lock()
	m.sessions[connectorID] = gsess
	m.sessionsMu.Unlock()
	return gsess
}

// finishGatewaySession transitions gsess to ready, extracts the upstream session ID
// from client if it exposes one, and persists the update.
func (m *Manager) finishGatewaySession(gsess *GatewaySession, client transport.Caller) {
	if m == nil || gsess == nil {
		return
	}
	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()
	if m.closed {
		return
	}
	m.sessionsMu.Lock()
	defer m.sessionsMu.Unlock()
	copy := *gsess
	gsess = &copy
	gsess.State = SessionStateReady
	gsess.LastUsedAt = time.Now().UTC()
	if sider, ok := client.(sessionIDer); ok {
		gsess.UpstreamSessionID = sider.SessionID()
	}
	m.sessions[gsess.ConnectorID] = gsess
}

// setGatewaySessionState updates the state of the active GatewaySession for connectorID.
func (m *Manager) setGatewaySessionState(connectorID string, state SessionState) {
	if m == nil {
		return
	}
	m.sessionsMu.Lock()
	if gsess := m.sessions[connectorID]; gsess != nil && (gsess.State != SessionStateClosed || state == SessionStateClosed) {
		gsess.State = state
		gsess.LastUsedAt = time.Now().UTC()
	}
	m.sessionsMu.Unlock()
}

// GetGatewaySession returns a snapshot of the active session for connectorID,
// or nil if no session exists for that connector.
func (m *Manager) GetGatewaySession(connectorID string) *GatewaySession {
	if m == nil {
		return nil
	}
	m.sessionsMu.RLock()
	defer m.sessionsMu.RUnlock()
	gsess := m.sessions[connectorID]
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

func (m *Manager) discoveryClientConfig(ctx context.Context, cfg ConnectorConfig) (transport.Caller, bool, error) {
	id := cfg.ID
	m.snapshotMu.RLock()
	v := m.entries[id]
	closed := m.closed
	m.snapshotMu.RUnlock()
	if closed || v == nil {
		return nil, false, fmt.Errorf("connector closed")
	}
	select {
	case v.connect <- struct{}{}:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
	defer func() { <-v.connect }()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	sessionCtx, sessionCancel := context.WithCancel(m.ctx)
	stop := context.AfterFunc(ctx, sessionCancel)
	accepted := false
	var candidate transport.Caller
	defer func() {
		stop()
		if !accepted {
			sessionCancel()
			if candidate != nil {
				_ = candidate.Close()
			}
		}
	}()
	if cfg.Type != "mcp" {
		return nil, false, &cr.CallError{Category: "unsupported_capability", Message: "capability unsupported by connector"}
	}
	if cfg.Disabled {
		return nil, false, fmt.Errorf("mcp service is disabled")
	}

	signature := discoveryConfigSignature(cfg)
	if cached := m.lookupDiscoverySession(id, signature); cached != nil {
		return cached.transport, true, nil
	}

	switch cfg.MCP.Transport {
	case TransportStreamableHTTP:
		client := transport.NewStreamableHTTPTransport(cfg.MCP.URL, http.DefaultClient)
		candidate = client
		applyServiceAuth(client, cfg.MCP.AuthConfig)
		gsess := m.beginGatewaySession(id, cfg.MCP.Transport)
		initResult, err := initializeService(ctx, client)
		if err != nil {
			m.setGatewaySessionState(id, SessionStateError)
			return nil, false, err
		}
		// Disarm caller cancellation before making this a manager-owned session.
		// If cancellation already won, discard the candidate instead of caching it.
		if !stop() {
			return nil, false, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if !m.storeDiscoverySession(id, &discoverySession{
			cancel:          sessionCancel,
			transport:       client,
			configSignature: signature,
			protocolVersion: initResult.ProtocolVersion,
			initialize:      initResult,
			initializedAt:   time.Now().UTC(),
			lastUsedAt:      time.Now().UTC(),
		}) {
			return nil, false, fmt.Errorf("connector closed during initialization")
		}
		accepted = true
		m.finishGatewaySession(gsess, client)
		return client, false, nil

	case TransportStdio:
		tr := transport.NewStdioTransport(cfg.MCP.Command, cfg.MCP.Args, osEnvWithOverrides(cfg.MCP.Env))
		candidate = tr
		if err := tr.Connect(sessionCtx); err != nil {
			return nil, false, fmt.Errorf("stdio: connect: %w", err)
		}
		gsess := m.beginGatewaySession(id, cfg.MCP.Transport)
		initResult, err := initializeService(ctx, tr)
		if err != nil {
			m.setGatewaySessionState(id, SessionStateError)
			_ = tr.Close()
			return nil, false, err
		}
		// Disarm caller cancellation before making this a manager-owned session.
		// If cancellation already won, discard the candidate instead of caching it.
		if !stop() {
			return nil, false, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if !m.storeDiscoverySession(id, &discoverySession{
			cancel:          sessionCancel,
			transport:       tr,
			configSignature: signature,
			protocolVersion: initResult.ProtocolVersion,
			initialize:      initResult,
			initializedAt:   time.Now().UTC(),
			lastUsedAt:      time.Now().UTC(),
		}) {
			return nil, false, fmt.Errorf("connector closed during initialization")
		}
		accepted = true
		m.finishGatewaySession(gsess, tr)
		return tr, false, nil

	case TransportSSE:
		postURL := cfg.MCP.PostURL
		if postURL == "" {
			postURL = deriveSSEPostURL(cfg.MCP.URL)
		}
		client := transport.NewSSETransport(cfg.MCP.URL, postURL, http.DefaultClient)
		candidate = client
		applyServiceAuth(client, cfg.MCP.AuthConfig)
		if err := client.Connect(sessionCtx); err != nil {
			return nil, false, fmt.Errorf("sse: connect: %w", err)
		}
		gsess := m.beginGatewaySession(id, cfg.MCP.Transport)
		initResult, err := initializeService(ctx, client)
		if err != nil {
			m.setGatewaySessionState(id, SessionStateError)
			_ = client.Close()
			return nil, false, err
		}
		// Disarm caller cancellation before making this a manager-owned session.
		// If cancellation already won, discard the candidate instead of caching it.
		if !stop() {
			return nil, false, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if !m.storeDiscoverySession(id, &discoverySession{
			cancel:          sessionCancel,
			transport:       client,
			configSignature: signature,
			protocolVersion: initResult.ProtocolVersion,
			initialize:      initResult,
			initializedAt:   time.Now().UTC(),
			lastUsedAt:      time.Now().UTC(),
		}) {
			return nil, false, fmt.Errorf("connector closed during initialization")
		}
		accepted = true
		m.finishGatewaySession(gsess, client)
		return client, false, nil

	default:
		return nil, false, fmt.Errorf("mcp service transport %q does not support discovery yet", cfg.MCP.Transport)
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

func (m *Manager) lookupDiscoverySession(id, signature string) *discoverySession {
	m.snapshotMu.RLock()
	busy := m.active[id] > 1
	m.snapshotMu.RUnlock()
	m.mu.Lock()
	session := m.discoverySession[id]
	if session == nil {
		m.mu.Unlock()
		return nil
	}
	if session.configSignature != signature || session.transport == nil || !busy && session.lastUsedAt.Add(discoverySessionTTL).Before(time.Now()) {
		delete(m.discoverySession, id)
		m.mu.Unlock()
		m.closeSessions([]*discoverySession{session})
		return nil
	}
	session.lastUsedAt = time.Now().UTC()
	m.mu.Unlock()
	return session
}
func (m *Manager) storeDiscoverySession(id string, session *discoverySession) bool {
	m.snapshotMu.RLock()
	if m.closed {
		m.snapshotMu.RUnlock()
		m.closeSessions([]*discoverySession{session})
		return false
	}
	m.mu.Lock()
	old := m.discoverySession[id]
	m.discoverySession[id] = session
	m.mu.Unlock()
	m.snapshotMu.RUnlock()
	if old != nil {
		m.closeSessions([]*discoverySession{old})
	}
	return true
}
func (m *Manager) invalidateDiscoverySession(id string) {
	m.closeSessions([]*discoverySession{m.detachSession(id)})
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

func discoveryConfigSignature(cfg ConnectorConfig) string {
	var b strings.Builder
	b.WriteString(string(cfg.MCP.Transport))
	b.WriteString("\x00")
	writeAuthSignature := func() {
		if cfg.MCP.AuthConfig != nil {
			b.WriteString(strings.TrimSpace(cfg.MCP.AuthConfig.Type))
			b.WriteString("\x00")
			b.WriteString(cfg.MCP.AuthConfig.APIKey)
			b.WriteString("\x00")
			b.WriteString(cfg.MCP.AuthConfig.Username)
			b.WriteString("\x00")
			b.WriteString(cfg.MCP.AuthConfig.Password)
		}
	}
	switch cfg.MCP.Transport {
	case TransportStreamableHTTP:
		b.WriteString(cfg.MCP.URL)
		b.WriteString("\x00")
		writeAuthSignature()
	case TransportSSE:
		b.WriteString(cfg.MCP.URL)
		b.WriteString("\x00")
		b.WriteString(cfg.MCP.PostURL)
		b.WriteString("\x00")
		writeAuthSignature()
	case TransportStdio:
		b.WriteString(cfg.MCP.Command)
		b.WriteString("\x00")
		for _, arg := range cfg.MCP.Args {
			b.WriteString(arg)
			b.WriteString("\x01")
		}
		b.WriteString("\x00")
		keys := make([]string, 0, len(cfg.MCP.Env))
		for k := range cfg.MCP.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(cfg.MCP.Env[k])
			b.WriteString("\x01")
		}
	}
	return b.String()
}
