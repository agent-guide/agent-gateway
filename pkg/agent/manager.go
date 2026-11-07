package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

// AgentRouteLookup resolves unified ingress routes that target an Agent. It is
// used to prevent deleting a definition while a route still references it.
type AgentRouteLookup interface {
	AgentRouteIDsForAgent(ctx context.Context, agentID string) ([]string, error)
}

// Manager owns agent CRUD, the deep-cloned definition snapshot used by
// per-request dispatch, and the in-memory resource-route -> agent index used
// for write-time attribution. The snapshot and index are rebuilt on every
// mutation and never read from the config store on the hot path.
type Manager struct {
	store                  configstore.ConfigStore
	routeLookup            AgentRouteLookup
	validateResourceRoutes func(context.Context, Routes) error
	guard                  configmgr.MutationGuard[Agent]

	// writeMu serializes the validate -> prospective-generation ->
	// store-write -> generation-commit sequence so the P0
	// one-runtime-one-agent invariant holds under concurrent mutations and
	// the committed generation always reflects the store write that just
	// succeeded. It is independent of mu (index maps) and snapMu (snapshot).
	writeMu sync.Mutex

	// snapMu guards the immutable definition generation. Listener preparation
	// and cleanup run outside it; only bounded in-memory listener commits share
	// the write-locked publication window.
	snapMu         sync.RWMutex
	snapshot       map[string]Agent
	snapshotLoaded bool
	generation     uint64
	listeners      []DefinitionListener
	pending        map[string]configmgr.PendingMutation
	recoveryClock  func() time.Time

	mu      sync.RWMutex
	byRoute map[string]string // exclusive MCP route id -> agent id
}

func NewManager(store configstore.ConfigStore, guards ...configmgr.MutationGuard[Agent]) *Manager {
	return &Manager{
		guard:    configmgr.SelectMutationGuard(guards),
		store:    store,
		snapshot: map[string]Agent{},
		byRoute:  map[string]string{},
	}
}

// SetRouteLookup wires the optional unified AgentRouteConfig reference lookup.
func (m *Manager) SetRouteLookup(lookup AgentRouteLookup) {
	if m == nil {
		return
	}
	m.routeLookup = lookup
}

// SetResourceRouteValidator wires control-plane dependency validation before
// the manager is used. The callback runs under writeMu and must not reenter it.
func (m *Manager) SetResourceRouteValidator(validate func(context.Context, Routes) error) {
	m.validateResourceRoutes = validate
}

// DeleteUnreferencedLLMRoute serializes the reference check and deletion with
// Agent writes. The callback must complete deletion before returning and must
// not reenter the Agent manager's mutation methods.
func (m *Manager) DeleteUnreferencedLLMRoute(ctx context.Context, id string, remove func(context.Context, string) error) error {
	return m.deleteUnreferencedResourceRoute(ctx, id, "llm", remove)
}

// DeleteUnreferencedMCPRoute serializes the reference check and deletion with
// Agent writes. The callback must complete deletion before returning and must
// not reenter the Agent manager's mutation methods.
func (m *Manager) DeleteUnreferencedMCPRoute(ctx context.Context, id string, remove func(context.Context, string) error) error {
	return m.deleteUnreferencedResourceRoute(ctx, id, "mcp", remove)
}

func (m *Manager) deleteUnreferencedResourceRoute(ctx context.Context, id, kind string, remove func(context.Context, string) error) error {
	if m == nil || m.store == nil {
		return ErrAgentNotConfigured
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	agents, err := m.List(ctx)
	if err != nil {
		return err
	}
	var refs []string
	for _, a := range agents {
		routeIDs := a.Routes.LLMRouteIDs
		if kind == "mcp" {
			routeIDs = a.Routes.MCPRouteIDs
		}
		if slices.Contains(routeIDs, id) {
			refs = append(refs, a.ID)
		}
	}
	if len(refs) > 0 {
		slices.Sort(refs)
		errReferenced := ErrLLMRouteReferenced
		if kind == "mcp" {
			errReferenced = ErrMCPRouteReferenced
		}
		return fmt.Errorf("%w: %s route %q is still referenced by agents: %s", errReferenced, kind, id, strings.Join(refs, ", "))
	}
	return remove(ctx, id)
}

func (m *Manager) List(ctx context.Context) ([]Agent, error) {
	if m == nil || m.store == nil {
		return nil, ErrAgentNotConfigured
	}
	items, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(items))
	for _, item := range items {
		cfg, err := decodeAgentItem("", item)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, nil
}

func (m *Manager) Get(ctx context.Context, id string) (Agent, error) {
	if id == "" {
		return Agent{}, fmt.Errorf("id is required")
	}
	if m == nil || m.store == nil {
		return Agent{}, ErrAgentNotConfigured
	}
	item, err := m.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return Agent{}, ErrAgentNotConfigured
		}
		return Agent{}, err
	}
	return decodeAgentItem(id, item)
}

func (m *Manager) Create(ctx context.Context, a Agent) error {
	if m == nil || m.store == nil {
		return ErrAgentNotConfigured
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationCreate, a.ID, &a, func(ctx context.Context) error { return m.createLocked(ctx, a) })
}

func (m *Manager) createLocked(ctx context.Context, a Agent) error {
	a.Normalize()
	if err := m.ensureWritableDefinition(a.ID); err != nil {
		return err
	}
	if err := a.Validate(); err != nil {
		return err
	}
	if m.validateResourceRoutes != nil {
		if err := m.validateResourceRoutes(ctx, a.Routes); err != nil {
			return err
		}
	}
	if err := m.checkMCPRouteUniqueness(ctx, a, ""); err != nil {
		return err
	}
	a.NormalizeTimestamps(time.Now().UTC())
	cloned, err := cloneAgent(a)
	if err != nil {
		return err
	}
	prospective, err := m.prospectiveGeneration(ctx, func(gen map[string]Agent) {
		gen[cloned.ID] = cloned
	})
	if err != nil {
		return err
	}
	persisted, err := m.persistVerified(ctx, a, configmgr.MutationCreate)
	if err != nil {
		return err
	}
	prospective[a.ID] = persisted
	if err := m.commitGeneration(ctx, prospective); err != nil {
		return m.pendingFailure(a.ID, err)
	}
	m.clearPending(a.ID)
	return nil
}

func (m *Manager) Update(ctx context.Context, id string, a Agent) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if m == nil || m.store == nil {
		return ErrAgentNotConfigured
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationUpdate, id, &a, func(ctx context.Context) error { return m.updateLocked(ctx, id, a) })
}

func (m *Manager) updateLocked(ctx context.Context, id string, a Agent) error {
	if err := m.ensureWritableDefinition(id); err != nil {
		return err
	}
	current, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	a.ID = id
	a.CreatedAt = current.CreatedAt
	a.Normalize()
	if err := a.Validate(); err != nil {
		return err
	}
	if m.validateResourceRoutes != nil {
		if err := m.validateResourceRoutes(ctx, a.Routes); err != nil {
			return err
		}
	}
	if err := m.checkMCPRouteUniqueness(ctx, a, id); err != nil {
		return err
	}
	a.NormalizeTimestamps(time.Now().UTC())
	cloned, err := cloneAgent(a)
	if err != nil {
		return err
	}
	prospective, err := m.prospectiveGeneration(ctx, func(gen map[string]Agent) {
		gen[cloned.ID] = cloned
	})
	if err != nil {
		return err
	}
	persisted, err := m.persistVerified(ctx, a, configmgr.MutationUpdate)
	if err != nil {
		return err
	}
	prospective[a.ID] = persisted
	if err := m.commitGeneration(ctx, prospective); err != nil {
		return m.pendingFailure(a.ID, err)
	}
	m.clearPending(a.ID)
	return nil
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if m == nil || m.store == nil {
		return ErrAgentNotConfigured
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationDelete, id, nil, func(ctx context.Context) error { return m.deleteLocked(ctx, id) })
}

func (m *Manager) deleteLocked(ctx context.Context, id string) error {
	if err := m.ensureWritableDefinition(id); err != nil {
		return err
	}
	if m.routeLookup != nil {
		routeIDs, err := m.routeLookup.AgentRouteIDsForAgent(ctx, id)
		if err != nil {
			return fmt.Errorf("check agent route references: %w", err)
		}
		if len(routeIDs) > 0 {
			return fmt.Errorf("%w: agent %q is targeted by agent route %q", ErrAgentRouteTarget, id, routeIDs[0])
		}
	}
	prospective, err := m.prospectiveGeneration(ctx, func(gen map[string]Agent) {
		delete(gen, id)
	})
	if err != nil {
		return err
	}
	if _, err := m.persistVerified(ctx, Agent{ID: id}, configmgr.MutationDelete); err != nil {
		return err
	}
	if err := m.commitGeneration(ctx, prospective); err != nil {
		return m.pendingFailure(id, err)
	}
	m.clearPending(id)
	return nil
}

// checkMCPRouteUniqueness preserves exclusive MCP attribution. LLM routes are
// shared dependencies; their callers are attributed by trusted execution identity.
func (m *Manager) checkMCPRouteUniqueness(ctx context.Context, a Agent, excludeID string) error {
	routeIDs := agentMCPRouteIDs(a)
	if len(routeIDs) == 0 {
		return nil
	}
	existing, err := m.List(ctx)
	if err != nil {
		return err
	}
	// A pending write can release a stored reference before its published
	// generation retires. Reserve both views until recovery completes.
	existing = append(existing, m.Snapshot()...)
	for _, other := range existing {
		if other.ID == excludeID || other.ID == a.ID {
			continue
		}
		for routeID := range agentMCPRouteIDs(other) {
			if _, ok := routeIDs[routeID]; ok {
				return fmt.Errorf("route %q is already bound by agent %q", routeID, other.ID)
			}
		}
	}
	return nil
}

// Refresh decodes and deep-clones the complete store result into a fresh
// definition generation, then commits it atomically. The derived
// resource-route -> agent attribution index is rebuilt as part of the commit.
func (m *Manager) Refresh(ctx context.Context) error {
	if m == nil || m.store == nil {
		return nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationRefresh, "", nil, m.refreshLocked)
}

// refreshLocked replaces the current definition generation from the store.
// The caller must hold writeMu so the List -> commit sequence cannot publish a
// stale store view after a concurrent Create, Update, or Delete has committed.
func (m *Manager) refreshLocked(ctx context.Context) error {
	agents, err := m.List(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]Agent, len(agents))
	for _, a := range agents {
		cloned, err := cloneAgent(a)
		if err != nil {
			return err
		}
		cloned.Normalize()
		if err := cloned.Validate(); err != nil {
			return fmt.Errorf("refresh agent %q: %w", cloned.ID, err)
		}
		next[cloned.ID] = cloned
	}
	if err := m.commitGeneration(ctx, next); err != nil {
		return m.publicationFailure(next, err)
	}
	m.snapMu.Lock()
	m.pending = nil
	m.snapMu.Unlock()
	return nil
}

// Recommit republishes the already-loaded definition generation without
// reading the Agent store. It is used when an external runtime record changes
// and definition listeners must rebuild derived runtime snapshots.
func (m *Manager) Recommit(ctx context.Context) error {
	if m == nil || m.store == nil {
		return nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return configmgr.GuardMutation(m.guard, ctx, configmgr.MutationRefresh, "", nil, m.recommitLocked)
}

func (m *Manager) recommitLocked(ctx context.Context) error {
	m.snapMu.RLock()
	loaded := m.snapshotLoaded
	m.snapMu.RUnlock()
	if !loaded {
		// The initial store load already commits and notifies listeners; a
		// second identical generation would add no reconciliation value.
		return m.refreshLocked(ctx)
	}
	m.snapMu.RLock()
	next := make(map[string]Agent, len(m.snapshot))
	for id, a := range m.snapshot {
		next[id] = a
	}
	m.snapMu.RUnlock()
	return m.commitGeneration(ctx, next)
}

// ResolveAgentID maps an originating MCP route back to a single agent for
// write-time usage attribution. The service/session arguments remain part of
// the protocol-neutral usage seam for MCP callers but do not identify an Agent.
// It returns ok=false when the route mapping is empty.
func (m *Manager) ResolveAgentID(routeID, serviceID, sessionID string) (string, bool) {
	_, _ = serviceID, sessionID
	if m == nil {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if routeID != "" {
		if id, ok := m.byRoute[routeID]; ok {
			return id, true
		}
	}
	return "", false
}

type storedAgent struct {
	cfg any
	tag string
}

func (c storedAgent) ConfigStoreObject() any { return c.cfg }
func (c storedAgent) ConfigStoreTag() string { return c.tag }

func decodeAgentItem(id string, item any) (Agent, error) {
	cfg, ok := item.(*Agent)
	if !ok || cfg == nil || cfg.ID == "" {
		if id == "" {
			id = "<unknown>"
		}
		return Agent{}, fmt.Errorf("agent %q has unexpected type %T", id, item)
	}
	cloned := *cfg
	return cloned, nil
}

func agentMCPRouteIDs(a Agent) map[string]struct{} {
	out := map[string]struct{}{}
	for _, routeID := range a.Routes.MCPRouteIDs {
		if routeID != "" {
			out[routeID] = struct{}{}
		}
	}
	return out
}
