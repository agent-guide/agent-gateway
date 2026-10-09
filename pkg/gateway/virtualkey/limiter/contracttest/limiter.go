// Package contracttest provides reusable protected limiter conformance suites.
package contracttest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vk "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter"
)

// Limiter exercises atomicity, isolation, non-consuming denials, retry metadata,
// cancellation, permanent retirement, health and close on independently owned backends.
// New must return a fresh backend and honor its context.
func Limiter(t *testing.T, newBackend func(context.Context) (vk.LimiterBackend, error)) {
	t.Helper()
	b, err := newBackend(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := b.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := b.Health(t.Context()); err != nil {
		t.Fatal(err)
	}
	var allowed atomic.Int64
	r := vk.LimiterRequest{Generation: "burst", VirtualKeyID: "key", Dimension: vk.DimensionLLM, Limit: vk.Policy{RequestsPerMinute: 1, Burst: 8}}
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, e := b.Admit(t.Context(), r)
			if e != nil {
				t.Error(e)
			}
			if d.Allowed {
				allowed.Add(1)
			} else if d.RetryAfter <= 0 {
				t.Error("missing retry-after")
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 8 {
		t.Fatalf("atomic burst=%d", allowed.Load())
	}
	for _, dim := range []vk.Dimension{vk.DimensionLLM, vk.DimensionMCP, vk.DimensionAgent} {
		isolated := r
		isolated.Generation = fmt.Sprint(dim)
		isolated.Dimension = dim
		if d, e := b.Admit(t.Context(), isolated); e != nil || !d.Allowed {
			t.Fatalf("dimension %s: %+v %v", dim, d, e)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	r.Generation = "canceled"
	if _, e := b.Admit(canceled, r); e == nil {
		t.Fatal("cancellation ignored")
	}
	if d, e := b.Admit(t.Context(), r); e != nil || !d.Allowed {
		t.Fatal("canceled admission consumed token", e)
	}
	if err := b.Retire(t.Context(), r.Generation); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, e := b.Admit(t.Context(), r); !errors.Is(e, vk.ErrLimiterRetired) {
			t.Fatalf("retired generation must return ErrLimiterRetired: %v", e)
		}
	}
	expired, cancelExpired := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if _, err := b.Admit(expired, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline ignored", err)
	}
	if err := b.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, e := b.Admit(t.Context(), vk.LimiterRequest{Generation: "after-close", VirtualKeyID: "key", Dimension: vk.DimensionLLM, Limit: r.Limit}); e == nil {
		t.Fatal("closed backend admitted")
	}
}

// LimiterRefill verifies that repeated/concurrent denials never reserve future
// tokens. Both implementations receive the same deterministic clock.
func LimiterRefill(t *testing.T, newBackend func(context.Context, vk.Clock) (vk.LimiterBackend, error)) {
	t.Helper()
	clock := &fixtureClock{now: time.Unix(1000, 0)}
	b, err := newBackend(t.Context(), clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	r := vk.LimiterRequest{Generation: "refill", VirtualKeyID: "key", Dimension: vk.DimensionMCP, Limit: vk.Policy{RequestsPerMinute: 60, Burst: 1}}
	if d, e := b.Admit(t.Context(), r); e != nil || !d.Allowed {
		t.Fatal(d, e)
	}
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, e := b.Admit(t.Context(), r)
			if e != nil || d.Allowed || d.RetryAfter != time.Second {
				t.Errorf("denial %+v %v", d, e)
			}
		}()
	}
	wg.Wait()
	clock.advance(time.Second)
	if d, e := b.Admit(t.Context(), r); e != nil || !d.Allowed {
		t.Fatal("denials consumed future refill", d, e)
	}
}

type fixtureClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fixtureClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fixtureClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
