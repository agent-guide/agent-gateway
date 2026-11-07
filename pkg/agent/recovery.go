package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/google/uuid"
)

var ErrDefinitionRetired = errors.New("agent definition was replaced; resolve the current definition before execution")

func (m *Manager) PendingMutation(id string) *configmgr.PendingMutation {
	if m == nil {
		return nil
	}
	m.snapMu.RLock()
	defer m.snapMu.RUnlock()
	state, ok := m.pending[id]
	if !ok {
		return nil
	}
	return &state
}

func (m *Manager) CheckExecutionID(id string) error {
	if state := m.PendingMutation(id); state != nil {
		return &configmgr.PendingMutationError{Mutation: *state}
	}
	return nil
}

// CheckExecution fences both pending writes and a previously captured definition
// after recovery/replacement. It does not read persistence or lock mutations.
func (m *Manager) CheckExecution(a Agent) error {
	if m == nil {
		return ErrAgentNotConfigured
	}
	m.snapMu.RLock()
	if state, ok := m.pending[a.ID]; ok {
		m.snapMu.RUnlock()
		return &configmgr.PendingMutationError{Mutation: state}
	}
	current, ok := m.snapshot[a.ID]
	m.snapMu.RUnlock()
	if !ok {
		return ErrAgentNotConfigured
	}
	if !current.UpdatedAt.Equal(a.UpdatedAt) {
		return ErrDefinitionRetired
	}
	// Authoritative timestamps can collide in custom/coarse-resolution stores.
	// Compare the canonical definition as well; JSON also treats omitted empty
	// containers consistently across store and snapshot clone boundaries.
	expected, err := json.Marshal(current)
	if err != nil {
		return ErrDefinitionRetired
	}
	candidate, err := json.Marshal(a)
	if err != nil || !bytes.Equal(expected, candidate) {
		return ErrDefinitionRetired
	}
	return nil
}

func (m *Manager) beginVerification(id string, action configmgr.MutationAction) {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	if m.pending == nil {
		m.pending = map[string]configmgr.PendingMutation{}
	}
	m.pending[id] = configmgr.PendingMutation{OperationID: uuid.NewString(), ResourceID: id, Action: action, State: "verifying", StartedAt: time.Now().UTC()}
}

func (m *Manager) clearPending(id string) {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	delete(m.pending, id)
}

func (m *Manager) pendingFailure(id string, cause error) error {
	m.snapMu.Lock()
	defer m.snapMu.Unlock()
	state := m.pending[id]
	state.State = "pending_recovery"
	m.pending[id] = state
	return &configmgr.PendingMutationError{Mutation: state, Cause: cause}
}

// persistVerified rolls back transactional write/readback failures. Backends
// without transactions retain an explicit pending outcome after any write or
// authoritative readback error. A commit error is conservatively
// treated as uncertain if its callback completed a write successfully.
func (m *Manager) persistVerified(ctx context.Context, a Agent, action configmgr.MutationAction) (Agent, error) {
	m.beginVerification(a.ID, action)
	written := false
	var persisted Agent
	write := func(store configstore.ConfigStore) error {
		var err error
		if action == configmgr.MutationDelete {
			err = store.Delete(ctx, a.ID)
		} else if action == configmgr.MutationCreate {
			err = store.Create(ctx, storedAgent{cfg: &a, tag: a.Runtime.Type})
		} else {
			err = store.Update(ctx, storedAgent{cfg: &a, tag: a.Runtime.Type})
		}
		if err != nil {
			return err
		}
		written = true
		raw, err := store.Get(ctx, a.ID)
		if action == configmgr.MutationDelete {
			if errors.Is(err, configstore.ErrNotFound) {
				return nil
			}
			return fmt.Errorf("verify agent deletion: %v", err)
		}
		if err != nil {
			return err
		}
		persisted, err = decodeAgentItem(a.ID, raw)
		if err != nil {
			return err
		}
		if persisted.ID != a.ID {
			return fmt.Errorf("agent %q authoritative record has a different id", a.ID)
		}
		persisted, err = cloneAgent(persisted)
		if err != nil {
			return err
		}
		persisted.Normalize()
		return persisted.Validate()
	}
	if tx, ok := m.store.(configstore.TransactionalStore); ok {
		var callbackErr error
		err := tx.Transaction(ctx, func(store configstore.ConfigStore) error { callbackErr = write(store); return callbackErr })
		if err != nil {
			if callbackErr != nil || !written {
				m.clearPending(a.ID)
				return Agent{}, err
			}
			return Agent{}, m.pendingFailure(a.ID, err)
		}
	} else if err := write(m.store); err != nil {
		// A nontransactional store can commit before returning a write error.
		return Agent{}, m.pendingFailure(a.ID, err)
	}
	return persisted, nil
}

// Recover verifies persisted facts without issuing another write. Failed
// attempts retain the same operation identity; successful Refresh also recovers
// a complete generation. Repeating Recover after success is a no-op.
func (m *Manager) Recover(ctx context.Context, id string) error {
	if m == nil || m.store == nil {
		return ErrAgentNotConfigured
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationRefresh, id, nil, func(ctx context.Context) (returnErr error) {
		state := m.PendingMutation(id)
		if state == nil {
			return nil
		}
		now := time.Now()
		if m.recoveryClock != nil {
			now = m.recoveryClock()
		}
		if now.Before(state.RetryAfter) {
			return &configmgr.PendingMutationError{Mutation: *state}
		}
		defer func() {
			if returnErr != nil {
				m.snapMu.Lock()
				state := configmgr.BackoffRecovery(m.pending[id], now)
				m.pending[id] = state
				m.snapMu.Unlock()
				var pending *configmgr.PendingMutationError
				if errors.As(returnErr, &pending) {
					pending.Mutation = state
				}
			}
		}()
		stored, err := m.Get(ctx, id)
		missing := errors.Is(err, ErrAgentNotConfigured) || errors.Is(err, configstore.ErrNotFound)
		if err != nil && !missing {
			return m.pendingFailure(id, err)
		}
		if !missing {
			if stored.ID != id {
				return m.pendingFailure(id, fmt.Errorf("agent %q authoritative record has a different id", id))
			}
			stored, err = cloneAgent(stored)
			if err != nil {
				return m.pendingFailure(id, err)
			}
			stored.Normalize()
			if err := stored.Validate(); err != nil {
				return m.pendingFailure(id, err)
			}
			if m.validateResourceRoutes != nil {
				if err := m.validateResourceRoutes(ctx, stored.Routes); err != nil {
					return m.pendingFailure(id, err)
				}
			}
			if err := m.checkMCPRouteUniqueness(ctx, stored, id); err != nil {
				return m.pendingFailure(id, err)
			}
		}
		next, err := m.prospectiveGeneration(ctx, func(gen map[string]Agent) {
			if missing {
				delete(gen, id)
			} else {
				gen[id] = stored
			}
		})
		if err != nil {
			return m.pendingFailure(id, err)
		}
		if err := m.commitGeneration(ctx, next); err != nil {
			return m.pendingFailure(id, err)
		}
		m.clearPending(id)
		return nil
	})
}

func (m *Manager) ensureWritableDefinition(id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	return m.CheckExecutionID(id)
}

func (m *Manager) publicationFailure(next map[string]Agent, cause error) error {
	var changed []string
	m.snapMu.RLock()
	for id, a := range next {
		if old, ok := m.snapshot[id]; !ok || !reflect.DeepEqual(old, a) {
			changed = append(changed, id)
		}
	}
	for id := range m.snapshot {
		if _, ok := next[id]; !ok {
			changed = append(changed, id)
		}
	}
	m.snapMu.RUnlock()
	sort.Strings(changed)
	var failures []error
	for _, id := range changed {
		if m.PendingMutation(id) == nil {
			m.beginVerification(id, configmgr.MutationRefresh)
		}
		failures = append(failures, m.pendingFailure(id, cause))
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	return cause
}
