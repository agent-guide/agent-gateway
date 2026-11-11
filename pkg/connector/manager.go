package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
	"github.com/google/uuid"
)

var ErrConnectorNotConfigured = errors.New("connector is not configured")

type entry struct {
	config      ConnectorConfig
	incarnation string
	connect     chan struct{}
	token       string
}
type Manager struct {
	ctx        context.Context
	cancel     context.CancelFunc
	active     map[string]int
	changed    chan struct{}
	closed     bool
	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErr   error
	store      configstore.ConfigStore
	pending    map[string]*pendingWrite
	writeMu    sync.Mutex
	snapshotMu sync.RWMutex
	entries    map[string]*entry
	loaded     bool
	generation uint64
	// Guard is supplied by application coordination; it surrounds persistence
	// and publication, including direct manager callers.
	Guard            func(context.Context, string, *ConnectorConfig, func() error) error
	mu               sync.Mutex
	discoverySession map[string]*discoverySession
	sessionsMu       sync.RWMutex
	sessions         map[string]*GatewaySession
}

func NewManager(store configstore.ConfigStore) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{ctx: ctx, cancel: cancel, active: map[string]int{}, changed: make(chan struct{}), closeDone: make(chan struct{}), store: store, pending: map[string]*pendingWrite{}, entries: map[string]*entry{}, discoverySession: map[string]*discoverySession{}, sessions: map[string]*GatewaySession{}}
}

func cloneConfig(c ConnectorConfig) ConnectorConfig {
	b, _ := json.Marshal(c)
	var out ConnectorConfig
	_ = json.Unmarshal(b, &out)
	return out
}
func (m *Manager) ensure(ctx context.Context) error {
	if m == nil || m.store == nil {
		return ErrConnectorNotConfigured
	}
	m.snapshotMu.RLock()
	loaded := m.loaded
	m.snapshotMu.RUnlock()
	if loaded {
		return nil
	}
	return m.Refresh(ctx)
}
func (m *Manager) prepare(c ConnectorConfig) (*entry, error) {
	c = cloneConfig(c)
	c.Normalize()
	if e := c.Validate(); e != nil {
		return nil, e
	}
	v := &entry{config: c, incarnation: uuid.NewString(), token: uuid.NewString(), connect: make(chan struct{}, 1)}
	return v, nil
}
func (m *Manager) Refresh(ctx context.Context) error {
	if m == nil || m.store == nil {
		return ErrConnectorNotConfigured
	}
	var retired []*discoverySession
	err := m.mutate(ctx, "", nil, func() error {
		m.writeMu.Lock()
		defer m.writeMu.Unlock()
		items, err := m.store.List(ctx)
		if err != nil {
			return err
		}
		next := map[string]*entry{}
		for _, item := range items {
			cfg, err := decodeServiceConfigItem("", item)
			if err != nil {
				return err
			}
			v, err := m.prepare(cfg)
			if err != nil {
				return fmt.Errorf("connector %q: %w; use offline configuration repair", cfg.ID, err)
			}
			next[cfg.ID] = v
		}
		m.snapshotMu.Lock()
		defer m.snapshotMu.Unlock()
		if m.closed {
			return fmt.Errorf("connector manager closed")
		}
		if len(m.pending) > 0 {
			return configmgr.ErrMutationPending
		}
		for id, old := range m.entries {
			v := next[id]
			if v != nil && old.config.Type != v.config.Type {
				return fmt.Errorf("connector type is immutable")
			}
			if v == nil || !ExecutionEqual(old.config, v.config) {
				if m.active[id] > 0 {
					return fmt.Errorf("%w: %s", ErrConnectorBusy, id)
				}
			} else {
				copy := *old
				copy.config = v.config
				next[id] = &copy
			}
		}
		for id, old := range m.entries {
			v := next[id]
			if v == nil || v.token != old.token {
				retired = append(retired, m.detachSession(id))
			}
		}
		m.entries = next
		m.loaded = true
		m.generation++
		return nil
	})
	m.closeSessions(retired)
	return err
}
func (m *Manager) lookup(ctx context.Context, id string) (*entry, error) {
	if e := m.ensure(ctx); e != nil {
		return nil, e
	}
	m.snapshotMu.RLock()
	v := m.entries[id]
	m.snapshotMu.RUnlock()
	if v == nil {
		return nil, ErrConnectorNotConfigured
	}
	return v, nil
}
func (m *Manager) List(ctx context.Context) ([]ConnectorConfig, error) {
	if e := m.ensure(ctx); e != nil {
		return nil, e
	}
	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()
	out := make([]ConnectorConfig, 0, len(m.entries))
	for _, v := range m.entries {
		out = append(out, cloneConfig(v.config))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (m *Manager) Get(ctx context.Context, id string) (ConnectorConfig, error) {
	if err := m.CheckExecution(id); err != nil {
		return ConnectorConfig{}, err
	}
	v, e := m.lookup(ctx, id)
	if e != nil {
		return ConnectorConfig{}, e
	}
	return cloneConfig(v.config), nil
}
func (m *Manager) mutate(ctx context.Context, id string, c *ConnectorConfig, apply func() error) error {
	if m.Guard != nil {
		return m.Guard(ctx, id, c, apply)
	}
	return apply()
}
func (m *Manager) Create(ctx context.Context, c ConnectorConfig) error {
	c.Normalize()
	return m.write(ctx, c.ID, c, false)
}
func (m *Manager) Update(ctx context.Context, id string, c ConnectorConfig) error {
	id = strings.TrimSpace(id)
	c.Normalize()
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if c.ID != "" && c.ID != id {
		return fmt.Errorf("connector id is immutable")
	}
	return m.write(ctx, id, c, true)
}
func (m *Manager) write(ctx context.Context, id string, c ConnectorConfig, update bool) error {
	if e := m.ensure(ctx); e != nil {
		return e
	}
	c.ID = id
	c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	var retired []*discoverySession
	err := m.mutate(ctx, id, &c, func() error {
		m.writeMu.Lock()
		defer m.writeMu.Unlock()
		m.snapshotMu.RLock()
		old := m.entries[id]
		m.snapshotMu.RUnlock()
		if update && old == nil {
			return ErrConnectorNotConfigured
		}
		if !update && old != nil {
			return fmt.Errorf("connector already exists")
		}
		if old != nil && old.config.Type != c.Type {
			return fmt.Errorf("connector type is immutable")
		}
		if old != nil {
			c.CreatedAt = old.config.CreatedAt
		}
		c.NormalizeTimestamps(time.Now().UTC())
		next, e := m.prepare(c)
		if e != nil {
			return e
		}
		if err := m.CheckExecution(id); err != nil {
			return err
		}
		action := configmgr.MutationCreate
		if update {
			action = configmgr.MutationUpdate
		}
		if old != nil {
			next.incarnation = old.incarnation
			if ExecutionEqual(old.config, next.config) {
				copy := *old
				copy.config = next.config
				next = &copy
			}
		}
		pending, err := m.beginPending(id, action, old, next)
		if err != nil {
			return err
		}
		var callbackErr error
		written := false
		persist := func(store configstore.ConfigStore) error {
			obj := storedConnectorConfig{cfg: &c, tag: c.Type}
			var err error
			if update {
				err = store.Update(ctx, obj)
			} else {
				err = store.Create(ctx, obj)
			}
			if err != nil {
				callbackErr = err
				return err
			}
			written = true
			item, err := store.Get(ctx, id)
			if err != nil {
				callbackErr = err
				return err
			}
			authoritative, err := decodeServiceConfigItem(id, item)
			if err != nil || !sameDefinition(authoritative, c) {
				callbackErr = fmt.Errorf("connector readback mismatch")
				return callbackErr
			}
			next.config = authoritative
			return nil
		}
		if tx, ok := m.store.(configstore.TransactionalStore); ok {
			e = tx.Transaction(ctx, persist)
			if e != nil && callbackErr != nil {
				m.clearPending(id)
				return fmt.Errorf("connector transaction rolled back")
			}
		} else {
			e = persist(m.store)
		}
		if e != nil {
			if !written && errors.Is(e, configstore.ErrNotFound) {
				m.clearPending(id)
				return e
			}
			return &configmgr.PendingMutationError{Mutation: pending.state}
		}

		m.snapshotMu.Lock()
		if m.closed {
			m.snapshotMu.Unlock()
			return &configmgr.PendingMutationError{Mutation: pending.state}
		}
		m.generation++
		if old != nil {
			next.incarnation = old.incarnation
		}
		m.entries[id] = next
		m.snapshotMu.Unlock()
		if old == nil || old.token != next.token {
			retired = append(retired, m.detachSession(id))
		}
		m.clearPending(id)
		return nil
	})
	m.closeSessions(retired)
	return err
}
func (m *Manager) Delete(ctx context.Context, id string) error {
	if e := m.ensure(ctx); e != nil {
		return e
	}
	var retired []*discoverySession
	err := m.mutate(ctx, id, nil, func() error {
		m.writeMu.Lock()
		defer m.writeMu.Unlock()
		if err := m.CheckExecution(id); err != nil {
			return err
		}
		m.snapshotMu.RLock()
		old := m.entries[id]
		m.snapshotMu.RUnlock()
		if old == nil {
			return configstore.ErrNotFound
		}
		pending, err := m.beginPending(id, configmgr.MutationDelete, old, nil)
		if err != nil {
			return err
		}
		var callbackErr error
		persist := func(store configstore.ConfigStore) error {
			err := store.Delete(ctx, id)
			if err != nil {
				callbackErr = err
				return err
			}
			if _, err = store.Get(ctx, id); !errors.Is(err, configstore.ErrNotFound) {
				callbackErr = fmt.Errorf("connector delete readback failed")
				return callbackErr
			}
			return nil
		}
		err = nil
		if tx, ok := m.store.(configstore.TransactionalStore); ok {
			err = tx.Transaction(ctx, persist)
			if err != nil && callbackErr != nil {
				m.clearPending(id)
				return fmt.Errorf("connector transaction rolled back")
			}
		} else {
			err = persist(m.store)
		}
		if err != nil {
			return &configmgr.PendingMutationError{Mutation: pending.state}
		}

		m.snapshotMu.Lock()
		if m.closed {
			m.snapshotMu.Unlock()
			return &configmgr.PendingMutationError{Mutation: pending.state}
		}
		delete(m.entries, id)
		m.generation++
		m.snapshotMu.Unlock()
		retired = append(retired, m.detachSession(id))
		m.clearPending(id)
		return nil
	})
	m.closeSessions(retired)
	return err
}
func (m *Manager) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.CloseContext(ctx)
}

type discoverySession struct {
	cancel          context.CancelFunc
	transport       transport.Caller
	configSignature string
	protocolVersion string
	initialize      initializeResult
	initializedAt   time.Time
	lastUsedAt      time.Time
}

type storedConnectorConfig struct {
	cfg any
	tag string
}

func (c storedConnectorConfig) ConfigStoreObject() any {
	return c.cfg
}

func (c storedConnectorConfig) ConfigStoreTag() string {
	return c.tag
}

func decodeServiceConfigItem(id string, item any) (ConnectorConfig, error) {
	cfg, ok := item.(*ConnectorConfig)
	if !ok || cfg == nil || cfg.ID == "" {
		if id == "" {
			id = "<unknown>"
		}
		return ConnectorConfig{}, fmt.Errorf("mcp service %q has unexpected type %T", id, item)
	}
	cloned := cloneConfig(*cfg)
	return cloned, nil
}

// GenerationVector is a process-local, secret-free materialization identity.
func (m *Manager) GenerationVector(ids []string) string {
	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()
	keys := append([]string(nil), ids...)
	sort.Strings(keys)
	var out string
	for _, id := range keys {
		if v := m.entries[id]; v != nil {
			out += id + ":" + v.token + ";"
		} else {
			out += id + ":missing;"
		}
	}
	return out
}

type State struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Generation string   `json:"generation"`
	Ready      bool     `json:"ready"`
	Reasons    []string `json:"reasons"`
}

func (m *Manager) State(ctx context.Context, id string) (State, error) {
	v, e := m.lookup(ctx, id)
	if e != nil {
		return State{}, e
	}
	s := State{ID: id, Type: v.config.Type, Generation: v.token, Ready: true, Reasons: []string{}}
	m.snapshotMu.RLock()
	closed := m.closed
	m.snapshotMu.RUnlock()
	if closed {
		s.Reasons = append(s.Reasons, "closed")
	}
	if v.config.Disabled {
		s.Reasons = append(s.Reasons, "disabled")
	}
	if _, pending := m.PendingMutation(id); pending {
		s.Reasons = append(s.Reasons, "mutation_pending")
	}
	s.Ready = len(s.Reasons) == 0
	return s, nil
}
