package connector

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
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
func (m *Manager) detachSession(id string) *connectormcp.Session { return m.mcpEngine.Detach(id) }
func (m *Manager) closeSessions(sessions []*connectormcp.Session) {
	m.mcpEngine.CloseSessions(sessions)
}

func (m *Manager) GetGatewaySession(id string) *connectormcp.GatewaySession {
	if m == nil {
		return nil
	}
	return m.mcpEngine.GetGatewaySession(id)
}
func (m *Manager) ListGatewaySessions() []connectormcp.GatewaySession {
	if m == nil {
		return nil
	}
	return m.mcpEngine.ListGatewaySessions()
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
		m.httpEngine.Close()
		m.closeDone = m.mcpEngine.BeginClose()
	})
	for {
		m.snapshotMu.RLock()
		n := len(m.active)
		changed := m.changed
		err := m.mcpEngine.CloseError()
		m.snapshotMu.RUnlock()
		if n == 0 {
			select {
			case <-m.closeDone:
				m.snapshotMu.RLock()
				err = m.mcpEngine.CloseError()
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

// withExclusiveSession atomically checks competing work and detaches a session.
// detach is in-memory only and never reenters Manager. Network close follows
// after this method releases snapshotMu.
func (m *Manager) withExclusiveSession(id string, detach func()) bool {
	m.snapshotMu.Lock()
	defer m.snapshotMu.Unlock()
	if m.closed || m.active[id] > 1 {
		return false
	}
	detach()
	return true
}
