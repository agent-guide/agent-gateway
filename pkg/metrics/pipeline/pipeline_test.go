package pipeline

import (
	"sync"
	"testing"
)

type captureSink struct {
	mu     sync.Mutex
	events []any
}

func (s *captureSink) Write(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, v)
	return nil
}

func (s *captureSink) Close() error { return nil }

func TestPipelineCloseFlushesPendingEvent(t *testing.T) {
	sink := &captureSink{}
	p := NewEventPipeline(4, sink)
	p.Start()
	if !p.Enqueue("one") {
		t.Fatal("Enqueue() = false")
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(sink.events) != 1 || sink.events[0] != "one" {
		t.Fatalf("events = %#v, want one pending event flushed", sink.events)
	}
}

func TestPipelineDropsWhenFull(t *testing.T) {
	p := NewEventPipeline(1)
	if !p.Enqueue("one") {
		t.Fatal("first Enqueue() = false")
	}
	if p.Enqueue("two") {
		t.Fatal("second Enqueue() = true, want drop")
	}
	if got := p.DroppedEvents(); got != 1 {
		t.Fatalf("DroppedEvents() = %d, want 1", got)
	}
}
