package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	acpservice "github.com/agent-guide/agent-gateway/pkg/acp/service"
)

const defaultJanitorInterval = 30 * time.Second

type Manager struct {
	services *acpservice.Manager
	active   *ActivityTracker

	janitorInterval time.Duration

	mu        sync.Mutex
	instances map[string]*managedInstance

	closeOnce sync.Once
	done      chan struct{}
}

type managedInstance struct {
	instance *instance
	idleTTL  time.Duration
	lastUsed time.Time
}

func NewManager(services *acpservice.Manager) *Manager {
	m := &Manager{
		services:        services,
		active:          NewActivityTracker(),
		janitorInterval: defaultJanitorInterval,
		instances:       map[string]*managedInstance{},
		done:            make(chan struct{}),
	}
	go m.janitor()
	return m
}

// Close stops the janitor and tears down every pooled instance, killing the
// underlying agent subprocesses. It is safe to call more than once.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.closeOnce.Do(func() { close(m.done) })
	m.mu.Lock()
	victims := make([]*instance, 0, len(m.instances))
	for scope, item := range m.instances {
		if item != nil && item.instance != nil {
			victims = append(victims, item.instance)
		}
		delete(m.instances, scope)
	}
	m.mu.Unlock()
	for _, inst := range victims {
		_ = inst.close()
	}
}

func (m *Manager) ServeTurn(ctx context.Context, serviceID string, req TurnRequest, emit EventSink) error {
	if m == nil || m.services == nil {
		return fmt.Errorf("acp runtime manager is not configured")
	}
	cfg, err := m.services.Get(ctx, strings.TrimSpace(serviceID))
	if err != nil {
		return err
	}
	if cfg.Disabled {
		return fmt.Errorf("acp service %q is disabled", cfg.ID)
	}
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Input = strings.TrimSpace(req.Input)
	if req.ThreadID == "" {
		return fmt.Errorf("thread_id is required")
	}
	if req.Input == "" {
		return fmt.Errorf("input is required")
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = cfg.CWD
	}
	if err := acpservice.ValidateCWDAllowed(cwd, cfg.AllowedRoots); err != nil {
		return err
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = cfg.DefaultModel
	}
	scope := cfg.ID + "\x00" + cwd + "\x00" + req.ThreadID + "\x00" + req.SessionID + "\x00" + model
	release, err := m.active.Begin(scope)
	if err != nil {
		return err
	}
	defer release()

	inst, err := m.resolveInstance(ctx, scope, cfg, req)
	if err != nil {
		return err
	}
	stopReason, err := inst.prompt(ctx, req, emit)
	if err != nil {
		return err
	}
	if emit != nil {
		return emit(TurnEvent{Event: "done", StopReason: stopReason})
	}
	return nil
}

func (m *Manager) ListInFlight() []InFlightTurn {
	if m == nil || m.active == nil {
		return nil
	}
	return m.active.List()
}

func (m *Manager) resolveInstance(ctx context.Context, scope string, cfg acpservice.ServiceConfig, req TurnRequest) (*instance, error) {
	// ServeTurn holds the per-scope activity lock for the whole turn, so only one
	// turn resolves a given scope at a time and the janitor skips active scopes;
	// no other goroutine mutates this scope's pool entry while we are here.
	m.mu.Lock()
	if item := m.instances[scope]; item != nil && item.instance != nil {
		// Reuse only a live instance, and never when the caller asked for a fresh
		// session. Otherwise evict and tear down the stale/dead instance.
		if !req.FreshSession && item.instance.alive() {
			item.lastUsed = time.Now().UTC()
			inst := item.instance
			m.mu.Unlock()
			return inst, nil
		}
		delete(m.instances, scope)
		stale := item.instance
		m.mu.Unlock()
		_ = stale.close()
	} else {
		m.mu.Unlock()
	}

	inst, err := newInstance(ctx, cfg, req)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.instances[scope] = &managedInstance{instance: inst, idleTTL: cfg.IdleTTL, lastUsed: time.Now().UTC()}
	m.mu.Unlock()
	return inst, nil
}

func (m *Manager) janitor() {
	ticker := time.NewTicker(m.janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			m.reapIdle(time.Now().UTC())
		}
	}
}

// reapIdle tears down pooled instances that are idle beyond their IdleTTL or
// whose process has already exited. Instances with an active turn are left
// alone; the active turn owns them until it releases the scope.
func (m *Manager) reapIdle(now time.Time) {
	var victims []*instance
	m.mu.Lock()
	for scope, item := range m.instances {
		if item == nil || item.instance == nil {
			delete(m.instances, scope)
			continue
		}
		if !shouldReap(now, item.lastUsed, item.idleTTL, item.instance.alive(), m.active.IsActive(scope)) {
			continue
		}
		victims = append(victims, item.instance)
		delete(m.instances, scope)
	}
	m.mu.Unlock()
	for _, inst := range victims {
		_ = inst.close()
	}
}

// shouldReap reports whether a pooled instance can be torn down. An instance
// with an active turn is never reaped. A dead transport is always reaped. A
// live, idle instance is reaped only when idleTTL > 0 and it has been idle for
// longer than idleTTL.
func shouldReap(now, lastUsed time.Time, idleTTL time.Duration, alive, active bool) bool {
	if active {
		return false
	}
	if !alive {
		return true
	}
	if idleTTL <= 0 {
		return false
	}
	return now.Sub(lastUsed) > idleTTL
}

type InFlightTurn struct {
	Scope string `json:"scope"`
}

type ActivityTracker struct {
	mu     sync.Mutex
	active map[string]struct{}
}

func NewActivityTracker() *ActivityTracker {
	return &ActivityTracker{active: map[string]struct{}{}}
}

func (t *ActivityTracker) Begin(scope string) (func(), error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, fmt.Errorf("scope is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.active[scope]; exists {
		return nil, fmt.Errorf("acp turn is already active for scope")
	}
	t.active[scope] = struct{}{}
	return func() {
		t.mu.Lock()
		delete(t.active, scope)
		t.mu.Unlock()
	}, nil
}

func (t *ActivityTracker) IsActive(scope string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.active[strings.TrimSpace(scope)]
	return ok
}

func (t *ActivityTracker) List() []InFlightTurn {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]InFlightTurn, 0, len(t.active))
	for scope := range t.active {
		out = append(out, InFlightTurn{Scope: scope})
	}
	return out
}
