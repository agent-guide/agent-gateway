package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
	"github.com/google/uuid"
)

type pendingWrite struct {
	state     configmgr.PendingMutation
	old, next *entry
}

func sameDefinition(a, b ConnectorConfig) bool {
	a.CreatedAt = time.Time{}
	a.UpdatedAt = time.Time{}
	b.CreatedAt = time.Time{}
	b.UpdatedAt = time.Time{}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (m *Manager) beginPending(id string, action configmgr.MutationAction, old, next *entry) (*pendingWrite, error) {
	p := &pendingWrite{state: configmgr.PendingMutation{OperationID: uuid.NewString(), ResourceID: id, Action: action, State: "pending", StartedAt: time.Now().UTC()}, old: old, next: next}
	m.snapshotMu.Lock()
	if m.closed {
		m.snapshotMu.Unlock()
		return nil, fmt.Errorf("connector manager closed")
	}
	if m.pending[id] != nil {
		m.snapshotMu.Unlock()
		return nil, configmgr.ErrMutationPending
	}
	if (next == nil || old == nil || !ExecutionEqual(old.config, next.config)) && m.active[id] > 0 {
		m.snapshotMu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrConnectorBusy, id)
	}
	m.pending[id] = p
	m.snapshotMu.Unlock()
	return p, nil
}
func (m *Manager) clearPending(id string) {
	m.snapshotMu.Lock()
	delete(m.pending, id)
	m.snapshotMu.Unlock()
}
func (m *Manager) PendingMutation(id string) (configmgr.PendingMutation, bool) {
	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()
	p := m.pending[id]
	if p == nil {
		return configmgr.PendingMutation{}, false
	}
	return p.state, true
}
func (m *Manager) CheckExecution(id string) error {
	if m == nil {
		return ErrConnectorNotConfigured
	}
	if p, ok := m.PendingMutation(id); ok {
		return &configmgr.PendingMutationError{Mutation: p}
	}
	return nil
}
func (m *Manager) Recover(ctx context.Context, id string) error {
	var retired []*connectormcp.Session
	defer func() { m.closeSessions(retired) }()
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	m.snapshotMu.RLock()
	p := m.pending[id]
	closed := m.closed
	m.snapshotMu.RUnlock()
	if closed {
		return fmt.Errorf("connector manager closed")
	}
	if p == nil {
		return nil
	}
	if time.Now().Before(p.state.RetryAfter) {
		return &configmgr.PendingMutationError{Mutation: p.state}
	}
	fail := func() error {
		m.snapshotMu.Lock()
		p.state = configmgr.BackoffRecovery(p.state, time.Now().UTC())
		state := p.state
		m.snapshotMu.Unlock()
		return &configmgr.PendingMutationError{Mutation: state}
	}
	item, err := m.store.Get(ctx, id)
	var verified *entry
	if errors.Is(err, configstore.ErrNotFound) {
		if p.old != nil && p.next != nil {
			return fail()
		}
	} else if err != nil {
		return fail()
	} else {
		cfg, err := decodeConnectorConfigItem(id, item)
		if err != nil {
			return fail()
		}
		if p.next != nil && sameDefinition(cfg, p.next.config) {
			verified = p.next
		} else if p.old != nil && sameDefinition(cfg, p.old.config) {
			verified = p.old
		} else {
			return fail()
		}
		copy := *verified
		copy.config = cfg
		verified = &copy
	}
	m.snapshotMu.Lock()
	if m.closed {
		m.snapshotMu.Unlock()
		return &configmgr.PendingMutationError{Mutation: p.state}
	}
	old := m.entries[id]
	m.generation++
	if verified == nil {
		delete(m.entries, id)
	} else {
		m.entries[id] = verified
	}
	if old != nil && (verified == nil || old.token != verified.token) {
		retired = append(retired, m.detachSession(id))
	}
	delete(m.pending, id)
	m.snapshotMu.Unlock()
	return nil
}
