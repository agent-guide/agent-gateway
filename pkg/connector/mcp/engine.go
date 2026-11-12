package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

// ExclusiveRetirement runs detach under the caller's admission lock only when
// no competing operation owns the session. detach performs no network I/O and
// must be called synchronously at most once. The engine never calls this hook
// while holding its own locks.
type ExclusiveRetirement func(connectorID string, detach func()) bool

// Target is an immutable execution definition with per-connector connection
// serialization. Display edits retain the same target. It owns no resources.
type Target struct {
	id      string
	config  Config
	connect chan struct{}
}

func NewTarget(id string, cfg Config) *Target {
	cfg.Args = slices.Clone(cfg.Args)
	cfg.Env = maps.Clone(cfg.Env)
	if cfg.AuthConfig != nil {
		auth := *cfg.AuthConfig
		cfg.AuthConfig = &auth
	}
	return &Target{id: id, config: cfg, connect: make(chan struct{}, 1)}
}

// Engine owns upstream MCP sessions; the composing Manager admits all work.
type Engine struct {
	ctx              context.Context
	cancel           context.CancelFunc
	exclusive        ExclusiveRetirement
	stateMu          sync.RWMutex
	closed           bool
	closeErr         error
	closeOnce        sync.Once
	closeDone        chan struct{}
	mu               sync.Mutex
	upstreamSessions map[string]*Session
	viewsMu          sync.RWMutex
	sessionViews     map[string]*GatewaySession
}

func NewEngine(exclusive ExclusiveRetirement) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{ctx: ctx, cancel: cancel, exclusive: exclusive, closeDone: make(chan struct{}), upstreamSessions: map[string]*Session{}, sessionViews: map[string]*GatewaySession{}}
}

// Session is an opaque upstream resource, cached until detached. Engine closes
// detached sessions outside Manager's admission/mutation locks.
type Session struct {
	cancel          context.CancelFunc
	transport       transport.Caller
	configSignature string
	protocolVersion string
	initialize      initializeResult
	initializedAt   time.Time
	lastUsedAt      time.Time
}

// Detach removes a cached session without I/O. The Manager invokes this while
// admission is excluded by its snapshot lock or a pending mutation gate.
func (m *Engine) Detach(id string) *Session {
	m.mu.Lock()
	s := m.upstreamSessions[id]
	delete(m.upstreamSessions, id)
	m.mu.Unlock()
	if s != nil {
		m.setGatewaySessionState(id, SessionStateClosed)
	}
	return s
}

func (m *Engine) closeSessions(sessions []*Session) {
	for _, s := range sessions {
		if s == nil {
			continue
		}
		if s.cancel != nil {
			s.cancel()
		}
		if err := s.transport.Close(); err != nil {
			m.stateMu.Lock()
			m.closeErr = errors.Join(m.closeErr, fmt.Errorf("MCP session close failed: %w", err))
			m.stateMu.Unlock()
		}
	}
}

func (m *Engine) CloseSessions(sessions []*Session) { m.closeSessions(sessions) }

func (m *Engine) CloseError() error {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.closeErr
}

func (m *Engine) retireSession(id string) bool {
	if m.exclusive == nil {
		return false
	}
	var retired *Session
	ok := m.exclusive(id, func() { retired = m.Detach(id) })
	if ok {
		m.closeSessions([]*Session{retired})
	}
	return ok
}

// BeginClose fences session publication and cancels initialization immediately.
// Network/process closure runs asynchronously; Manager also waits for its work.
func (m *Engine) BeginClose() <-chan struct{} {
	m.closeOnce.Do(func() {
		m.stateMu.Lock()
		m.closed = true
		m.cancel()
		m.stateMu.Unlock()
		m.mu.Lock()
		var sessions []*Session
		for id, s := range m.upstreamSessions {
			sessions = append(sessions, s)
			delete(m.upstreamSessions, id)
		}
		m.mu.Unlock()
		m.viewsMu.Lock()
		for _, s := range m.sessionViews {
			s.State = SessionStateClosed
		}
		m.viewsMu.Unlock()
		go func() { m.closeSessions(sessions); close(m.closeDone) }()
	})
	return m.closeDone
}
