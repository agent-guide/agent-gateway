package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	acpservice "github.com/agent-guide/agent-gateway/pkg/acp/service"
)

type Manager struct {
	services *acpservice.Manager
	active   *ActivityTracker

	mu        sync.Mutex
	instances map[string]*managedInstance
}

type managedInstance struct {
	instance *instance
	lastUsed time.Time
}

func NewManager(services *acpservice.Manager) *Manager {
	return &Manager{
		services:  services,
		active:    NewActivityTracker(),
		instances: map[string]*managedInstance{},
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
	m.mu.Lock()
	if item := m.instances[scope]; item != nil && item.instance != nil {
		item.lastUsed = time.Now().UTC()
		inst := item.instance
		m.mu.Unlock()
		return inst, nil
	}
	m.mu.Unlock()

	inst, err := newInstance(ctx, cfg, req)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if item := m.instances[scope]; item != nil && item.instance != nil {
		item.lastUsed = time.Now().UTC()
		existing := item.instance
		m.mu.Unlock()
		_ = inst.close()
		return existing, nil
	}
	m.instances[scope] = &managedInstance{instance: inst, lastUsed: time.Now().UTC()}
	m.mu.Unlock()
	return inst, nil
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
