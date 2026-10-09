package limiter

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Dimension is one ingress traffic class.
type Dimension string

const (
	DimensionLLM   Dimension = "llm"
	DimensionMCP   Dimension = "mcp"
	DimensionAgent Dimension = "agent"
)

// Policy contains validated positive request-token limits.
type Policy struct {
	RequestsPerMinute int
	Burst             int
}

// Clock supports deterministic backend contract fixtures.
type Clock interface{ Now() time.Time }
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// LimiterRequest is an immutable policy snapshot. Generation is an opaque,
// globally unique policy identity, never a bearer key. An unchanged policy keeps
// its generation across extension refreshes. Backends must preserve its state.
type LimiterRequest struct {
	Generation   string
	VirtualKeyID string
	Dimension    Dimension
	Limit        Policy
}

// LimiterDecision reports one atomic consume-or-deny operation. A failure may
// have consumed a token; the caller must never retry it automatically.
type LimiterDecision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// LimiterBackend is the candidate protected domain SPI. All methods honor
// context; diagnostics are replaced by bounded gateway codes. Retire fences a
// generation permanently, including late operations. Admit on a retired
// generation must return ErrLimiterRetired (possibly wrapped); it never fails open.
// Generation-scoped storage
// survives replacement of a module's prepared connection/resource handles.
// Close stops admissions, cancels/drains work, and runs once per owned handle.
// Backend factories initialize connections before extension publication.
type LimiterBackend interface {
	Admit(context.Context, LimiterRequest) (LimiterDecision, error)
	Retire(context.Context, string) error
	Health(context.Context) error
	Close(context.Context) error
}

// LimiterProvider is implemented by a linked extension's prepared resource.
// Selection resolves this interface only during preparation, never per request.
type LimiterProvider interface {
	LimiterBackend(string) (LimiterBackend, error)
}

var ErrLimiterUnavailable = errors.New("limiter_unavailable")
var ErrLimiterRetired = errors.New("limiter_retired")

// MemoryBackend is the Community process-local reference backend. Its lifetime
// is one gateway; it is not a distributed quota authority.
type MemoryBackend struct {
	mu      sync.Mutex
	clock   Clock
	closed  bool
	buckets map[string]*memoryBucket
	retired map[string]bool
}
type memoryBucket struct {
	request LimiterRequest
	limiter *rate.Limiter
}

func NewMemoryBackend(clock Clock) *MemoryBackend {
	if clock == nil {
		clock = systemClock{}
	}
	return &MemoryBackend{clock: clock, buckets: map[string]*memoryBucket{}, retired: map[string]bool{}}
}
func (b *MemoryBackend) Admit(ctx context.Context, r LimiterRequest) (LimiterDecision, error) {
	if err := ctx.Err(); err != nil {
		return LimiterDecision{}, err
	}
	if r.Generation == "" || r.VirtualKeyID == "" || r.Limit.RequestsPerMinute <= 0 || r.Limit.Burst <= 0 {
		return LimiterDecision{}, ErrLimiterUnavailable
	}
	switch r.Dimension {
	case DimensionLLM, DimensionMCP, DimensionAgent:
	default:
		return LimiterDecision{}, ErrLimiterUnavailable
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return LimiterDecision{}, err
	}
	if b.closed {
		return LimiterDecision{}, ErrLimiterUnavailable
	}
	if b.retired[r.Generation] {
		return LimiterDecision{}, ErrLimiterRetired
	}
	bucket := b.buckets[r.Generation]
	if bucket == nil {
		bucket = &memoryBucket{request: r, limiter: rate.NewLimiter(rate.Limit(float64(r.Limit.RequestsPerMinute)/60), r.Limit.Burst)}
		b.buckets[r.Generation] = bucket
	} else if bucket.request != r {
		return LimiterDecision{}, ErrLimiterUnavailable
	}
	now := b.clock.Now()
	if bucket.limiter.AllowN(now, 1) {
		return LimiterDecision{Allowed: true}, nil
	}
	seconds := math.Max(0, 1-bucket.limiter.TokensAt(now)) / (float64(r.Limit.RequestsPerMinute) / 60)
	// Keep conversion bounded even for extreme policies supplied by a backend user.
	retry := time.Duration(math.Ceil(seconds * float64(time.Second)))
	return LimiterDecision{RetryAfter: max(time.Nanosecond, retry)}, nil
}
func (b *MemoryBackend) Retire(ctx context.Context, g string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.retired[g] = true
	delete(b.buckets, g)
	return nil
}
func (b *MemoryBackend) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrLimiterUnavailable
	}
	return nil
}
func (b *MemoryBackend) Close(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.buckets = map[string]*memoryBucket{}
	return ctx.Err()
}
