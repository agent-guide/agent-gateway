package mcp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
	"github.com/google/uuid"
)

const idleSessionTTL = 10 * time.Minute

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
	ID                string                `json:"id"`
	ConnectorID       string                `json:"connector_id"`
	UpstreamSessionID string                `json:"upstream_session_id,omitempty"`
	Transport         basemcp.TransportType `json:"transport"`
	State             SessionState          `json:"state"`
	CreatedAt         time.Time             `json:"created_at"`
	LastUsedAt        time.Time             `json:"last_used_at"`
}

// sessionIDer is implemented by transports that expose an upstream session ID.
type sessionIDer interface {
	SessionID() string
}

// beginGatewaySession creates a new GatewaySession in connecting state for connectorID,
// stores it in the manager, and returns it so the caller can update it after initialization.
func (m *Engine) beginGatewaySession(connectorID string, tr basemcp.TransportType) *GatewaySession {
	if m == nil {
		return nil
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
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
	m.viewsMu.Lock()
	m.sessionViews[connectorID] = gsess
	m.viewsMu.Unlock()
	return gsess
}

// finishGatewaySession transitions gsess to ready, extracts the upstream session ID
// from client if it exposes one, and persists the update.
func (m *Engine) finishGatewaySession(gsess *GatewaySession, client transport.Caller) {
	if m == nil || gsess == nil {
		return
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	if m.closed {
		return
	}
	m.viewsMu.Lock()
	defer m.viewsMu.Unlock()
	copy := *gsess
	gsess = &copy
	gsess.State = SessionStateReady
	gsess.LastUsedAt = time.Now().UTC()
	if sider, ok := client.(sessionIDer); ok {
		gsess.UpstreamSessionID = sider.SessionID()
	}
	m.sessionViews[gsess.ConnectorID] = gsess
}

// setGatewaySessionState updates the state of the active GatewaySession for connectorID.
func (m *Engine) setGatewaySessionState(connectorID string, state SessionState) {
	if m == nil {
		return
	}
	m.viewsMu.Lock()
	if gsess := m.sessionViews[connectorID]; gsess != nil && (gsess.State != SessionStateClosed || state == SessionStateClosed) {
		gsess.State = state
		gsess.LastUsedAt = time.Now().UTC()
	}
	m.viewsMu.Unlock()
}

// GetGatewaySession returns a snapshot of the active session for connectorID,
// or nil if no session exists for that connector.
func (m *Engine) GetGatewaySession(connectorID string) *GatewaySession {
	if m == nil {
		return nil
	}
	m.viewsMu.RLock()
	defer m.viewsMu.RUnlock()
	gsess := m.sessionViews[connectorID]
	if gsess == nil {
		return nil
	}
	clone := *gsess
	return &clone
}

// ListGatewaySessions returns a snapshot of all active gateway sessions,
// sorted by creation time.
func (m *Engine) ListGatewaySessions() []GatewaySession {
	if m == nil {
		return nil
	}
	m.viewsMu.RLock()
	defer m.viewsMu.RUnlock()
	out := make([]GatewaySession, 0, len(m.sessionViews))
	for _, gsess := range m.sessionViews {
		out = append(out, *gsess)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (m *Engine) sessionClient(ctx context.Context, target *Target) (transport.Caller, bool, error) {
	if target == nil {
		return nil, false, &cr.CallError{Category: "unsupported_capability", Message: "capability unsupported by connector"}
	}
	id := target.id
	cfg := target.config
	m.stateMu.RLock()
	closed := m.closed
	m.stateMu.RUnlock()
	if closed {
		return nil, false, fmt.Errorf("connector closed")
	}
	select {
	case target.connect <- struct{}{}:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
	defer func() { <-target.connect }()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	signature := connectionSignature(cfg)
	if cached := m.lookupSession(id, signature); cached != nil {
		return cached.transport, true, nil
	}
	candidate, err := newUpstreamTransport(cfg)
	if err != nil {
		return nil, false, err
	}
	client, err := m.initializeSession(ctx, id, cfg.Transport, signature, candidate)
	return client, false, err
}

// connectingCaller is the common startup surface of the three linked transports.
type connectingCaller interface {
	transport.Caller
	Connect(context.Context) error
}

// newUpstreamTransport only constructs the selected transport; lifecycle and
// network operations are shared by initializeSession.
func newUpstreamTransport(cfg Config) (connectingCaller, error) {
	var client connectingCaller
	switch cfg.Transport {
	case basemcp.TransportStreamableHTTP:
		client = transport.NewStreamableHTTPTransport(cfg.URL, http.DefaultClient)
	case basemcp.TransportStdio:
		client = transport.NewStdioTransport(cfg.Command, cfg.Args, osEnvWithOverrides(cfg.Env))
	case basemcp.TransportSSE:
		postURL := cfg.PostURL
		if postURL == "" {
			postURL = deriveSSEPostURL(cfg.URL)
		}
		client = transport.NewSSETransport(cfg.URL, postURL, http.DefaultClient)
	default:
		return nil, fmt.Errorf("MCP upstream transport %q does not support discovery yet", cfg.Transport)
	}
	if headers, ok := client.(headerSetter); ok {
		applyAuth(headers, cfg.AuthConfig)
	}
	return client, nil
}

func (m *Engine) initializeSession(ctx context.Context, id string, tr basemcp.TransportType, signature string, candidate connectingCaller) (transport.Caller, error) {
	initCtx, cancelInit := context.WithCancel(ctx)
	stopShutdown := context.AfterFunc(m.ctx, cancelInit)
	defer stopShutdown()
	defer cancelInit()
	sessionCtx, sessionCancel := context.WithCancel(m.ctx)
	stopCaller := context.AfterFunc(initCtx, sessionCancel)
	session := &Session{cancel: sessionCancel, transport: candidate, configSignature: signature}
	handedOff := false
	defer func() {
		stopCaller()
		if !handedOff {
			m.closeSessions([]*Session{session})
		}
	}()
	if err := candidate.Connect(sessionCtx); err != nil {
		return nil, fmt.Errorf("%s: connect: %w", tr, err)
	}
	gsess := m.beginGatewaySession(id, tr)
	result, err := initializeUpstream(initCtx, candidate)
	if err != nil {
		m.setGatewaySessionState(id, SessionStateError)
		return nil, err
	}
	// Caller cancellation owns initialization only; published sessions belong to
	// the engine. Cancellation that already won must never publish a candidate.
	if !stopCaller() {
		return nil, initCtx.Err()
	}
	if err := initCtx.Err(); err != nil {
		return nil, err
	}
	session.initialize = result
	session.protocolVersion = result.ProtocolVersion
	session.initializedAt = time.Now().UTC()
	session.lastUsedAt = session.initializedAt
	// publishSession takes resource ownership on both success and rejection.
	handedOff = true
	if !m.publishSession(id, session) {
		return nil, fmt.Errorf("connector closed during initialization")
	}
	m.finishGatewaySession(gsess, candidate)
	return candidate, nil
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

func (m *Engine) lookupSession(id, signature string) *Session {
	m.mu.Lock()
	session := m.upstreamSessions[id]
	if session == nil {
		m.mu.Unlock()
		return nil
	}
	expired := session.configSignature != signature || session.transport == nil || session.lastUsedAt.Add(idleSessionTTL).Before(time.Now())
	m.mu.Unlock()
	if expired && m.retireSession(id) {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session = m.upstreamSessions[id]
	if session != nil {
		session.lastUsedAt = time.Now().UTC()
	}
	return session
}

func (m *Engine) publishSession(id string, session *Session) bool {
	m.stateMu.RLock()
	if m.closed {
		m.stateMu.RUnlock()
		m.closeSessions([]*Session{session})
		return false
	}
	m.mu.Lock()
	old := m.upstreamSessions[id]
	m.upstreamSessions[id] = session
	m.mu.Unlock()
	m.stateMu.RUnlock()
	if old != nil {
		m.closeSessions([]*Session{old})
	}
	return true
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

func connectionSignature(cfg Config) string {
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
	case basemcp.TransportStreamableHTTP:
		b.WriteString(cfg.URL)
		b.WriteString("\x00")
		writeAuthSignature()
	case basemcp.TransportSSE:
		b.WriteString(cfg.URL)
		b.WriteString("\x00")
		b.WriteString(cfg.PostURL)
		b.WriteString("\x00")
		writeAuthSignature()
	case basemcp.TransportStdio:
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
