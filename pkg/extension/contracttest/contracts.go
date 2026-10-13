// Package contracttest supplies reusable candidate extension contract suites.
package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"reflect"
	"slices"
	"sync"
	"testing"
)

// Registry checks deterministic, pointer-free projection and ownership isolation.
func Registry(t *testing.T, registrations []extension.Registration) {
	t.Helper()
	t.Run("registry", func(t *testing.T) {
		r, err := extension.NewRegistry(registrations...)
		if err != nil {
			t.Fatal(err)
		}
		reversed := slices.Clone(registrations)
		slices.Reverse(reversed)
		other, err := extension.NewRegistry(reversed...)
		if err != nil {
			t.Fatal(err)
		}
		first, _ := json.Marshal(r.Manifest())
		second, _ := json.Marshal(other.Manifest())
		if string(first) != string(second) {
			t.Fatal("nondeterministic manifest")
		}
		m := r.Manifest()
		if len(m.Modules) > 0 {
			m.Modules[0].ID = "modified"
			again, _ := json.Marshal(r.Manifest())
			if string(first) != string(again) {
				t.Fatal("manifest aliases registry")
			}
		}
		if len(registrations) > 0 {
			_, err = extension.NewRegistry(append(slices.Clone(registrations), registrations[0])...)
			var e *extension.Error
			if !errors.As(err, &e) || e.Code != "duplicate_module" {
				t.Fatalf("duplicate: %v", err)
			}
		}
		_, err = extension.NewRegistry(extension.Registration{})
		if err == nil {
			t.Fatal("empty registration accepted")
		}
	})
}

type persistence struct {
	items []extension.Envelope
	fail  bool
}

func (p *persistence) List(context.Context) ([]extension.Envelope, error) { return p.items, nil }
func (p *persistence) Put(_ context.Context, e extension.Envelope, _ bool) error {
	if p.fail {
		return errors.New("private storage diagnostic")
	}
	for i, old := range p.items {
		if old.ModuleID == e.ModuleID {
			p.items[i] = e
			return nil
		}
	}
	p.items = append(p.items, e)
	return nil
}
func (p *persistence) Delete(_ context.Context, id string) error {
	if p.fail {
		return errors.New("private storage diagnostic")
	}
	for i, e := range p.items {
		if e.ModuleID == id {
			p.items = append(p.items[:i], p.items[i+1:]...)
			break
		}
	}
	return nil
}

// Configuration expects a complete valid runtime configuration and any required
// environment references supplied by the caller. It checks local validation,
// persistence failure isolation, concurrent reads and repeated lifecycle close.
func Configuration(t *testing.T, registrations []extension.Registration, input []extension.Envelope) {
	t.Helper()
	t.Run("configuration", func(t *testing.T) {
		if len(input) == 0 {
			t.Fatal("at least one configured module required")
		}
		r, err := extension.NewRegistry(registrations...)
		if err != nil {
			t.Fatal(err)
		}
		ordered, err := r.Validate(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		bad := ordered[0]
		bad.SchemaVersion++
		if _, err = r.Validate(context.Background(), []extension.Envelope{bad}); err == nil {
			t.Fatal("schema mismatch accepted")
		}
		store := &persistence{items: slices.Clone(ordered)}
		m, err := extension.NewManager(context.Background(), r, store)
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close(context.Background())
		before := m.List()
		store.fail = true
		if err = m.Put(context.Background(), ordered[0]); err == nil {
			t.Fatal("failed persistence accepted")
		}
		store.fail = false
		if !reflect.DeepEqual(before, m.List()) {
			t.Fatal("failed persistence published")
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 20 {
					lease, err := m.Acquire()
					if err != nil {
						t.Error(err)
						return
					}
					for _, e := range ordered {
						if _, ok := lease.Snapshot.Resource(e.ModuleID); !ok {
							t.Error("incomplete generation")
						}
					}
					lease.Release()
					lease.Release()
				}
			}()
		}
		if err = m.Put(context.Background(), ordered[0]); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if err = m.Health(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err = m.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err = m.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err = m.Acquire(); err == nil {
			t.Fatal("admission after close")
		}
	})
}
