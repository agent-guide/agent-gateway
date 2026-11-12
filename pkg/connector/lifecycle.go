package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
)

var ErrConnectorBusy = errors.New("connector is in use")

// Keep management recovery metadata inspectable without exposing it through the
// execution error's text. Admin uses the cause for status and Retry-After;
// execution adapters use the bounded CallError.
type pendingAdmissionError struct {
	boundary *cr.CallError
	pending  *configmgr.PendingMutationError
}

func (e *pendingAdmissionError) Error() string   { return e.boundary.Error() }
func (e *pendingAdmissionError) Unwrap() []error { return []error{e.boundary, e.pending} }

// ExecutionEqual ignores display, revocation and server-owned metadata. JSON
// canonicalizes map order and omits equivalent empty optional collections.
func ExecutionEqual(a, b ConnectorConfig) bool {
	a = cloneConfig(a)
	b = cloneConfig(b)
	a.ID = ""
	b.ID = ""
	a.Name = ""
	b.Name = ""
	a.Description = ""
	b.Description = ""
	a.Disabled = false
	b.Disabled = false
	a.CreatedAt = time.Time{}
	b.CreatedAt = time.Time{}
	a.UpdatedAt = time.Time{}
	b.UpdatedAt = time.Time{}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (m *Manager) ExecutionChange(id string, next *ConnectorConfig) bool {
	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()
	old := m.entries[id]
	return old != nil && (next == nil || !ExecutionEqual(old.config, *next))
}
func (m *Manager) Snapshot() []ConnectorConfig {
	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()
	out := make([]ConnectorConfig, 0, len(m.entries))
	for _, v := range m.entries {
		out = append(out, cloneConfig(v.config))
	}
	return out
}

// admit linearizes version/pending/closed checks and work registration with
// mutation gating. It owns no network locks. Queueing is part of active work.
func (m *Manager) admit(ctx context.Context, id, token string) (*entry, context.Context, func(), error) {
	if err := m.ensure(ctx); err != nil {
		return nil, ctx, nil, err
	}
	m.snapshotMu.Lock()
	if m.closed {
		m.snapshotMu.Unlock()
		return nil, ctx, nil, &cr.CallError{Category: "connector_unavailable", Message: "connector closed"}
	}
	v := m.entries[id]
	if v == nil {
		m.snapshotMu.Unlock()
		return nil, ctx, nil, errors.Join(ErrConnectorNotConfigured, &cr.CallError{Category: "connector_unavailable", Message: "connector unavailable"})
	}
	if v.config.Disabled {
		m.snapshotMu.Unlock()
		return nil, ctx, nil, &cr.CallError{Category: "connector_unavailable", Message: "connector unavailable"}
	}
	if token != "" && v.token != token {
		m.snapshotMu.Unlock()
		return nil, ctx, nil, &cr.CallError{Category: "generation_unavailable", Message: "connector execution changed"}
	}
	if p := m.pending[id]; p != nil {
		state := p.state
		m.snapshotMu.Unlock()
		return nil, ctx, nil, &pendingAdmissionError{
			boundary: &cr.CallError{Category: "mutation_pending", Message: "connector requires recovery"},
			pending:  &configmgr.PendingMutationError{Mutation: state},
		}
	}
	if err := ctx.Err(); err != nil {
		m.snapshotMu.Unlock()
		return nil, ctx, nil, err
	}
	m.active[id]++
	m.snapshotMu.Unlock()
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	var once sync.Once
	done := func() {
		once.Do(func() {
			stop()
			cancel()
			m.snapshotMu.Lock()
			m.active[id]--
			if m.active[id] == 0 {
				delete(m.active, id)
			}
			close(m.changed)
			m.changed = make(chan struct{})
			m.snapshotMu.Unlock()
		})
	}
	return v, callCtx, done, nil
}
func (m *Manager) detachSession(id string) *discoverySession {
	m.mu.Lock()
	s := m.discoverySession[id]
	delete(m.discoverySession, id)
	m.mu.Unlock()
	if s != nil {
		m.setGatewaySessionState(id, SessionStateClosed)
	}
	return s
}
func (m *Manager) closeSessions(sessions []*discoverySession) {
	for _, s := range sessions {
		if s == nil {
			continue
		}
		if s.cancel != nil {
			s.cancel()
		}
		if err := s.transport.Close(); err != nil {
			m.snapshotMu.Lock()
			m.closeErr = errors.Join(m.closeErr, fmt.Errorf("MCP session close failed: %w", err))
			m.snapshotMu.Unlock()
		}
	}
}
func (m *Manager) CloseContext(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.snapshotMu.Lock()
		m.closed = true
		m.cancel()
		m.snapshotMu.Unlock()
		m.mu.Lock()
		var sessions []*discoverySession
		for id, s := range m.discoverySession {
			sessions = append(sessions, s)
			delete(m.discoverySession, id)
		}
		m.mu.Unlock()
		m.sessionsMu.Lock()
		for _, session := range m.sessions {
			session.State = SessionStateClosed
		}
		m.sessionsMu.Unlock()
		go func() { m.closeSessions(sessions); close(m.closeDone) }()
	})
	for {
		m.snapshotMu.RLock()
		n := len(m.active)
		changed := m.changed
		err := m.closeErr
		m.snapshotMu.RUnlock()
		if n == 0 {
			select {
			case <-m.closeDone:
				m.snapshotMu.RLock()
				err = m.closeErr
				m.snapshotMu.RUnlock()
				return err
			case <-ctx.Done():
				return errors.Join(err, ctx.Err())
			}
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		}
	}
}

// A failing call may reconnect only when no other call owns the old session.
func (m *Manager) retireFailedSession(id string) bool {
	m.snapshotMu.Lock()
	if m.active[id] > 1 {
		m.snapshotMu.Unlock()
		return false
	}
	s := m.detachSession(id)
	m.snapshotMu.Unlock()
	m.closeSessions([]*discoverySession{s})
	return true
}
