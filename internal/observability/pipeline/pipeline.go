package pipeline

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"sync"
	"sync/atomic"
	"time"
)

type Sink interface {
	Write(any) error
	Close() error
}

type EventPipeline struct {
	ch            chan any
	sinks         []Sink
	once          sync.Once
	closed        chan struct{}
	closeMu       sync.RWMutex
	closing       bool
	public        []*event.Delivery
	policy        attribution.Policy
	shutdownDone  chan struct{}
	shutdownErr   error
	dropped       atomic.Uint64
	writeFailures atomic.Uint64
	rejected      atomic.Uint64
}

func NewEventPipeline(size int, sinks ...Sink) *EventPipeline {
	if size <= 0 {
		size = 4096
	}
	return &EventPipeline{
		ch:     make(chan any, size),
		sinks:  append([]Sink(nil), sinks...),
		closed: make(chan struct{}),
	}
}

func (p *EventPipeline) Start() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		go p.run()
	})
}

func (p *EventPipeline) Enqueue(v any) bool {
	if p == nil || v == nil {
		return false
	}
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()
	if p.closing {
		p.dropped.Add(1)
		return false
	}
	select {
	case p.ch <- v:
		return true
	default:
		p.dropped.Add(1)
		return false
	}
}

func (p *EventPipeline) DroppedEvents() uint64 {
	if p == nil {
		return 0
	}
	total := p.dropped.Load()
	for _, s := range p.PublicStats() {
		total += s.Dropped
	}
	return total
}

func (p *EventPipeline) WriteFailures() uint64 {
	if p == nil {
		return 0
	}
	total := p.writeFailures.Load()
	for _, s := range p.PublicStats() {
		total += s.Failures
	}
	return total
}

// ConfigurePublic installs per-sink transports before the pipeline serves.
func (p *EventPipeline) ConfigurePublic(policy attribution.Policy, sinks []event.Registration) error {
	p.closeMu.Lock()
	defer p.closeMu.Unlock()
	if p.closing || len(p.public) > 0 {
		return &event.Error{Code: "invalid_sink_options"}
	}
	p.policy = policy
	cleanup := func(s event.Sink) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if s != nil {
			if d, err := event.NewDelivery(s, event.Options{Policy: policy}); err == nil {
				_ = d.Close(ctx)
			}
		}
		for _, d := range p.public {
			_ = d.Close(ctx)
		}
		p.public = nil
	}
	for _, r := range sinks {
		// Validate options before constructing a resource so no rejected registration
		// can strand an initialized sink outside a bounded lifecycle worker.
		if r.New == nil || r.QueueSize < 0 || r.QueueSize > 4096 || r.WriteBudget < 0 || r.WriteBudget > time.Second {
			cleanup(nil)
			return &event.Error{Code: "invalid_sink_options"}
		}
		sink, err := createSink(r)
		if err != nil {
			cleanup(sink)
			return &event.Error{Code: "sink_initialization_failed"}
		}
		d, err := event.NewDelivery(sink, event.Options{QueueSize: r.QueueSize, WriteBudget: r.WriteBudget, Policy: policy})
		if err != nil {
			cleanup(sink)
			return err
		}
		p.public = append(p.public, d)
	}
	return nil
}
func createSink(r event.Registration) (s event.Sink, err error) {
	defer func() {
		if recover() != nil {
			err = &event.Error{Code: "sink_initialization_failed"}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err = r.New(ctx)
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return
}

// Publish is the public boundary of the same authority that owns usage events.
// It only accepts the two explicit sanitized DTO families, never internal usage.
func (p *EventPipeline) Publish(ctx context.Context, e event.Envelope) bool {
	if p == nil {
		return false
	}
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()
	if event.Validate(e, p.policy) != nil {
		p.rejected.Add(1)
		return false
	}
	if p.closing {
		return false
	}
	accepted := false
	for _, d := range p.public {
		if d.Publish(ctx, e) {
			accepted = true
		}
	}
	return accepted
}
func (p *EventPipeline) PublicStats() []event.Stats {
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()
	out := make([]event.Stats, len(p.public))
	for i, d := range p.public {
		out[i] = d.Stats()
	}
	return out
}
func (p *EventPipeline) RejectedEvents() uint64 { return p.rejected.Load() }
func (p *EventPipeline) Close() error           { return p.CloseContext(context.Background()) }

// CloseContext bounds both legacy drain and the public sink lifecycle. Legacy
// callbacks cannot be interrupted, but a stuck callback cannot block teardown.
func (p *EventPipeline) CloseContext(ctx context.Context) error {
	caller := ctx
	if p == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p.Start()
	p.closeMu.Lock()
	if !p.closing {
		p.closing = true
		close(p.ch)
		p.shutdownDone = make(chan struct{})
		deadline, _ := ctx.Deadline()
		shutdown, stop := context.WithDeadline(caller, deadline)
		go func() { defer stop(); p.shutdown(shutdown) }()
	}
	done := p.shutdownDone
	p.closeMu.Unlock()
	select {
	case <-done:
		p.closeMu.RLock()
		defer p.closeMu.RUnlock()
		return p.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *EventPipeline) shutdown(ctx context.Context) {
	results := make(chan error, len(p.public)+1)
	// One fixed lifecycle goroutine for the legacy pipeline, never one per write.
	go func() {
		<-p.closed
		var err error
		for _, sink := range p.sinks {
			if sink != nil {
				if e := sink.Close(); err == nil {
					err = e
				}
			}
		}
		results <- err
	}()
	for _, d := range p.public {
		go func() { results <- d.Close(ctx) }()
	}
	var err error
	for i := 0; i < len(p.public)+1; i++ {
		select {
		case e := <-results:
			if err == nil {
				err = e
			}
		case <-ctx.Done():
			err = ctx.Err()
			i = len(p.public) + 1
		}
	}
	p.closeMu.Lock()
	p.shutdownErr = err
	p.closeMu.Unlock()
	close(p.shutdownDone)
}

func (p *EventPipeline) run() {
	defer close(p.closed)
	for ev := range p.ch {
		for _, sink := range p.sinks {
			if sink == nil {
				continue
			}
			if err := sink.Write(ev); err != nil {
				p.writeFailures.Add(1)
			}
		}
	}
}
