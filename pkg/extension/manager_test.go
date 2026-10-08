package extension

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryPersistence struct {
	items []Envelope
	fail  bool
}

func (s *memoryPersistence) List(context.Context) ([]Envelope, error) { return s.items, nil }
func (s *memoryPersistence) Put(_ context.Context, e Envelope, create bool) error {
	if s.fail {
		return errors.New("storage secret")
	}
	for i, old := range s.items {
		if old.ModuleID == e.ModuleID {
			s.items[i] = e
			return nil
		}
	}
	s.items = append(s.items, e)
	return nil
}
func (s *memoryPersistence) Delete(_ context.Context, id string) error {
	if s.fail {
		return errors.New("storage secret")
	}
	for i, e := range s.items {
		if e.ModuleID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			break
		}
	}
	return nil
}

type countedResource struct{ closed atomic.Int32 }

func (r *countedResource) Health(context.Context) error { return nil }
func (r *countedResource) Close(context.Context) error  { r.closed.Add(1); return nil }
func TestMutationPublicationAndRetirement(t *testing.T) {
	d := descriptor("example.test.lifecycle")
	f := factory()
	resources := []*countedResource{}
	f.Prepare = func(_ context.Context, c PreparedConfig, _ Snapshot) (Resource, error) {
		if string(c.Envelope.Config) == `{"fail":true}` {
			return nil, errors.New("private")
		}
		r := &countedResource{}
		resources = append(resources, r)
		return r, nil
	}
	registry, _ := NewRegistry(Registration{d, f})
	store := &memoryPersistence{}
	m, err := NewManager(context.Background(), registry, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	e := Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{"n":1}`)}
	if err = m.Put(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	lease, _ := m.Acquire()
	old, _ := lease.Snapshot.Resource(d.ID)
	e.Config = json.RawMessage(`{"fail":true}`)
	code(t, m.Put(context.Background(), e), "prepare_failed")
	if string(m.List()[0].Config) != `{"n":1}` {
		t.Fatal("failed preparation published")
	}
	e.Config = json.RawMessage(`{"n":2}`)
	store.fail = true
	code(t, m.Put(context.Background(), e), "persistence_failed")
	store.fail = false
	if string(m.List()[0].Config) != `{"n":1}` || resources[1].closed.Load() != 1 {
		t.Fatal("persistence failure leaked or published")
	}
	if err = m.Put(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if old.(*countedResource).closed.Load() != 0 {
		t.Fatal("leased generation retired")
	}
	lease.Release()
	lease.Release()
	deadline := time.After(time.Second)
	for old.(*countedResource).closed.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("not retired")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err = m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = m.Acquire()
	code(t, err, "closed")
}
func TestStaticDependencyAndCancellation(t *testing.T) {
	a, b := descriptor("example.test.a"), descriptor("example.test.b")
	b.Dependencies = []string{a.ID}
	r, _ := NewRegistry(Registration{a, factory()}, Registration{b, factory()})
	store := &memoryPersistence{items: []Envelope{{ModuleID: a.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}, {ModuleID: b.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}}}
	m, err := NewManager(context.Background(), r, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	code(t, m.Delete(context.Background(), a.ID), "missing_dependency")
	_, err = NewManager(context.Background(), r, store, []Envelope{store.items[0]})
	code(t, err, "duplicate_module")
	static, err := NewManager(context.Background(), r, &memoryPersistence{}, []Envelope{store.items[0]})
	if err != nil {
		t.Fatal(err)
	}
	defer static.Close(context.Background())
	code(t, static.Put(context.Background(), store.items[0]), "static_config")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = m.Put(ctx, store.items[0]); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestConcurrentLeasesAndMutations(t *testing.T) {
	d := descriptor("example.test.concurrent")
	r, _ := NewRegistry(Registration{d, factory()})
	store := &memoryPersistence{}
	m, err := NewManager(context.Background(), r, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	e := Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}
	if err = m.Put(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				lease, err := m.Acquire()
				if err != nil {
					t.Error(err)
					return
				}
				cfg, ok := lease.Snapshot.Config(d.ID)
				if !ok || string(cfg.Config) != "{}" {
					t.Error("mixed generation")
				}
				lease.Release()
			}
		}()
	}
	for range 10 {
		if err = m.Put(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}
func TestCloseDeadlineCancelsLeasedGeneration(t *testing.T) {
	d := descriptor("example.test.deadline")
	r, _ := NewRegistry(Registration{d, factory()})
	store := &memoryPersistence{items: []Envelope{{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}}}
	m, err := NewManager(context.Background(), r, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	lease, _ := m.Acquire()
	defer lease.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err = m.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	select {
	case <-lease.Snapshot.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("generation lifetime was not canceled")
	}
}
func TestValidationOnlyFactory(t *testing.T) {
	d := descriptor("example.test.schema")
	d.Roles = []string{"validation"}
	f := factory()
	f.Prepare = nil
	r, err := NewRegistry(Registration{d, f})
	if err != nil {
		t.Fatal(err)
	}
	e := Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}
	if _, err = r.Validate(context.Background(), []Envelope{e}); err != nil {
		t.Fatal(err)
	}
	_, err = NewManager(context.Background(), r, nil, []Envelope{e})
	code(t, err, "unknown_module")
}

type panicResource struct{}

func (panicResource) Health(context.Context) error { return nil }
func (panicResource) Close(context.Context) error  { panic("private-secret-panic") }
func TestCallbackPanicIsBounded(t *testing.T) {
	d := descriptor("example.test.panic")
	f := factory()
	f.Validate = func(context.Context, json.RawMessage) (json.RawMessage, error) { panic("private-secret-panic") }
	r, _ := NewRegistry(Registration{d, f})
	e := Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}
	_, err := r.Validate(context.Background(), []Envelope{e})
	code(t, err, "invalid_config")
	f = factory()
	f.Prepare = func(context.Context, PreparedConfig, Snapshot) (Resource, error) { panic("private-secret-panic") }
	r, _ = NewRegistry(Registration{d, f})
	_, err = NewManager(context.Background(), r, nil, []Envelope{e})
	code(t, err, "prepare_failed")
	f.Prepare = func(context.Context, PreparedConfig, Snapshot) (Resource, error) { return panicResource{}, nil }
	r, _ = NewRegistry(Registration{d, f})
	m, err := NewManager(context.Background(), r, nil, []Envelope{e})
	if err != nil {
		t.Fatal(err)
	}
	code(t, m.Close(context.Background()), "unhealthy")
}
func TestCloseCapsPreviouslyRetiredGenerations(t *testing.T) {
	d := descriptor("example.test.olddeadline")
	r, _ := NewRegistry(Registration{d, factory()})
	e := Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)}
	store := &memoryPersistence{items: []Envelope{e}}
	m, err := NewManager(context.Background(), r, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := m.Acquire()
	defer old.Release()
	if err = m.Put(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	current, _ := m.Acquire()
	defer current.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err = m.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	for _, lease := range []*Lease{old, current} {
		select {
		case <-lease.Snapshot.Context().Done():
		case <-time.After(time.Second):
			t.Fatal("retired generation ignored shutdown deadline")
		}
	}
}

func TestPrepareOnlyReceivesDeclaredDependencies(t *testing.T) {
	a, b, c := descriptor("example.test.a"), descriptor("example.test.b"), descriptor("example.test.c")
	c.Dependencies = []string{a.ID}
	f := factory()
	f.Prepare = func(_ context.Context, _ PreparedConfig, dependencies Snapshot) (Resource, error) {
		if _, ok := dependencies.Resource(a.ID); !ok {
			t.Error("declared dependency missing")
		}
		if _, ok := dependencies.Resource(b.ID); ok {
			t.Error("undeclared resource exposed")
		}
		return &testResource{}, nil
	}
	r, err := NewRegistry(Registration{a, factory()}, Registration{b, factory()}, Registration{c, f})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryPersistence{}
	for _, d := range []Descriptor{a, b, c} {
		store.items = append(store.items, Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)})
	}
	m, err := NewManager(context.Background(), r, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
}

func TestCloseCancelsMutationBeforeWaitingForGate(t *testing.T) {
	d := descriptor("example.test.closing-mutation")
	entered := make(chan struct{})
	f := factory()
	f.Prepare = func(ctx context.Context, _ PreparedConfig, _ Snapshot) (Resource, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	r, err := NewRegistry(Registration{d, f})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryPersistence{}
	m, err := NewManager(context.Background(), r, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	mutation := make(chan error, 1)
	go func() {
		mutation <- m.Put(context.Background(), Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-mutation; err == nil {
		t.Fatal("preparation committed after close")
	}
	_, err = m.Acquire()
	code(t, err, "closed")
	if len(store.items) != 0 || len(m.List()) != 0 {
		t.Fatal("canceled mutation persisted or published")
	}
	if err = m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type committedPersistence struct {
	memoryPersistence
	committed chan struct{}
}

func (s *committedPersistence) Put(ctx context.Context, e Envelope, create bool) error {
	if err := s.memoryPersistence.Put(ctx, e, create); err != nil {
		return err
	}
	close(s.committed)
	<-ctx.Done()
	return nil // commit already succeeded before cancellation
}
func TestCloseRetiresAWriteCommittedBeforeCancellation(t *testing.T) {
	d := descriptor("example.test.committed-close")
	resource := &countedResource{}
	f := factory()
	f.Prepare = func(context.Context, PreparedConfig, Snapshot) (Resource, error) { return resource, nil }
	r, _ := NewRegistry(Registration{d, f})
	store := &committedPersistence{committed: make(chan struct{})}
	m, err := NewManager(context.Background(), r, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	mutation := make(chan error, 1)
	go func() {
		mutation <- m.Put(context.Background(), Envelope{ModuleID: d.ID, SchemaVersion: 1, Config: json.RawMessage(`{}`)})
	}()
	<-store.committed
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-mutation; err != nil {
		t.Fatal("successful commit must finalize", err)
	}
	_, err = m.Acquire()
	code(t, err, "closed")
	if len(store.items) != 1 || len(m.List()) != 1 || resource.closed.Load() != 1 {
		t.Fatal("committed generation lost or leaked at close")
	}
}
