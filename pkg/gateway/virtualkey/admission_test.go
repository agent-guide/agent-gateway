package virtualkey

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/extension"
)

type testBackend struct {
	limiter.LimiterBackend
	calls         atomic.Int64
	retireWait    atomic.Bool
	retireEntered chan struct{}
	retireRelease chan struct{}
	mode          string
	entered       chan struct{}
	release       chan struct{}
}

func (b *testBackend) Retire(ctx context.Context, generation string) error {
	if b.retireEntered != nil {
		close(b.retireEntered)
		select {
		case <-b.retireRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := b.LimiterBackend.Retire(ctx, generation); err != nil {
		return err
	}
	if b.retireWait.Load() {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func TestPolicyCleanupDoesNotBlockUnrelatedAuthentication(t *testing.T) {
	for _, operation := range []string{"delete", "reset"} {
		t.Run(operation, func(t *testing.T) {
			_, ext, b := admissionManager(t, "", "closed")
			m := NewVirtualKeyManager(&testManagedVirtualKeyStore{items: map[string]*VirtualKey{}})
			m.SetExtensions(ext)
			t.Cleanup(func() { _ = m.Close(context.Background()) })
			key := hardKey()
			key.Key = "limited-secret"
			if err := m.Create(t.Context(), key); err != nil {
				t.Fatal(err)
			}
			other := VirtualKey{ID: "other", Key: "other-secret"}
			if err := m.Create(t.Context(), other); err != nil {
				t.Fatal(err)
			}
			other, err := m.GetByID(t.Context(), other.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Admit(t.Context(), key, RateLimitDimensionLLM); err != nil {
				t.Fatal(err)
			}
			b.retireEntered = make(chan struct{})
			b.retireRelease = make(chan struct{})
			release := sync.OnceFunc(func() { close(b.retireRelease) })
			t.Cleanup(release)
			done := make(chan error, 1)
			go func() {
				if operation == "delete" {
					done <- m.Delete(t.Context(), key.ID)
				} else {
					m.Reset()
					done <- nil
				}
			}()
			<-b.retireEntered
			lookup := make(chan error, 1)
			go func() { _, err := m.GetByKey(t.Context(), other.Key); lookup <- err }()
			select {
			case err := <-lookup:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(50 * time.Millisecond):
				release()
				<-done
				<-lookup
				t.Fatal("unrelated authentication blocked by retirement callback")
			}
			release()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func (b *testBackend) Admit(ctx context.Context, r limiter.LimiterRequest) (limiter.LimiterDecision, error) {
	b.calls.Add(1)
	switch b.mode {
	case "retired":
		return limiter.LimiterDecision{}, fmt.Errorf("private wrapped diagnostic: %w", limiter.ErrLimiterRetired)
	case "retirement_race":
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
			return limiter.LimiterDecision{}, ctx.Err()
		}
		return b.LimiterBackend.Admit(ctx, r)
	case "error":
		return limiter.LimiterDecision{}, errors.New("private diagnostic secret")
	case "panic":
		panic("secret")
	case "wait":
		<-ctx.Done()
		return limiter.LimiterDecision{}, ctx.Err()
	case "ambiguous":
		_, _ = b.LimiterBackend.Admit(ctx, r)
		return limiter.LimiterDecision{}, errors.New("consumed but response lost")
	}
	return b.LimiterBackend.Admit(ctx, r)
}

type testProvider struct{ *testBackend }

func (p *testProvider) LimiterBackend(name string) (limiter.LimiterBackend, error) {
	if name != "fixture" {
		return nil, limiter.ErrLimiterUnavailable
	}
	return p.testBackend, nil
}
func (p *testProvider) Close(context.Context) error { return nil } // Test owns shared durable generation storage.
func admissionManager(t *testing.T, mode, failure string, timeout ...int) (*VirtualKeyManager, *extension.Manager, *testBackend) {
	t.Helper()
	b := &testBackend{LimiterBackend: limiter.NewMemoryBackend(nil), mode: mode}
	r, err := extension.NewRegistry(extension.Registration{Descriptor: extension.Descriptor{ID: limiter.ModuleID, Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime", "validation", "spi"}}, Factory: limiter.Factory(true)}, extension.Registration{Descriptor: extension.Descriptor{ID: "example.fixture.limiter", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime", "validation", "spi"}}, Factory: &extension.Factory{Validate: func(_ context.Context, r json.RawMessage) (json.RawMessage, error) { return r, nil }, Prepare: func(context.Context, extension.PreparedConfig, extension.Snapshot) (extension.Resource, error) {
		return &testProvider{b}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	timeoutMS := 10
	if len(timeout) > 0 {
		timeoutMS = timeout[0]
	}
	selected, _ := json.Marshal(limiter.Config{BackendModuleID: "example.fixture.limiter", BackendType: "fixture", TimeoutMS: timeoutMS, FailureMode: failure})
	e, err := extension.NewManager(t.Context(), r, admissionStore([]extension.Envelope{{ModuleID: "example.fixture.limiter", SchemaVersion: 1, Config: json.RawMessage(`{}`)}, {ModuleID: limiter.ModuleID, SchemaVersion: 1, Config: selected}}))
	if err != nil {
		t.Fatal(err)
	}
	m := NewVirtualKeyManager(nil)
	m.SetExtensions(e)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = m.Close(ctx)
		_ = e.Close(ctx)
		_ = b.LimiterBackend.Close(ctx)
	})
	return m, e, b
}
func hardKey() VirtualKey {
	return VirtualKey{ID: "key", RateLimits: &VirtualKeyRateLimits{LLM: &RateLimit{RequestsPerMinute: 1, Burst: 1}}}
}
func TestAdmissionFailureModesAndNoRetries(t *testing.T) {
	for _, mode := range []string{"error", "panic", "wait", "ambiguous"} {
		for _, failure := range []string{"closed", "open"} {
			t.Run(mode+"/"+failure, func(t *testing.T) {
				m, _, b := admissionManager(t, mode, failure)
				result, err := m.Admit(t.Context(), hardKey(), RateLimitDimensionLLM)
				if failure == "closed" {
					if !errors.Is(err, limiter.ErrLimiterUnavailable) || result.Allowed {
						t.Fatalf("closed %+v %v", result, err)
					}
				} else if err != nil || !result.Allowed || !result.Degraded {
					t.Fatalf("open %+v %v", result, err)
				}
				if b.calls.Load() != 1 {
					t.Fatal("uncertain operation retried")
				}
				if err != nil && err.Error() != "limiter_unavailable" {
					t.Fatal("diagnostic escaped", err)
				}
			})
		}
	}
}
func TestAdmissionCancellationNeverFailsOpenAndUnlimitedSkipsBackend(t *testing.T) {
	m, _, b := admissionManager(t, "wait", "open")
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if result, err := m.Admit(ctx, hardKey(), RateLimitDimensionLLM); !errors.Is(err, context.DeadlineExceeded) || result.Allowed {
		t.Fatalf("caller deadline %+v %v", result, err)
	}
	calls := b.calls.Load()
	if result, err := m.Admit(t.Context(), VirtualKey{ID: "unlimited"}, RateLimitDimensionLLM); err != nil || !result.Allowed {
		t.Fatal(result, err)
	}
	if b.calls.Load() != calls {
		t.Fatal("unlimited called backend")
	}
}
func TestAdmissionPolicyFencesAndUnchangedDimensions(t *testing.T) {
	store := &testManagedVirtualKeyStore{items: map[string]*VirtualKey{}}
	m := NewVirtualKeyManager(store)
	key := hardKey()
	key.Key = "secret"
	key.RateLimits.MCP = &RateLimit{RequestsPerMinute: 1, Burst: 1}
	if err := m.Create(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	for _, dim := range []RateLimitDimension{RateLimitDimensionLLM, RateLimitDimensionMCP} {
		if d, e := m.Admit(t.Context(), key, dim); e != nil || !d.Allowed {
			t.Fatal(d, e)
		}
	}
	old := m.limiters.states[limiterKey{key.ID, RateLimitDimensionLLM}]
	key.RateLimits.LLM = &RateLimit{RequestsPerMinute: 1, Burst: 2}
	if err := m.Update(t.Context(), key.ID, key); err != nil {
		t.Fatal(err)
	}
	if _, err := m.limiters.memory.Admit(t.Context(), old.request); !errors.Is(err, limiter.ErrLimiterRetired) {
		t.Fatal("stale generation revived", err)
	}
	if d, e := m.Admit(t.Context(), key, RateLimitDimensionMCP); e != nil || d.Allowed {
		t.Fatal("unchanged MCP reset", d, e)
	}
	if d, e := m.Admit(t.Context(), key, RateLimitDimensionLLM); e != nil || !d.Allowed {
		t.Fatal("changed policy not full", d, e)
	}
	if err := m.Delete(t.Context(), key.ID); err != nil {
		t.Fatal(err)
	}
	if d, e := m.Admit(t.Context(), key, RateLimitDimensionLLM); e == nil || d.Allowed {
		t.Fatal("deleted key revived", d, e)
	}
}
func TestCloseDuringAdmissionAndRepeatedClose(t *testing.T) {
	m, _, b := admissionManager(t, "wait", "open")
	done := make(chan error, 1)
	go func() { _, err := m.Admit(t.Context(), hardKey(), RateLimitDimensionLLM); done <- err }()
	deadline := time.After(time.Second)
	for b.calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("admission did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("shutdown failed open")
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Admit(ctx, hardKey(), RateLimitDimensionLLM); err == nil {
		t.Fatal("admitted after close")
	}
}
func TestLimiterSelectionStrictValidation(t *testing.T) {
	for _, raw := range []string{`{"timeout_ms":0}`, `{"timeout_ms":1001}`, `{"failure_mode":"fallback"}`, `{"backend_type":"attacker\"label"}`, `{"extra":1}`, `{"backend_module_id":"unknown"}`, `{"backend_type":"redis"}`, `{"timeout_ms":1,"timeout_ms":2}`} {
		if _, err := limiter.Factory(false).Validate(t.Context(), json.RawMessage(raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	raw, err := limiter.Factory(false).Validate(t.Context(), json.RawMessage(`{}`))
	var cfg limiter.Config
	_ = json.Unmarshal(raw, &cfg)
	if err != nil || cfg.TimeoutMS != 100 || cfg.FailureMode != "closed" {
		t.Fatal(cfg, err)
	}
}

// Concurrent store calls are deliberately allowed by the test backend; the
// manager must serialize the full persist/cache/policy publication operation.
type concurrentPolicyStore struct {
	*testManagedVirtualKeyStore
	mu sync.Mutex
}

func (s *concurrentPolicyStore) Update(ctx context.Context, obj any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.testManagedVirtualKeyStore.Update(ctx, obj)
}
func TestConcurrentPolicyWritesMatchPersistedAdmissionSnapshot(t *testing.T) {
	store := &concurrentPolicyStore{testManagedVirtualKeyStore: &testManagedVirtualKeyStore{items: map[string]*VirtualKey{}}}
	m := NewVirtualKeyManager(store)
	key := hardKey()
	key.Key = "secret"
	if err := m.Create(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 1; i <= 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := key
			next.RateLimits = &VirtualKeyRateLimits{LLM: &RateLimit{RequestsPerMinute: i, Burst: i}}
			if err := m.Update(t.Context(), key.ID, next); err != nil {
				t.Error(err)
			}
		}()
	}
	for range 64 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = m.Admit(t.Context(), key, RateLimitDimensionLLM) }()
	}
	wg.Wait()
	current, err := m.GetByID(t.Context(), key.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Get(t.Context(), key.ID)
	if !rateLimitsEqual(current.RateLimits, stored.(*VirtualKey).RateLimits) {
		t.Fatal("cache differs from persisted policy")
	}
	m.limiters.mu.Lock()
	policy := cloneRateLimits(m.limiters.policies[key.ID])
	m.limiters.mu.Unlock()
	if !rateLimitsEqual(current.RateLimits, policy) {
		t.Fatalf("admission snapshot differs: stored=%+v admission=%+v", current.RateLimits.LLM, policy.LLM)
	}
}

func TestRetiredGenerationNeverFailsOpen(t *testing.T) {
	for _, failure := range []string{"closed", "open"} {
		t.Run(failure, func(t *testing.T) {
			m, _, b := admissionManager(t, "retired", failure)
			result, err := m.Admit(t.Context(), hardKey(), RateLimitDimensionLLM)
			if result.Allowed || result.Degraded || !errors.Is(err, limiter.ErrLimiterRetired) {
				t.Fatalf("retired generation %+v %v", result, err)
			}
			if err.Error() != limiter.ErrLimiterRetired.Error() {
				t.Fatal("private wrapped error escaped", err)
			}
			if b.calls.Load() != 1 {
				t.Fatal("retired admission retried")
			}
		})
	}
}
func TestPolicyMutationFencesInflightAdmissionWithFailOpen(t *testing.T) {
	for _, operation := range []string{"update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			_, extensions, b := admissionManager(t, "retirement_race", "open", 1000)
			b.entered = make(chan struct{})
			b.release = make(chan struct{})
			release := sync.OnceFunc(func() { close(b.release) })
			defer release()
			store := &testManagedVirtualKeyStore{items: map[string]*VirtualKey{}}
			m := NewVirtualKeyManager(store)
			m.SetExtensions(extensions)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = m.Close(ctx)
			})
			key := hardKey()
			key.Key = "secret"
			if err := m.Create(t.Context(), key); err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				admission Admission
				err       error
			}
			done := make(chan outcome, 1)
			go func() { a, e := m.Admit(t.Context(), key, RateLimitDimensionLLM); done <- outcome{a, e} }()
			select {
			case <-b.entered:
			case <-time.After(time.Second):
				t.Fatal("admission did not start")
			}
			if operation == "update" {
				key.RateLimits = &VirtualKeyRateLimits{LLM: &RateLimit{RequestsPerMinute: 1, Burst: 2}}
				if err := m.Update(t.Context(), key.ID, key); err != nil {
					t.Fatal(err)
				}
			} else if err := m.Delete(t.Context(), key.ID); err != nil {
				t.Fatal(err)
			}
			release()
			select {
			case result := <-done:
				if result.admission.Allowed || result.admission.Degraded || !errors.Is(result.err, limiter.ErrLimiterRetired) {
					t.Fatalf("mutation failed open: %+v %v", result.admission, result.err)
				}
			case <-time.After(time.Second):
				t.Fatal("admission did not finish")
			}
		})
	}
}

func admissionStore(envelopes []extension.Envelope) selectionStore {
	s := selectionStore{}
	for _, e := range envelopes {
		s[e.ModuleID] = e
	}
	return s
}
