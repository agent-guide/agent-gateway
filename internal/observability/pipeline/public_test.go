package pipeline

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"sync/atomic"
	"testing"
	"time"
)

type publicSink struct {
	writes  atomic.Int32
	closes  atomic.Int32
	blocked bool
}

func (s *publicSink) Write(ctx context.Context, e event.Envelope) error {
	if s.blocked {
		<-ctx.Done()
		return ctx.Err()
	}
	s.writes.Add(1)
	return nil
}
func (s *publicSink) Flush(ctx context.Context) error { return ctx.Err() }
func (s *publicSink) Close(ctx context.Context) error { s.closes.Add(1); return nil }
func TestPublicSinkIsolationAndProjection(t *testing.T) {
	slow, fast := &publicSink{blocked: true}, &publicSink{}
	p := NewEventPipeline(1)
	if err := p.ConfigurePublic(attribution.Policy{}, []event.Registration{{New: func(context.Context) (event.Sink, error) { return slow, nil }}, {New: func(context.Context) (event.Sink, error) { return fast, nil }}}); err != nil {
		t.Fatal(err)
	}
	e := event.Envelope{SchemaVersion: 1, ID: "test", Timestamp: time.Now().UTC(), Family: "extension.mutation", Outcome: "success", Mutation: &event.ExtensionMutation{ModuleID: "example.fixture", Operation: "put"}}
	if !p.Publish(context.Background(), e) {
		t.Fatal("not accepted")
	}
	deadline := time.Now().Add(time.Second)
	for fast.writes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if fast.writes.Load() != 1 {
		t.Fatal("slow sink blocked another")
	}
	e.Reason = "arbitrary prompt"
	if p.Publish(context.Background(), e) || p.RejectedEvents() != 1 {
		t.Fatal("projection boundary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = p.CloseContext(ctx)
	_ = p.Close()
	if fast.closes.Load() != 1 {
		t.Fatal("not closed once")
	}
}

type stalledLegacySink struct {
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
	closes  atomic.Int32
}

func (s *stalledLegacySink) Write(any) error { close(s.entered); <-s.release; return nil }
func (s *stalledLegacySink) Close() error {
	if s.closes.Add(1) == 1 {
		close(s.closed)
	}
	return nil
}
func TestLegacyStallBoundedAndClosedOnce(t *testing.T) {
	s := &stalledLegacySink{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	p := NewEventPipeline(1, s)
	p.Start()
	p.Enqueue("event")
	<-s.entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if p.CloseContext(ctx) == nil {
		t.Fatal("legacy stall not bounded")
	}
	close(s.release)
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("legacy cleanup abandoned")
	}
	_ = p.Close()
	if s.closes.Load() != 1 {
		t.Fatal("legacy close repeated")
	}
}
