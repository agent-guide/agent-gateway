package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/acp/agentspi"
	acpservice "github.com/agent-guide/agent-gateway/pkg/acp/service"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

const fakePoolAgent = "fake-pool"

var fakeOpenCount int32

func init() {
	agentspi.Register(fakePoolAgent, func(agentspi.OpenRequest) (agentspi.Agent, error) {
		return &fakePoolAgentImpl{}, nil
	})
}

type fakePoolAgentImpl struct{}

func (a *fakePoolAgentImpl) Name() string { return fakePoolAgent }

func (a *fakePoolAgentImpl) Open(context.Context, acptransport.Handlers) (acptransport.Transport, error) {
	atomic.AddInt32(&fakeOpenCount, 1)
	return newFakePoolTransport(), nil
}

func (a *fakePoolAgentImpl) InitializeParams() map[string]any        { return map[string]any{} }
func (a *fakePoolAgentImpl) SessionNewParams(string) map[string]any  { return map[string]any{} }
func (a *fakePoolAgentImpl) SessionLoadParams(string) map[string]any { return map[string]any{} }

func (a *fakePoolAgentImpl) PromptParams(string, string, string) map[string]any {
	return map[string]any{}
}

func (a *fakePoolAgentImpl) Cancel(context.Context, acptransport.Transport, string) {}

type fakePoolTransport struct {
	mu    sync.Mutex
	alive bool
}

func newFakePoolTransport() *fakePoolTransport { return &fakePoolTransport{alive: true} }

func (f *fakePoolTransport) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method == "session/new" {
		return json.RawMessage(`{"sessionId":"sess-1"}`), nil
	}
	return json.RawMessage(`{}`), nil
}

func (f *fakePoolTransport) Notify(string, any) error { return nil }

func (f *fakePoolTransport) Updates(int) (<-chan acptransport.Message, func()) {
	ch := make(chan acptransport.Message)
	return ch, func() {}
}

func (f *fakePoolTransport) Alive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive
}

func (f *fakePoolTransport) Close() error {
	f.kill()
	return nil
}

// kill simulates the agent process exiting without an explicit Close.
func (f *fakePoolTransport) kill() {
	f.mu.Lock()
	f.alive = false
	f.mu.Unlock()
}

func newTestManager() *Manager {
	return &Manager{
		active:    NewActivityTracker(),
		instances: map[string]*managedInstance{},
		done:      make(chan struct{}),
	}
}

func testServiceConfig(t *testing.T) acpservice.ServiceConfig {
	t.Helper()
	return acpservice.ServiceConfig{ID: "svc", AgentType: fakePoolAgent, CWD: t.TempDir()}
}

func transportOf(t *testing.T, inst *instance) *fakePoolTransport {
	t.Helper()
	tr, ok := inst.t.(*fakePoolTransport)
	if !ok {
		t.Fatalf("instance transport is %T, want *fakePoolTransport", inst.t)
	}
	return tr
}

func TestResolveInstanceReusesLiveInstance(t *testing.T) {
	atomic.StoreInt32(&fakeOpenCount, 0)
	m := newTestManager()
	cfg := testServiceConfig(t)
	ctx := context.Background()

	first, err := m.resolveInstance(ctx, "scope-a", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("first resolveInstance: %v", err)
	}
	second, err := m.resolveInstance(ctx, "scope-a", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("second resolveInstance: %v", err)
	}
	if first != second {
		t.Fatal("expected the live instance to be reused")
	}
	if got := atomic.LoadInt32(&fakeOpenCount); got != 1 {
		t.Fatalf("agent opened %d times, want 1", got)
	}
}

func TestResolveInstanceFreshSessionReplacesInstance(t *testing.T) {
	atomic.StoreInt32(&fakeOpenCount, 0)
	m := newTestManager()
	cfg := testServiceConfig(t)
	ctx := context.Background()

	first, err := m.resolveInstance(ctx, "scope-a", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("first resolveInstance: %v", err)
	}
	firstTransport := transportOf(t, first)

	second, err := m.resolveInstance(ctx, "scope-a", cfg, TurnRequest{ThreadID: "t1", Input: "hi", FreshSession: true})
	if err != nil {
		t.Fatalf("fresh resolveInstance: %v", err)
	}
	if first == second {
		t.Fatal("fresh_session must not reuse the pooled instance")
	}
	if firstTransport.Alive() {
		t.Fatal("evicted instance transport was not closed")
	}
	if got := atomic.LoadInt32(&fakeOpenCount); got != 2 {
		t.Fatalf("agent opened %d times, want 2", got)
	}
}

func TestResolveInstanceEvictsDeadInstance(t *testing.T) {
	atomic.StoreInt32(&fakeOpenCount, 0)
	m := newTestManager()
	cfg := testServiceConfig(t)
	ctx := context.Background()

	first, err := m.resolveInstance(ctx, "scope-a", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("first resolveInstance: %v", err)
	}
	transportOf(t, first).kill()

	second, err := m.resolveInstance(ctx, "scope-a", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("second resolveInstance: %v", err)
	}
	if first == second {
		t.Fatal("a dead instance must not be reused")
	}
	if got := atomic.LoadInt32(&fakeOpenCount); got != 2 {
		t.Fatalf("agent opened %d times, want 2", got)
	}
}

func TestReapIdleClosesIdleInstance(t *testing.T) {
	m := newTestManager()
	cfg := testServiceConfig(t)
	ctx := context.Background()

	inst, err := m.resolveInstance(ctx, "scope-idle", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("resolveInstance: %v", err)
	}
	m.mu.Lock()
	m.instances["scope-idle"].idleTTL = time.Millisecond
	m.instances["scope-idle"].lastUsed = time.Now().UTC().Add(-time.Hour)
	m.mu.Unlock()

	m.reapIdle(time.Now().UTC())

	m.mu.Lock()
	_, present := m.instances["scope-idle"]
	m.mu.Unlock()
	if present {
		t.Fatal("idle instance was not reaped")
	}
	if transportOf(t, inst).Alive() {
		t.Fatal("reaped instance transport was not closed")
	}
}

func TestReapIdleSkipsActiveScope(t *testing.T) {
	m := newTestManager()
	cfg := testServiceConfig(t)
	ctx := context.Background()

	if _, err := m.resolveInstance(ctx, "scope-busy", cfg, TurnRequest{ThreadID: "t1", Input: "hi"}); err != nil {
		t.Fatalf("resolveInstance: %v", err)
	}
	m.mu.Lock()
	m.instances["scope-busy"].idleTTL = time.Millisecond
	m.instances["scope-busy"].lastUsed = time.Now().UTC().Add(-time.Hour)
	m.mu.Unlock()

	release, err := m.active.Begin("scope-busy")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer release()

	m.reapIdle(time.Now().UTC())

	m.mu.Lock()
	_, present := m.instances["scope-busy"]
	m.mu.Unlock()
	if !present {
		t.Fatal("an instance with an active turn must not be reaped")
	}
}

func TestManagerCloseTearsDownInstances(t *testing.T) {
	m := NewManager(nil)
	cfg := testServiceConfig(t)
	ctx := context.Background()

	inst, err := m.resolveInstance(ctx, "scope-a", cfg, TurnRequest{ThreadID: "t1", Input: "hi"})
	if err != nil {
		t.Fatalf("resolveInstance: %v", err)
	}

	m.Close()
	if transportOf(t, inst).Alive() {
		t.Fatal("Close did not tear down the pooled instance")
	}
	m.Close() // must be idempotent
}

func TestShouldReap(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name     string
		lastUsed time.Time
		idleTTL  time.Duration
		alive    bool
		active   bool
		want     bool
	}{
		{"active is never reaped", now.Add(-time.Hour), time.Millisecond, true, true, false},
		{"dead is always reaped", now, 0, false, false, true},
		{"dead but active stays", now, 0, false, true, false},
		{"live idle disabled stays", now.Add(-time.Hour), 0, true, false, false},
		{"live within ttl stays", now, time.Hour, true, false, false},
		{"live beyond ttl reaped", now.Add(-2 * time.Hour), time.Hour, true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldReap(now, tc.lastUsed, tc.idleTTL, tc.alive, tc.active); got != tc.want {
				t.Fatalf("shouldReap = %v, want %v", got, tc.want)
			}
		})
	}
}
