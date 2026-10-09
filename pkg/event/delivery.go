package event

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/attribution"
)

// Options bounds one sink's transport. Zero values select the contract defaults.
// Policy must be the assembly-owned attribution allowlist.
type Options struct {
	QueueSize   int
	WriteBudget time.Duration
	Policy      attribution.Policy
}
type Stats struct {
	Accepted  uint64 `json:"accepted"`
	Delivered uint64 `json:"delivered"`
	Dropped   uint64 `json:"dropped"`
	Rejected  uint64 `json:"rejected"`
	Failures  uint64 `json:"failures"`
	Healthy   bool   `json:"healthy"`
}
type item struct {
	event   *Envelope
	barrier chan error
	ctx     context.Context
}

// Delivery is a single worker owned by the existing gateway pipeline. Publish
// never waits for I/O. Sink callbacks execute serially; errors are never retried.
type Delivery struct {
	mu             sync.Mutex
	sink           Sink
	options        Options
	queue          []item
	queuedEvents   int
	queuedBarriers int
	wake           chan struct{}
	changed        chan struct{}
	done           chan struct{}
	closing        bool
	shutdown       context.Context
	life           context.Context
	cancel         context.CancelFunc
	stats          Stats
	closeErr       error
}

func NewDelivery(s Sink, o Options) (*Delivery, error) {
	if s == nil || (reflect.ValueOf(s).Kind() == reflect.Pointer && reflect.ValueOf(s).IsNil()) {
		return nil, &Error{Code: "invalid_sink"}
	}
	if o.QueueSize == 0 {
		o.QueueSize = 256
	}
	if o.WriteBudget == 0 {
		o.WriteBudget = time.Second
	}
	if o.QueueSize < 1 || o.QueueSize > 4096 || o.WriteBudget <= 0 || o.WriteBudget > time.Second {
		return nil, &Error{Code: "invalid_sink_options"}
	}
	life, cancel := context.WithCancel(context.Background())
	d := &Delivery{sink: s, options: o, wake: make(chan struct{}, 1), changed: make(chan struct{}), done: make(chan struct{}), life: life, cancel: cancel, stats: Stats{Healthy: true}}
	go d.run()
	return d, nil
}
func (d *Delivery) signal() {
	close(d.changed)
	d.changed = make(chan struct{})
	select {
	case d.wake <- struct{}{}:
	default:
	}
}
func clone(e Envelope) Envelope {
	e.Attribution.Attributes = maps.Clone(e.Attribution.Attributes)
	if e.Limiter != nil {
		x := *e.Limiter
		e.Limiter = &x
	}
	if e.Mutation != nil {
		x := *e.Mutation
		e.Mutation = &x
	}
	return e
}
func (d *Delivery) Publish(ctx context.Context, e Envelope) bool {
	if err := Validate(e, d.options.Policy); err != nil {
		d.mu.Lock()
		d.stats.Rejected++
		d.mu.Unlock()
		return false
	}
	e = clone(e)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closing || d.queuedEvents >= d.options.QueueSize {
		d.stats.Dropped++
		return false
	}
	d.queue = append(d.queue, item{event: &e})
	d.queuedEvents++
	d.stats.Accepted++
	d.signal()
	return true
}

// Flush inserts a FIFO barrier and waits only for events accepted before it.
// It never calls the sink concurrently with Write or Close.
func (d *Delivery) Flush(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b := make(chan error, 1)
	for {
		d.mu.Lock()
		if d.closing {
			d.mu.Unlock()
			return &Error{Code: "closed"}
		}
		if d.queuedBarriers < d.options.QueueSize {
			d.queue = append(d.queue, item{barrier: b, ctx: ctx})
			d.queuedBarriers++
			d.signal()
			d.mu.Unlock()
			break
		}
		changed := d.changed
		d.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case err := <-b:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (d *Delivery) Stats() Stats { d.mu.Lock(); defer d.mu.Unlock(); return d.stats }

// Close rejects new events immediately. The first call owns the shared shutdown
// budget; concurrent calls wait with their own (possibly shorter) deadline.
func (d *Delivery) Close(ctx context.Context) error {
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	d.mu.Lock()
	if !d.closing {
		d.closing = true
		// Own the deadline independently of this call's deferred cancellation.
		deadline, _ := ctx.Deadline()
		shutdown, stop := context.WithDeadline(caller, deadline)
		d.shutdown = shutdown
		go func() {
			select {
			case <-shutdown.Done():
				d.cancel()
			case <-d.done:
			}
			stop()
		}()
		d.signal()
	}
	d.mu.Unlock()
	select {
	case <-d.done:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.closeErr
	case <-ctx.Done():
		d.mu.Lock()
		deadline, _ := d.shutdown.Deadline()
		if d.shutdown.Err() != nil || !time.Now().Before(deadline) {
			d.cancel()
			d.stats.Healthy = false
			d.dropQueued()
		}
		d.mu.Unlock()
		return ctx.Err()
	}
}
func (d *Delivery) dropQueued() {
	for _, x := range d.queue {
		if x.event != nil {
			d.stats.Dropped++
		} else {
			x.barrier <- &Error{Code: "closed"}
		}
	}
	d.queue = nil
	d.queuedEvents = 0
	d.queuedBarriers = 0
	d.signal()
}
func safe(call func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = &Error{Code: "sink_panic"}
		}
	}()
	err = call()
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return &Error{Code: "sink_failure"}
	}
}
func (d *Delivery) failure(err error) {
	if err != nil {
		d.mu.Lock()
		d.stats.Failures++
		d.stats.Healthy = false
		d.mu.Unlock()
	}
}
func (d *Delivery) run() {
	defer close(d.done)
	defer d.cancel()
	for {
		d.mu.Lock()
		if d.closing && d.shutdown.Err() != nil {
			d.dropQueued()
		}
		if len(d.queue) == 0 {
			if d.closing {
				ctx := d.shutdown
				d.mu.Unlock()
				err := safe(func() error { return d.sink.Flush(ctx) })
				if err == nil && ctx.Err() != nil {
					err = ctx.Err()
				}
				d.failure(err)
				cerr := safe(func() error { return d.sink.Close(ctx) })
				d.failure(cerr)
				if err == nil {
					err = cerr
				}
				d.mu.Lock()
				d.closeErr = err
				d.mu.Unlock()
				return
			}
			d.mu.Unlock()
			<-d.wake
			continue
		}
		x := d.queue[0]
		d.queue = d.queue[1:]
		if x.event != nil {
			d.queuedEvents--
		} else {
			d.queuedBarriers--
		}
		d.signal()
		d.mu.Unlock()
		if x.event != nil {
			ctx, cancel := context.WithTimeout(d.life, d.options.WriteBudget)
			err := safe(func() error { return d.sink.Write(ctx, *x.event) })
			if err == nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			cancel()
			d.failure(err)
			d.mu.Lock()
			if err == nil {
				d.stats.Delivered++
			}
			d.mu.Unlock()
		} else {
			if err := x.ctx.Err(); err != nil {
				x.barrier <- err
				continue
			}
			ctx, cancel := context.WithCancel(x.ctx)
			stop := context.AfterFunc(d.life, cancel)
			err := safe(func() error { return d.sink.Flush(ctx) })
			if err == nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			stop()
			cancel()
			// A caller abandoning its barrier does not imply a faulty sink. Shared
			// shutdown cancellation remains a lifecycle failure and is observable.
			if x.ctx.Err() == nil || !errors.Is(err, x.ctx.Err()) || d.life.Err() != nil {
				d.failure(err)
			}
			x.barrier <- err
		}
	}
}
