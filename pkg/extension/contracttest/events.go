package contracttest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
)

// Attribution checks every field, explicit absence and defensive context copies.
// The caller supplies its assembly policy and one valid populated fixture.
func Attribution(t *testing.T, p attribution.Policy, v attribution.Value) {
	t.Helper()
	ctx, err := attribution.WithContext(context.Background(), v, p)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := attribution.FromContext(ctx)
	if !ok || !reflect.DeepEqual(v, got) {
		t.Fatal("attribution propagation")
	}
	expected, _ := attribution.FromContext(ctx)
	for key := range got.Attributes {
		got.Attributes[key] = "changed"
	}
	again, _ := attribution.FromContext(ctx)
	if !reflect.DeepEqual(expected, again) {
		t.Fatal("retrieval ownership")
	}
	for key := range v.Attributes {
		v.Attributes[key] = "changed"
	}
	again, _ = attribution.FromContext(ctx)
	if !reflect.DeepEqual(expected, again) {
		t.Fatal("insertion ownership")
	}
	if _, ok := attribution.FromContext(context.Background()); ok {
		t.Fatal("absence")
	}
	empty, err := attribution.WithContext(context.Background(), attribution.Value{}, p)
	if err != nil {
		t.Fatal(err)
	}
	x, ok := attribution.FromContext(empty)
	if !ok || !reflect.DeepEqual(x, attribution.Value{}) {
		t.Fatal("empty fields synthesized")
	}
}

// CheckSinkCancellation invokes each lifecycle operation with an already
// canceled context. A stalled callback returns sink_nonconforming at the caller
// deadline. Test goroutines are probes, never the production delivery model.
func CheckSinkCancellation(ctx context.Context, s event.Sink) error {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	e := eventFixture("probe")
	for _, call := range []func() error{func() error { return s.Write(canceled, e) }, func() error { return s.Flush(canceled) }, func() error { return s.Close(canceled) }} {
		result := make(chan error, 1)
		go func() {
			defer func() {
				if recover() != nil {
					result <- &event.Error{Code: "sink_nonconforming"}
				}
			}()
			result <- call()
		}()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				return &event.Error{Code: "sink_nonconforming"}
			}
		case <-ctx.Done():
			return &event.Error{Code: "sink_nonconforming"}
		}
	}
	return nil
}

// EventSink proves cancellation plus FIFO/barrier and exactly-once concurrent
// close using independently owned sink implementations. Factories must return
// a fresh sink for each fixture.
func EventSink(t *testing.T, newSink func(context.Context) (event.Sink, error)) {
	t.Helper()
	t.Run("cancellation", func(t *testing.T) {
		s, err := newSink(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		if err := CheckSinkCancellation(ctx, s); err != nil {
			t.Fatal(err)
		}
		// A rejected canceled Close must not prevent later normal cleanup.
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("delivery", func(t *testing.T) {
		s, err := newSink(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		probe := &sinkProbe{sink: s}
		d, err := event.NewDelivery(probe, event.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close(context.Background())
		for i := 0; i < 8; i++ {
			if !d.Publish(context.Background(), eventFixture(fmt.Sprint(i))) {
				t.Fatal("unexpected drop")
			}
		}
		if err := d.Flush(context.Background()); err != nil {
			t.Fatal(err)
		}
		probe.mu.Lock()
		for i, id := range probe.ids {
			if id != fmt.Sprint(i) {
				t.Fatal("non-FIFO delivery")
			}
		}
		n := len(probe.ids)
		probe.mu.Unlock()
		if n != 8 {
			t.Fatal("flush passed accepted events")
		}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := d.Close(context.Background()); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if probe.closes != 1 {
			t.Fatal("close not exactly once")
		}
	})
}
func eventFixture(id string) event.Envelope {
	return event.Envelope{SchemaVersion: 1, ID: id, Timestamp: time.Now().UTC(), Family: "extension.mutation", Outcome: "success", Mutation: &event.ExtensionMutation{ModuleID: "example.fixture", Operation: "put"}}
}

type sinkProbe struct {
	mu     sync.Mutex
	sink   event.Sink
	ids    []string
	closes int
}

func (s *sinkProbe) Write(ctx context.Context, e event.Envelope) error {
	err := s.sink.Write(ctx, e)
	if err == nil {
		s.mu.Lock()
		s.ids = append(s.ids, e.ID)
		s.mu.Unlock()
	}
	return err
}
func (s *sinkProbe) Flush(ctx context.Context) error { return s.sink.Flush(ctx) }
func (s *sinkProbe) Close(ctx context.Context) error { s.closes++; return s.sink.Close(ctx) }
