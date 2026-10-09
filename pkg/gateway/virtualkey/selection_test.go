package virtualkey

import (
	"context"
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter"
	"sync"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/extension"
)

type selectionStore map[string]extension.Envelope

func (s selectionStore) List(context.Context) ([]extension.Envelope, error) {
	var out []extension.Envelope
	for _, e := range s {
		out = append(out, e)
	}
	return out, nil
}
func (s selectionStore) Put(_ context.Context, e extension.Envelope, _ bool) error {
	s[e.ModuleID] = e
	return nil
}
func (s selectionStore) Delete(_ context.Context, id string) error { delete(s, id); return nil }

type selectionEvents struct {
	mu     sync.Mutex
	events []event.Envelope
}

func (s *selectionEvents) Publish(_ context.Context, e event.Envelope) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return true
}
func (s *selectionEvents) lastReset() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.events[len(s.events)-1].Mutation.StateReset
}
func TestSelectionRefreshSwitchAndFailureIsolation(t *testing.T) {
	b := &testBackend{LimiterBackend: limiter.NewMemoryBackend(nil)}
	generic := &extension.Factory{Validate: func(_ context.Context, r json.RawMessage) (json.RawMessage, error) { return r, nil }, Prepare: func(context.Context, extension.PreparedConfig, extension.Snapshot) (extension.Resource, error) {
		return &testProvider{b}, nil
	}}
	r, err := extension.NewRegistry(
		extension.Registration{Descriptor: extension.Descriptor{ID: limiter.ModuleID, Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime"}}, Factory: limiter.Factory(true)},
		extension.Registration{Descriptor: extension.Descriptor{ID: "example.fixture.limiter", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime"}}, Factory: generic},
		extension.Registration{Descriptor: extension.Descriptor{ID: "example.fixture.unrelated", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime"}}, Factory: generic})
	if err != nil {
		t.Fatal(err)
	}
	ext, err := extension.NewManager(t.Context(), r, selectionStore{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	events := &selectionEvents{}
	ext.SetPublisher(events)
	m := NewVirtualKeyManager(nil)
	m.SetExtensions(ext)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = m.Close(ctx)
		_ = ext.Close(ctx)
	})
	put := func(id, raw string) {
		t.Helper()
		if err := ext.Put(t.Context(), extension.Envelope{ModuleID: id, SchemaVersion: 1, Config: json.RawMessage(raw)}); err != nil {
			t.Fatal(err)
		}
	}
	admit := func(want bool) {
		t.Helper()
		d, e := m.Admit(t.Context(), hardKey(), RateLimitDimensionLLM)
		if e != nil || d.Allowed != want {
			t.Fatalf("admission=%+v %v want %v", d, e, want)
		}
	}
	admit(true)
	admit(false)
	put("example.fixture.limiter", `{}`)
	put(limiter.ModuleID, `{"backend_module_id":"example.fixture.limiter","backend_type":"fixture"}`)
	if !events.lastReset() {
		t.Fatal("switch missing state_reset")
	}
	admit(true)
	admit(false)
	old := m.limiters.states[limiterKey{"key", RateLimitDimensionLLM}].request
	put("example.fixture.unrelated", `{}`)
	if events.lastReset() {
		t.Fatal("unrelated refresh reset")
	}
	admit(false)
	put(limiter.ModuleID, `{"backend_module_id":"example.fixture.limiter","backend_type":"fixture","timeout_ms":20,"failure_mode":"open"}`)
	if events.lastReset() {
		t.Fatal("timeout/failure policy reset")
	}
	admit(false)
	if err := ext.Put(t.Context(), extension.Envelope{ModuleID: limiter.ModuleID, SchemaVersion: 1, Config: json.RawMessage(`{"backend_module_id":"example.fixture.limiter","backend_type":"unknown"}`)}); err == nil {
		t.Fatal("unavailable backend accepted")
	}
	admit(false)
	// A switch away and back resets even if no request used the intermediate backend.
	if err := ext.Delete(t.Context(), limiter.ModuleID); err != nil {
		t.Fatal(err)
	}
	if !events.lastReset() {
		t.Fatal("delete missing reset")
	}
	put(limiter.ModuleID, `{"backend_module_id":"example.fixture.limiter","backend_type":"fixture"}`)
	admit(true)
	if _, err := b.LimiterBackend.Admit(t.Context(), old); err == nil {
		t.Fatal("old remote generation revived")
	}
	put("example.fixture.limiter", `{"namespace":"new"}`)
	if !events.lastReset() {
		t.Fatal("backend config change missing reset")
	}
	admit(true)
}

func TestSlowRetirementDoesNotExhaustFreshAdmissionBudget(t *testing.T) {
	for _, failure := range []string{"closed", "open"} {
		t.Run(failure, func(t *testing.T) {
			b := &testBackend{LimiterBackend: limiter.NewMemoryBackend(nil)}
			factory := &extension.Factory{Validate: func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) { return raw, nil }, Prepare: func(context.Context, extension.PreparedConfig, extension.Snapshot) (extension.Resource, error) {
				return &testProvider{b}, nil
			}}
			r, err := extension.NewRegistry(
				extension.Registration{Descriptor: extension.Descriptor{ID: limiter.ModuleID, Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime"}}, Factory: limiter.Factory(true)},
				extension.Registration{Descriptor: extension.Descriptor{ID: "example.fixture.limiter", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime"}}, Factory: factory})
			if err != nil {
				t.Fatal(err)
			}
			ext, err := extension.NewManager(t.Context(), r, selectionStore{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			m := NewVirtualKeyManager(nil)
			m.SetExtensions(ext)
			t.Cleanup(func() { _ = m.Close(context.Background()); _ = ext.Close(context.Background()) })
			put := func(id, raw string) {
				t.Helper()
				if err := ext.Put(t.Context(), extension.Envelope{ModuleID: id, SchemaVersion: 1, Config: json.RawMessage(raw)}); err != nil {
					t.Fatal(err)
				}
			}
			put("example.fixture.limiter", `{}`)
			put(limiter.ModuleID, `{"backend_module_id":"example.fixture.limiter","backend_type":"fixture","timeout_ms":10,"failure_mode":"`+failure+`"}`)
			if d, err := m.Admit(t.Context(), hardKey(), RateLimitDimensionLLM); err != nil || !d.Allowed {
				t.Fatal(d, err)
			}
			b.retireWait.Store(true)
			put("example.fixture.limiter", `{"namespace":"new"}`)
			if d, err := m.Admit(t.Context(), hardKey(), RateLimitDimensionLLM); err != nil || !d.Allowed || d.Degraded {
				t.Fatalf("fresh bucket must consume normally after slow retirement: %+v %v", d, err)
			}
			if d, err := m.Admit(t.Context(), hardKey(), RateLimitDimensionLLM); err != nil || d.Allowed {
				t.Fatalf("fresh burst must already be consumed: %+v %v", d, err)
			}
		})
	}
}
