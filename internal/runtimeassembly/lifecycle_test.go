package runtimeassembly

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
)

type lifecyclePipeline struct{ close func() error }

func (*lifecyclePipeline) Start()                {}
func (*lifecyclePipeline) Enqueue(any) bool      { return true }
func (*lifecyclePipeline) DroppedEvents() uint64 { return 0 }
func (*lifecyclePipeline) WriteFailures() uint64 { return 0 }
func (p *lifecyclePipeline) Close() error        { return p.close() }

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func TestLifecycleClosesInOrderOnceAndRetainsErrors(t *testing.T) {
	var order []string
	runtimeErr, usageErr, storeErr := errors.New("runtime"), errors.New("usage"), errors.New("store")
	l := &Lifecycle{
		Runtime: func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("shutdown has no budget")
			}
			order = append(order, "runtime")
			return runtimeErr
		},
		Usage: usage.NewUsageService(&lifecyclePipeline{close: func() error { order = append(order, "usage"); return usageErr }}, nil),
		Store: closeFunc(func() error { order = append(order, "store"); return storeErr }),
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Go(func() {
			err := l.Close()
			if !errors.Is(err, runtimeErr) || !errors.Is(err, usageErr) || !errors.Is(err, storeErr) {
				t.Errorf("lost teardown error: %v", err)
			}
		})
	}
	wg.Wait()
	if !reflect.DeepEqual(order, []string{"runtime", "usage", "store"}) {
		t.Fatalf("teardown order: %v", order)
	}
}

func TestShortConcurrentWaiterDoesNotRetireAnotherLifecycle(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var drains atomic.Int32
	l := &Lifecycle{Runtime: func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, Usage: usage.NewUsageService(&lifecyclePipeline{close: func() error { drains.Add(1); return nil }}, nil)}
	complete := make(chan error, 1)
	go func() { complete <- l.Close() }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := l.CloseContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("short waiter: %v", err)
	}
	if drains.Load() != 0 {
		t.Fatal("usage drained while runtime was still emitting")
	}
	replacement := &Lifecycle{Usage: usage.NewUsageService(&lifecyclePipeline{close: func() error { drains.Add(10); return nil }}, nil)}
	close(release)
	if err := <-complete; err != nil {
		t.Fatal(err)
	}
	if drains.Load() != 1 {
		t.Fatal("old shutdown touched replacement")
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if drains.Load() != 11 {
		t.Fatal("replacement lifecycle was not independently owned")
	}
}

func TestLifecycleReleasesRealConnectionAfterUsage(t *testing.T) {
	b := testBackend(t)
	db, err := b.Database().DB()
	if err != nil {
		t.Fatal(err)
	}
	svc := NewUsage(b, nil, usage.Config{})
	l := &Lifecycle{Usage: svc, Store: b}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.Ping(); err == nil {
		t.Fatal("owned database left open")
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
}
