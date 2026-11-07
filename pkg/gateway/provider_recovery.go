package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/google/uuid"
)

func (m *ProviderManager) PendingMutation(id string) *configmgr.PendingMutation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.pending[id]
	if !ok {
		return nil
	}
	return &state
}
func (m *ProviderManager) checkPending(id string) error {
	if state := m.PendingMutation(id); state != nil {
		return &configmgr.PendingMutationError{Mutation: *state}
	}
	return nil
}
func (m *ProviderManager) beginDeletion(id string) {
	m.beginVerification(id, configmgr.MutationDelete)
}

func (m *ProviderManager) beginVerification(id string, action configmgr.MutationAction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		m.pending = map[string]configmgr.PendingMutation{}
	}
	m.pending[id] = configmgr.PendingMutation{OperationID: uuid.NewString(), ResourceID: id, Action: action, State: "verifying", StartedAt: time.Now().UTC()}
}

// writeProvider verifies authoritative storage before publishing either kind of
// write. Callback failures roll back; errors after a successful callback retain
// the recovery gate because the commit outcome cannot be inferred from them.
func (c *controlPlane) writeProvider(ctx context.Context, action configmgr.MutationAction, id string, next *provider.ProviderConfig) error {
	if next == nil || next.ProviderType == "" {
		return fmt.Errorf("provider_type is required")
	}
	if err := validateProviderTypeEnabled(next.ProviderType); err != nil {
		return err
	}
	m := c.providers
	if m.store == nil {
		return fmt.Errorf("provider store is not configured")
	}
	cfg := provider.NormalizeConfig(*next, id, "")
	if action == configmgr.MutationUpdate {
		current, err := m.GetConfig(ctx, id)
		if err != nil {
			return err
		}
		cfg.CreatedAt = current.CreatedAt
	}
	cfg = normalizeProviderConfigTimestamps(cfg, time.Now().UTC(), action == configmgr.MutationCreate)
	m.beginVerification(id, action)
	var persisted provider.ProviderConfig
	write := func(store configstore.ConfigStore) error {
		var err error
		if action == configmgr.MutationCreate {
			err = store.Create(ctx, &cfg)
		} else {
			err = store.Update(ctx, &cfg)
		}
		if err != nil {
			return err
		}
		raw, err := store.Get(ctx, id)
		if err != nil {
			return err
		}
		persisted, err = decodeProviderConfigManagerItem(id, raw)
		if err == nil && persisted.Id != id {
			return fmt.Errorf("Provider authoritative record has a different id")
		}
		return err
	}
	if tx, ok := c.backend.(configstore.TransactionalBackend); ok {
		var callbackErr error
		completed := false
		err := tx.Transaction(ctx, []string{schema.StoreProviders}, func(stores map[string]configstore.ConfigStore) error {
			callbackErr = write(stores[schema.StoreProviders])
			completed = callbackErr == nil
			return callbackErr
		})
		if err != nil {
			if errors.Is(err, configstore.ErrTransactionsUnsupported) && callbackErr == nil && !completed {
				// Transaction capability is optional for single-family writes.
				// Unsupported means the callback never ran, so a direct write is
				// safe; any error from that write still requires recovery.
				if err := write(m.store); err != nil {
					return m.pendingFailure(id, err)
				}
			} else if callbackErr != nil || !completed {
				m.clearPending(id)
				return err
			} else {
				return m.pendingFailure(id, err)
			}
		}
	} else if err := write(m.store); err != nil {
		// A nontransactional store may commit before returning an error.
		return m.pendingFailure(id, err)
	}
	if err := c.publishProviderConfig(id, persisted); err != nil {
		return m.pendingFailure(id, err)
	}
	m.clearPending(id)
	return nil
}

func (c *controlPlane) publishProviderConfig(id string, cfg provider.ProviderConfig) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("Provider cache publication failed")
		}
	}()
	c.providers.configs.Cache(cfg)
	c.providers.deleteDynamicCache(id)
	modelcatalog.InvalidateProvider(c.models, id)
	return nil
}
func (m *ProviderManager) clearPending(id string) { m.mu.Lock(); delete(m.pending, id); m.mu.Unlock() }
func (m *ProviderManager) pendingFailure(id string, cause error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.pending[id]
	state.State = "pending_recovery"
	m.pending[id] = state
	return &configmgr.PendingMutationError{Mutation: state, Cause: cause}
}

func (m *ProviderManager) Recover(ctx context.Context, id string) error {
	if m.PendingMutation(id) == nil {
		return nil
	}
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationRefresh, id, nil, func(context.Context) error { return fmt.Errorf("Provider recovery coordinator is not configured") })
}

func (c *controlPlane) recoverProvider(ctx context.Context, id string) (returnErr error) {
	state := c.providers.PendingMutation(id)
	if state == nil {
		return nil
	}
	now := time.Now()
	if c.providers.recoveryClock != nil {
		now = c.providers.recoveryClock()
	}
	if now.Before(state.RetryAfter) {
		return &configmgr.PendingMutationError{Mutation: *state}
	}
	defer func() {
		if returnErr != nil {
			c.providers.mu.Lock()
			state := configmgr.BackoffRecovery(c.providers.pending[id], now)
			c.providers.pending[id] = state
			c.providers.mu.Unlock()
			var pending *configmgr.PendingMutationError
			if errors.As(returnErr, &pending) {
				pending.Mutation = state
			}
		}
	}()
	raw, err := c.providers.store.Get(ctx, id)
	missing := errors.Is(err, configstore.ErrNotFound)
	if err != nil && !missing {
		return c.providers.pendingFailure(id, err)
	}
	if missing && c.modelStore != nil {
		items, err := c.modelStore.List(ctx)
		if err != nil {
			return c.providers.pendingFailure(id, err)
		}
		for _, item := range items {
			model, ok := item.(*modelcatalog.ManagedModel)
			if !ok || model == nil {
				return c.providers.pendingFailure(id, fmt.Errorf("unexpected managed model type %T", item))
			}
			if model.ProviderID == id {
				return c.providers.pendingFailure(id, fmt.Errorf("deleted Provider still has managed models; inspect persisted configuration"))
			}
		}
	}
	if missing {
		if err := c.publishProviderDeletion(id); err != nil {
			return c.providers.pendingFailure(id, err)
		}
	} else {
		cfg, err := decodeProviderConfigManagerItem(id, raw)
		if err != nil {
			return c.providers.pendingFailure(id, err)
		}
		if cfg.Id != id {
			return c.providers.pendingFailure(id, fmt.Errorf("Provider authoritative record has a different id"))
		}
		if err := c.publishProviderConfig(id, cfg); err != nil {
			return c.providers.pendingFailure(id, err)
		}
	}
	c.providers.clearPending(id)
	return nil
}

func (c *controlPlane) publishProviderDeletion(id string) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("Provider cache publication failed")
		}
	}()
	c.providers.configs.Evict(id)
	c.providers.deleteDynamicCache(id)
	modelcatalog.InvalidateProvider(c.models, id)
	return nil
}
