package extension

import (
	"context"
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type capturePublisher struct {
	events []event.Envelope
	m      *Manager
	t      *testing.T
}

func (p *capturePublisher) Publish(_ context.Context, e event.Envelope) bool {
	if e.Outcome == "success" && e.Mutation.Operation == "put" && len(p.m.List()) != 1 {
		p.t.Fatal("published before commit")
	}
	p.events = append(p.events, e)
	return true
}
func TestMutationEventsAfterCommitAndSanitizedFailure(t *testing.T) {
	d := descriptor("example.fixture")
	f := factory()
	f.Prepare = func(context.Context, PreparedConfig, Snapshot) (Resource, error) { return &countedResource{}, nil }
	r, _ := NewRegistry(Registration{d, f})
	store := &memoryPersistence{}
	m, err := NewManager(context.Background(), r, store)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	p := &capturePublisher{m: m, t: t}
	m.SetPublisher(p)
	e := Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}
	if err := m.Put(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	store.fail = true
	_ = m.Put(context.Background(), e)
	if len(p.events) != 2 || p.events[0].Outcome != "success" || p.events[1].Reason != "persistence_failed" {
		t.Fatal(p.events)
	}
	raw, _ := json.Marshal(p.events)
	if strings.Contains(string(raw), "storage secret") {
		t.Fatal("diagnostic escaped")
	}
	store.fail = false
	if err := m.Delete(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	if p.events[2].Mutation.Operation != "delete" || p.events[2].Outcome != "success" {
		t.Fatal(p.events)
	}
}

// Pause the first publication before accepting it, exposing whether another
// mutation or shutdown can overtake that committed event.
type pausedMutationPublisher struct {
	entered chan struct{}
	release chan struct{}
	first   atomic.Bool
	mu      sync.Mutex
	events  []event.Envelope
}

func (p *pausedMutationPublisher) Publish(_ context.Context, e event.Envelope) bool {
	if p.first.CompareAndSwap(false, true) {
		close(p.entered)
		<-p.release
	}
	p.mu.Lock()
	p.events = append(p.events, e)
	p.mu.Unlock()
	return true
}
func TestMutationPublicationCompletesBeforeNextMutationOrShutdown(t *testing.T) {
	for _, next := range []string{"delete", "close"} {
		t.Run(next, func(t *testing.T) {
			d := descriptor("example.fixture")
			f := factory()
			f.Prepare = func(context.Context, PreparedConfig, Snapshot) (Resource, error) { return &countedResource{}, nil }
			r, err := NewRegistry(Registration{d, f})
			if err != nil {
				t.Fatal(err)
			}
			m, err := NewManager(context.Background(), r, &memoryPersistence{})
			if err != nil {
				t.Fatal(err)
			}
			p := &pausedMutationPublisher{entered: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			unblock := func() { release.Do(func() { close(p.release) }) }
			defer func() { unblock(); _ = m.Close(context.Background()) }()
			m.SetPublisher(p)
			putDone := make(chan error, 1)
			go func() {
				putDone <- m.Put(context.Background(), Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)})
			}()
			select {
			case <-p.entered:
			case <-time.After(time.Second):
				t.Fatal("put did not reach publication")
			}
			if len(m.List()) != 1 {
				t.Fatal("publication precedes snapshot commit")
			}
			nextDone := make(chan error, 1)
			go func() {
				if next == "delete" {
					nextDone <- m.Delete(context.Background(), d.ID)
				} else {
					nextDone <- m.Close(context.Background())
				}
			}()
			select {
			case err := <-nextDone:
				t.Fatalf("%s overtook pending committed publication: %v", next, err)
			case <-time.After(50 * time.Millisecond):
			}
			unblock()
			for _, done := range []chan error{putDone, nextDone} {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("mutation/shutdown did not finish")
				}
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if len(p.events) == 0 || p.events[0].Outcome != "success" || p.events[0].Mutation.Operation != "put" {
				t.Fatalf("committed event missing or reordered: %+v", p.events)
			}
			if next == "delete" && (len(p.events) != 2 || p.events[1].Mutation.Operation != "delete" || p.events[1].Outcome != "success") {
				t.Fatalf("mutation commit order lost: %+v", p.events)
			}
		})
	}
}
