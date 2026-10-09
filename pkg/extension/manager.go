package extension

import (
	"context"
	"errors"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/google/uuid"
	"sync"
	"time"
)

const LifecycleBudget = 5 * time.Second

// Persistence is implemented by the existing schema-bound ConfigStore adapter.
// Operations must honor context; failed writes must leave persisted state intact.
type Persistence interface {
	List(context.Context) ([]Envelope, error)
	Put(context.Context, Envelope, bool) error
	Delete(context.Context, string) error
}

// Manager serializes mutations and publishes complete immutable generations.
// It never resolves registries or reads storage during snapshot acquisition.
type Manager struct {
	sequence        uint64
	publisher       event.Publisher
	registry        *Registry
	store           Persistence
	gate            chan struct{}
	mu              sync.Mutex
	current         *generation
	closed          bool
	unhealthy       bool
	retired         []*generation
	activeCancel    context.CancelFunc
	activeDone      chan struct{}
	shutdownContext context.Context
}
type generation struct {
	stateReset   bool
	snapshot     Snapshot
	order        []Envelope
	cancel       context.CancelFunc
	refs         int
	retiring     bool
	drained      chan struct{}
	done         chan struct{}
	retireCancel context.CancelFunc
}

// Lease retains an admitted generation until Release. Release is idempotent.
type Lease struct {
	Snapshot Snapshot
	m        *Manager
	g        *generation
	once     sync.Once
}

func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.m.mu.Lock()
		defer l.m.mu.Unlock()
		l.g.refs--
		if l.g.retiring && l.g.refs == 0 {
			close(l.g.drained)
		}
	})
}
func budget(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, LifecycleBudget)
}

// NewManager rejects static/dynamic conflicts and prepares all effective config
// before returning. Startup failure closes all resources already prepared.
func NewManager(ctx context.Context, r *Registry, store Persistence, static []Envelope) (*Manager, error) {
	if r == nil {
		return nil, failure("invalid_registration", "", "registry")
	}
	m := &Manager{registry: r, store: store, gate: make(chan struct{}, 1)}
	m.gate <- struct{}{}
	effective := make([]Envelope, 0, len(static))
	for _, e := range static {
		e = cloneEnvelope(e)
		e.Source = "static"
		effective = append(effective, e)
	}
	if store != nil {
		dynamic, err := store.List(ctx)
		if err != nil {
			return nil, failure("persistence_failed", "", "")
		}
		for _, e := range dynamic {
			e = cloneEnvelope(e)
			e.Source = "dynamic"
			effective = append(effective, e)
		}
	}
	g, err := m.prepare(ctx, effective)
	if err != nil {
		return nil, err
	}
	m.current = g
	return m, nil
}
func (m *Manager) lock(ctx context.Context) (context.Context, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.gate:
	}
	m.mu.Lock()
	if m.closed || ctx.Err() != nil {
		m.mu.Unlock()
		m.gate <- struct{}{}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure("closed", "", "")
	}
	active, cancel := context.WithCancel(ctx)
	m.activeCancel = cancel
	m.activeDone = make(chan struct{})
	m.mu.Unlock()
	return active, nil
}
func (m *Manager) unlock() {
	m.mu.Lock()
	m.activeCancel()
	m.activeCancel = nil
	close(m.activeDone)
	m.activeDone = nil
	m.mu.Unlock()
	m.gate <- struct{}{}
}
func (m *Manager) prepare(ctx context.Context, envelopes []Envelope) (*generation, error) {
	ctx, stop := budget(ctx)
	defer stop()
	graph := map[string][]string{}
	ordered, err := m.registry.validate(ctx, envelopes, graph)
	if err != nil {
		return nil, err
	}
	m.sequence++
	life, cancel := context.WithCancel(context.Background())
	g := &generation{snapshot: Snapshot{sequence: m.sequence, configs: map[string]Envelope{}, resources: map[string]Resource{}, ctx: life}, order: ordered, cancel: cancel, drained: make(chan struct{}), done: make(chan struct{})}
	fail := func(err error) (*generation, error) { g.cancel(); m.dispose(g); return nil, err }
	for _, e := range ordered {
		if ctx.Err() != nil {
			return fail(failure("prepare_failed", e.ModuleID, "deadline"))
		}
		f := m.registry.entries[e.ModuleID].Factory
		if f.Prepare == nil {
			return fail(failure("unknown_module", e.ModuleID, "runtime"))
		}
		cfg, err := resolve(e, f)
		if err != nil {
			return fail(err)
		}
		// Each callback receives owned metadata; Snapshot copies its maps so a
		// preparation cannot observe later additions by the assembly loop.
		deps := Snapshot{configs: map[string]Envelope{}, resources: map[string]Resource{}, ctx: life}
		for _, id := range graph[e.ModuleID] {
			if c, ok := g.snapshot.configs[id]; ok {
				deps.configs[id] = cloneEnvelope(c)
			}
			if r, ok := g.snapshot.resources[id]; ok {
				deps.resources[id] = r
			}
		}
		resource, err := callback(func() (Resource, error) { return f.Prepare(ctx, cfg, deps) })
		if !nilResource(resource) {
			g.snapshot.resources[e.ModuleID] = resource
		}
		if err != nil || nilResource(resource) || ctx.Err() != nil {
			return fail(failure("prepare_failed", e.ModuleID, ""))
		}
		if len(f.AdminRoutes) > 0 {
			if _, ok := resource.(AdminContributor); !ok {
				return fail(failure("prepare_failed", e.ModuleID, "admin_routes"))
			}
		}
		if err := healthResource(ctx, resource); err != nil || ctx.Err() != nil {
			return fail(failure("unhealthy", e.ModuleID, ""))
		}
		g.snapshot.configs[e.ModuleID] = cloneEnvelope(e)
	}
	if ctx.Err() != nil {
		return fail(failure("prepare_failed", "", "deadline"))
	}
	m.mu.Lock()
	previous := m.current
	m.mu.Unlock()
	if previous != nil {
		for id, r := range g.snapshot.resources {
			if state, ok := r.(StatefulResource); ok {
				reset, err := callback(func() (bool, error) { return state.StateResetComparedTo(ctx, previous.snapshot.resources[id]) })
				if err != nil {
					return fail(failure("prepare_failed", id, "state"))
				}
				g.stateReset = g.stateReset || reset
			}
		}
		for id, r := range previous.snapshot.resources {
			if _, ok := r.(StatefulResource); ok && g.snapshot.resources[id] == nil {
				g.stateReset = true
			}
		}
	}
	return g, nil
}
func (m *Manager) dispose(g *generation) {
	parent := context.Background()
	m.mu.Lock()
	if m.shutdownContext != nil {
		parent = m.shutdownContext
	}
	m.mu.Unlock()
	ctx, stop := budget(parent)
	defer stop()
	g.cancel()
	for i := len(g.order) - 1; i >= 0; i-- {
		if r := g.snapshot.resources[g.order[i].ModuleID]; !nilResource(r) {
			if err := closeResource(ctx, r); err != nil {
				m.mu.Lock()
				m.unhealthy = true
				m.mu.Unlock()
			}
		}
	}
}
func (m *Manager) publish(g *generation) {
	m.mu.Lock()
	old := m.current
	m.current = g
	m.retireLocked(old, context.Background())
	if m.closed {
		m.retireLocked(g, m.shutdownContext)
	}
	m.mu.Unlock()
}
func (m *Manager) retireLocked(g *generation, parent context.Context) {
	if g == nil || g.retiring {
		return
	}
	g.retiring = true
	if g.refs == 0 {
		close(g.drained)
	}
	pending := m.retired[:0]
	for _, retired := range m.retired {
		select {
		case <-retired.done:
		default:
			pending = append(pending, retired)
		}
	}
	m.retired = append(pending, g)
	ctx, stop := budget(parent)
	g.retireCancel = stop
	go func() {
		defer stop()
		select {
		case <-g.drained:
		case <-ctx.Done():
		}
		g.cancel()
		for i := len(g.order) - 1; i >= 0; i-- {
			if r := g.snapshot.resources[g.order[i].ModuleID]; !nilResource(r) {
				if err := closeResource(ctx, r); err != nil {
					m.mu.Lock()
					m.unhealthy = true
					m.mu.Unlock()
				}
			}
		}
		close(g.done)
	}()
}

// Acquire performs only generation ownership bookkeeping. No registry/storage
// reads or callback invocations occur on this path.
func (m *Manager) Acquire() (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, failure("closed", "", "")
	}
	g := m.current
	g.refs++
	return &Lease{Snapshot: g.snapshot, m: m, g: g}, nil
}
func (m *Manager) List() []Envelope {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Envelope, 0, len(m.current.order))
	for _, e := range m.current.order {
		out = append(out, cloneEnvelope(e))
	}
	return out
}
func (m *Manager) Manifest() Manifest { return m.registry.Manifest() }

// Put prepares a complete dependency generation, persists only the changed
// envelope, then performs an infallible in-memory publication.
func (m *Manager) Put(ctx context.Context, e Envelope) (result error) {
	active, err := m.lock(ctx)
	if err != nil {
		m.emitMutation(ctx, e.ModuleID, "put", err)
		return err
	}
	ctx = active
	defer m.unlock()
	// Publish before releasing the mutation gate and notifying shutdown waiters.
	// The publisher enqueues without sink I/O; no manager mutex is held.
	stateReset := false
	defer func() { m.emitMutation(ctx, e.ModuleID, "put", result, stateReset) }()
	if !ValidID(e.ModuleID) {
		return failure("invalid_config", "", "module_id")
	}
	input := m.List()
	found := false
	for i, old := range input {
		if old.ModuleID == e.ModuleID {
			if old.Source == "static" {
				return failure("static_config", e.ModuleID, "")
			}
			e.Source = "dynamic"
			input[i] = cloneEnvelope(e)
			found = true
		}
	}
	if !found {
		e.Source = "dynamic"
		input = append(input, cloneEnvelope(e))
	}
	g, err := m.prepare(ctx, input)
	if err != nil {
		return err
	}
	desired := g.snapshot.configs[e.ModuleID]
	desired.Source = ""
	if ctx.Err() != nil {
		m.dispose(g)
		return ctx.Err()
	}
	if m.store == nil || m.store.Put(ctx, desired, !found) != nil {
		m.dispose(g)
		return failure("persistence_failed", e.ModuleID, "")
	}
	m.publish(g)
	stateReset = g.stateReset
	return nil
}

// Delete fails when any configured module still depends on the removed one.
func (m *Manager) Delete(ctx context.Context, id string) (result error) {
	active, err := m.lock(ctx)
	if err != nil {
		m.emitMutation(ctx, id, "delete", err)
		return err
	}
	ctx = active
	defer m.unlock()
	// Publish before releasing the mutation gate and notifying shutdown waiters.
	// The publisher enqueues without sink I/O; no manager mutex is held.
	stateReset := false
	defer func() { m.emitMutation(ctx, id, "delete", result, stateReset) }()
	if !ValidID(id) {
		return failure("invalid_config", "", "module_id")
	}
	input := m.List()
	found := false
	candidate := []Envelope{}
	for _, e := range input {
		if e.ModuleID == id {
			found = true
			if e.Source == "static" {
				return failure("static_config", id, "")
			}
		} else {
			candidate = append(candidate, e)
		}
	}
	if !found {
		return failure("not_found", id, "")
	}
	g, err := m.prepare(ctx, candidate)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		m.dispose(g)
		return ctx.Err()
	}
	if m.store == nil || m.store.Delete(ctx, id) != nil {
		m.dispose(g)
		return failure("persistence_failed", id, "")
	}
	m.publish(g)
	stateReset = g.stateReset
	return nil
}

// Health checks the admitted generation with the common bounded budget.
func (m *Manager) Health(ctx context.Context) error {
	ctx, stop := budget(ctx)
	defer stop()
	m.mu.Lock()
	unhealthy := m.unhealthy
	m.mu.Unlock()
	if unhealthy {
		return failure("unhealthy", "", "lifecycle")
	}
	lease, err := m.Acquire()
	if err != nil {
		return err
	}
	defer lease.Release()
	for _, e := range lease.g.order {
		if r, ok := lease.Snapshot.Resource(e.ModuleID); ok {
			if err := healthResource(ctx, r); err != nil {
				return failure("unhealthy", e.ModuleID, "")
			}
		}
	}
	return ctx.Err()
}

// Close stops admissions, retires all generations, and waits within caller and
// five-second deadlines. Module cancellation violations return deadline errors.
func (m *Manager) Close(ctx context.Context) error {
	ctx, stop := budget(ctx)
	defer stop()
	// Stop admissions and cancel preparation before waiting for a mutator. A
	// successful write already in flight still finalizes its committed generation,
	// which publish immediately retires instead of admitting new readers.
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		m.shutdownContext = ctx
	}
	if m.activeCancel != nil {
		m.activeCancel()
	}
	activeDone := m.activeDone
	m.retireLocked(m.current, m.shutdownContext)
	m.mu.Unlock()
	cancelRetirement := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, g := range m.retired {
			g.retireCancel()
		}
	}
	stopCancellation := context.AfterFunc(ctx, cancelRetirement)
	defer stopCancellation()
	if activeDone != nil {
		select {
		case <-activeDone:
		case <-ctx.Done():
			cancelRetirement()
			return ctx.Err()
		}
	}
	m.mu.Lock()
	retired := append([]*generation(nil), m.retired...)
	m.mu.Unlock()
	for _, g := range retired {
		select {
		case <-g.done:
		case <-ctx.Done():
			cancelRetirement()
			return ctx.Err()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.mu.Lock()
	unhealthy := m.unhealthy
	m.mu.Unlock()
	if unhealthy {
		return failure("unhealthy", "", "lifecycle")
	}
	return nil
}

// SetPublisher binds the existing pipeline authority during bootstrap.
func (m *Manager) SetPublisher(p event.Publisher) { m.mu.Lock(); m.publisher = p; m.mu.Unlock() }
func (m *Manager) emitMutation(ctx context.Context, id, operation string, err error, reset ...bool) {
	if !ValidID(id) {
		return
	}
	m.mu.Lock()
	p := m.publisher
	m.mu.Unlock()
	if p == nil {
		return
	}
	outcome, reason := "success", ""
	if err != nil {
		outcome, reason = "failure", "mutation_failed"
		var e *Error
		if errors.As(err, &e) {
			switch e.Code {
			case "static_config":
				reason = "static_conflict"
			case "invalid_config", "unknown_module", "schema_mismatch", "missing_dependency", "dependency_cycle", "prepare_failed", "persistence_failed", "closed", "unhealthy":
				reason = e.Code
			}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reason = "canceled"
		}
	}
	v, _ := attribution.FromContext(ctx)
	p.Publish(ctx, event.Envelope{SchemaVersion: 1, ID: uuid.NewString(), Family: "extension.mutation", Timestamp: time.Now().UTC(), Attribution: v, Outcome: outcome, Reason: reason, Mutation: &event.ExtensionMutation{ModuleID: id, Operation: operation, StateReset: err == nil && len(reset) > 0 && reset[0]}})
}
