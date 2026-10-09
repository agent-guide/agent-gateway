package event

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testSink struct {
	mu      sync.Mutex
	ids     []string
	block   chan struct{}
	entered chan struct{}
	once    sync.Once
	fail    bool
	closes  atomic.Int32
}

func (s *testSink) Write(ctx context.Context, e Envelope) error {
	if s.entered != nil {
		s.once.Do(func() { close(s.entered) })
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	s.ids = append(s.ids, e.ID)
	s.mu.Unlock()
	if s.fail {
		return errors.New("private diagnostic")
	}
	return nil
}
func (s *testSink) Flush(ctx context.Context) error { return ctx.Err() }
func (s *testSink) Close(ctx context.Context) error { s.closes.Add(1); return ctx.Err() }
func fixture(id string) Envelope {
	return Envelope{SchemaVersion: 1, ID: id, Timestamp: time.Now().UTC(), Family: "extension.mutation", Outcome: "success", Mutation: &ExtensionMutation{ModuleID: "example.fixture", Operation: "put"}}
}
func TestFIFOAndCloseOnce(t *testing.T) {
	s := &testSink{}
	d, err := NewDelivery(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if !d.Publish(context.Background(), fixture(id)) {
			t.Fatal("drop")
		}
	}
	if err := d.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if len(s.ids) != 3 || s.ids[0] != "a" || s.ids[2] != "c" {
		t.Fatal(s.ids)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if s.closes.Load() != 1 || d.Publish(context.Background(), fixture("late")) {
		t.Fatal("close lifecycle")
	}
}
func TestOverflowCancellationAndIsolation(t *testing.T) {
	slow := &testSink{block: make(chan struct{}), entered: make(chan struct{})}
	d, _ := NewDelivery(slow, Options{QueueSize: 1})
	d.Publish(context.Background(), fixture("active"))
	<-slow.entered
	if !d.Publish(context.Background(), fixture("queued")) || d.Publish(context.Background(), fixture("drop")) {
		t.Fatal("queue bounds")
	}
	fast := &testSink{fail: true}
	other, _ := NewDelivery(fast, Options{})
	other.Publish(context.Background(), fixture("independent"))
	_ = other.Flush(context.Background())
	if other.Stats().Failures != 1 {
		t.Fatal(other.Stats())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if d.Close(ctx) == nil || time.Since(start) > time.Second {
		t.Fatal("shutdown budget")
	}
	close(slow.block)
	_ = d.Close(context.Background())
	_ = other.Close(context.Background())
	if d.Stats().Dropped < 2 {
		t.Fatal(d.Stats())
	}
}
func TestRejectBeforeQueueAndCopy(t *testing.T) {
	s := &testSink{block: make(chan struct{}), entered: make(chan struct{})}
	d, _ := NewDelivery(s, Options{})
	e := fixture("first")
	d.Publish(context.Background(), e)
	<-s.entered
	bad := fixture("bad")
	bad.Reason = "raw error"
	if d.Publish(context.Background(), bad) {
		t.Fatal("invalid event")
	}
	e = fixture("owned")
	d.Publish(context.Background(), e)
	e.Mutation.ModuleID = "mutated"
	close(s.block)
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.Stats().Rejected != 1 || d.Stats().Delivered != 2 {
		t.Fatal(d.Stats())
	}
}

func TestConcurrentShortCloseDoesNotAbortSharedDrain(t *testing.T) {
	s := &testSink{block: make(chan struct{}), entered: make(chan struct{})}
	d, _ := NewDelivery(s, Options{})
	d.Publish(context.Background(), fixture("active"))
	<-s.entered
	d.Publish(context.Background(), fixture("queued"))
	result := make(chan error, 1)
	go func() { result <- d.Close(context.Background()) }()
	for {
		d.mu.Lock()
		closing := d.closing
		d.mu.Unlock()
		if closing {
			break
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if d.Close(ctx) == nil {
		t.Fatal("short caller should time out")
	}
	if d.Stats().Dropped != 0 || !d.Stats().Healthy {
		t.Fatal("short waiter changed shared drain")
	}
	close(s.block)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if d.Stats().Delivered != 2 {
		t.Fatal(d.Stats())
	}
}

type stuckSink struct {
	testSink
	release chan struct{}
}

func (s *stuckSink) Write(context.Context, Envelope) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return nil
}
func TestNonCooperativeSinkCannotBlockTeardown(t *testing.T) {
	s := &stuckSink{testSink: testSink{entered: make(chan struct{})}, release: make(chan struct{})}
	d, _ := NewDelivery(s, Options{})
	d.Publish(context.Background(), fixture("stuck"))
	<-s.entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if d.Close(ctx) == nil || d.Stats().Healthy {
		t.Fatal("noncooperative sink not reported")
	}
	close(s.release)
	_ = d.Close(context.Background())
	if s.closes.Load() != 1 {
		t.Fatal("close count")
	}
}

type blockingFlushSink struct {
	testSink
	flushing chan struct{}
	stopped  chan struct{}
}

func (s *blockingFlushSink) Flush(ctx context.Context) error {
	s.once.Do(func() { close(s.flushing) })
	<-ctx.Done()
	select {
	case s.stopped <- struct{}{}:
	default:
	}
	return ctx.Err()
}
func TestShutdownCancelsInFlightFlush(t *testing.T) {
	s := &blockingFlushSink{flushing: make(chan struct{}), stopped: make(chan struct{}, 2)}
	d, _ := NewDelivery(s, Options{})
	result := make(chan error, 1)
	go func() { result <- d.Flush(context.Background()) }()
	<-s.flushing
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_ = d.Close(ctx)
	select {
	case <-s.stopped:
	case <-time.After(time.Second):
		t.Fatal("flush did not receive shutdown cancellation")
	}
	if <-result == nil {
		t.Fatal("flush cancellation hidden")
	}
	_ = d.Close(context.Background())
}
func TestFlushBarrierDoesNotConsumeEventCapacity(t *testing.T) {
	s := &testSink{block: make(chan struct{}), entered: make(chan struct{})}
	d, _ := NewDelivery(s, Options{QueueSize: 1})
	d.Publish(context.Background(), fixture("active"))
	<-s.entered
	done := make(chan error, 1)
	go func() { done <- d.Flush(context.Background()) }()
	for {
		d.mu.Lock()
		n := d.queuedBarriers
		d.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !d.Publish(context.Background(), fixture("after-barrier")) {
		t.Fatal("control barrier consumed event capacity")
	}
	close(s.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCloseHonorsCallerCancellationWithoutDeadline(t *testing.T) {
	s := &testSink{block: make(chan struct{}), entered: make(chan struct{})}
	d, _ := NewDelivery(s, Options{})
	d.Publish(context.Background(), fixture("active"))
	<-s.entered
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- d.Close(ctx) }()
	for {
		d.mu.Lock()
		closing := d.closing
		d.mu.Unlock()
		if closing {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-d.done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("caller cancellation was reduced to a deadline")
	}
	if d.Stats().Healthy {
		t.Fatal("canceled lifecycle not reported")
	}
}

type diagnosticSink struct {
	testSink
	flushErr, closeErr error
}

func (s *diagnosticSink) Flush(context.Context) error { return s.flushErr }
func (s *diagnosticSink) Close(context.Context) error { return s.closeErr }
func TestWrappedCancellationDiagnosticsCannotEscape(t *testing.T) {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, phase := range []string{"flush", "close"} {
			t.Run(phase+"/"+sentinel.Error(), func(t *testing.T) {
				wrapped := fmt.Errorf("fixture-private-diagnostic: %w", sentinel)
				s := &diagnosticSink{}
				if phase == "flush" {
					s.flushErr = wrapped
				} else {
					s.closeErr = wrapped
				}
				d, err := NewDelivery(s, Options{})
				if err != nil {
					t.Fatal(err)
				}
				defer d.Close(context.Background())
				if phase == "flush" {
					err = d.Flush(context.Background())
				} else {
					err = d.Close(context.Background())
				}
				if !errors.Is(err, sentinel) || strings.Contains(err.Error(), "fixture-private") || err.Error() != sentinel.Error() {
					t.Fatalf("callback diagnostic escaped: %v", err)
				}
			})
		}
	}
}

type countedFlushSink struct {
	testSink
	flushes atomic.Int32
}

func (s *countedFlushSink) Flush(ctx context.Context) error { s.flushes.Add(1); return ctx.Err() }
func TestAbandonedFlushDoesNotPoisonSinkHealth(t *testing.T) {
	s := &countedFlushSink{testSink: testSink{block: make(chan struct{}), entered: make(chan struct{})}}
	d, _ := NewDelivery(s, Options{})
	defer d.Close(context.Background())
	d.Publish(context.Background(), fixture("active"))
	<-s.entered
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := d.Flush(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	close(s.block)
	if err := d.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.flushes.Load() != 1 || d.Stats().Failures != 0 || !d.Stats().Healthy {
		t.Fatalf("abandoned barrier changed sink health: flushes %d stats %+v", s.flushes.Load(), d.Stats())
	}
}
