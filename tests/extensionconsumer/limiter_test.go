package extensionconsumer

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter/contracttest"
	"testing"
)

func TestIndependentLimiterBackendContract(t *testing.T) {
	contracttest.Limiter(t, func(context.Context) (limiter.LimiterBackend, error) {
		return newFixtureLimiter(newQuotaStorage(), limiterSettings{Namespace: "contract", Mode: "healthy"}), nil
	})
}
func TestCommunityLimiterBackendContract(t *testing.T) {
	contracttest.Limiter(t, func(context.Context) (limiter.LimiterBackend, error) { return limiter.NewMemoryBackend(nil), nil })
}
func TestIndependentPreparedHandlesPreserveState(t *testing.T) {
	storage := newQuotaStorage()
	a := newFixtureLimiter(storage, limiterSettings{Namespace: "shared"})
	b := newFixtureLimiter(storage, limiterSettings{Namespace: "shared"})
	t.Cleanup(func() { _ = a.Close(t.Context()); _ = b.Close(t.Context()) })
	r := limiter.LimiterRequest{Generation: "unchanged", VirtualKeyID: "key", Dimension: limiter.DimensionAgent, Limit: limiter.Policy{RequestsPerMinute: 1, Burst: 1}}
	if d, e := a.Admit(t.Context(), r); e != nil || !d.Allowed {
		t.Fatal(d, e)
	}
	if e := a.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	if d, e := b.Admit(t.Context(), r); e != nil || d.Allowed {
		t.Fatal("refresh reset tokens", d, e)
	}
	if e := b.Retire(t.Context(), r.Generation); e != nil {
		t.Fatal(e)
	}
	if _, e := b.Admit(t.Context(), r); e == nil {
		t.Fatal("retired state recreated")
	}
}

func TestIndependentLimiterRefillContract(t *testing.T) {
	contracttest.LimiterRefill(t, func(_ context.Context, clock limiter.Clock) (limiter.LimiterBackend, error) {
		b := newFixtureLimiter(newQuotaStorage(), limiterSettings{Namespace: "refill", Mode: "healthy"})
		b.clock = clock
		return b, nil
	})
}
func TestCommunityLimiterRefillContract(t *testing.T) {
	contracttest.LimiterRefill(t, func(_ context.Context, clock limiter.Clock) (limiter.LimiterBackend, error) {
		return limiter.NewMemoryBackend(clock), nil
	})
}
