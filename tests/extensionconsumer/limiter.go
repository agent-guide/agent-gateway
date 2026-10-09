package extensionconsumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter"
)

const fixtureLimiterID = "example.fixture.limiter"

type limiterSettings struct {
	Namespace string `json:"namespace"`
	Mode      string `json:"mode"`
}

// This independently implemented store models shared remote quota storage.
// Prepared connection handles close independently; generations remain fenced.
type quotaStorage struct {
	mu      sync.Mutex
	buckets map[string]*quotaBucket
	retired map[string]bool
}
type quotaBucket struct {
	request limiter.LimiterRequest
	tokens  float64
	last    time.Time
}
type fixtureLimiter struct {
	clock    limiter.Clock
	storage  *quotaStorage
	settings limiterSettings
	mu       sync.Mutex
	closed   bool
	life     context.Context
	cancel   context.CancelFunc
}

func newQuotaStorage() *quotaStorage {
	return &quotaStorage{buckets: map[string]*quotaBucket{}, retired: map[string]bool{}}
}
func newFixtureLimiter(storage *quotaStorage, settings limiterSettings) *fixtureLimiter {
	ctx, cancel := context.WithCancel(context.Background())
	return &fixtureLimiter{storage: storage, settings: settings, life: ctx, cancel: cancel}
}
func (b *fixtureLimiter) Health(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("closed fixture backend")
	}
	return nil
}
func (b *fixtureLimiter) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.cancel()
	b.mu.Unlock()
	return ctx.Err()
}
func (b *fixtureLimiter) LimiterBackend(name string) (limiter.LimiterBackend, error) {
	if name != "fixture" {
		return nil, errors.New("unknown backend")
	}
	return b, nil
}
func (b *fixtureLimiter) Retire(ctx context.Context, g string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	b.storage.mu.Lock()
	defer b.storage.mu.Unlock()
	key := b.settings.Namespace + "/" + g
	b.storage.retired[key] = true
	delete(b.storage.buckets, key)
	return nil
}
func (b *fixtureLimiter) Admit(ctx context.Context, r limiter.LimiterRequest) (limiter.LimiterDecision, error) {
	if err := b.Health(ctx); err != nil {
		return limiter.LimiterDecision{}, err
	}
	switch b.settings.Mode {
	case "error":
		return limiter.LimiterDecision{}, errors.New("fixture private backend failure")
	case "timeout":
		select {
		case <-ctx.Done():
			return limiter.LimiterDecision{}, ctx.Err()
		case <-b.life.Done():
			return limiter.LimiterDecision{}, errors.New("closed")
		}
	}
	if r.Generation == "" || r.VirtualKeyID == "" || r.Limit.RequestsPerMinute <= 0 || r.Limit.Burst <= 0 {
		return limiter.LimiterDecision{}, errors.New("invalid policy")
	}
	switch r.Dimension {
	case limiter.DimensionLLM, limiter.DimensionMCP, limiter.DimensionAgent:
	default:
		return limiter.LimiterDecision{}, errors.New("invalid dimension")
	}
	b.storage.mu.Lock()
	defer b.storage.mu.Unlock()
	if ctx.Err() != nil {
		return limiter.LimiterDecision{}, ctx.Err()
	}
	if b.life.Err() != nil {
		return limiter.LimiterDecision{}, errors.New("closed")
	}
	key := b.settings.Namespace + "/" + r.Generation
	if b.storage.retired[key] {
		return limiter.LimiterDecision{}, limiter.ErrLimiterRetired
	}
	now := time.Now()
	if b.clock != nil {
		now = b.clock.Now()
	}
	bucket := b.storage.buckets[key]
	if bucket == nil {
		bucket = &quotaBucket{request: r, tokens: float64(r.Limit.Burst), last: now}
		b.storage.buckets[key] = bucket
	} else if bucket.request != r {
		return limiter.LimiterDecision{}, errors.New("policy changed within generation")
	}
	refill := float64(r.Limit.RequestsPerMinute) / 60
	bucket.tokens = math.Min(float64(r.Limit.Burst), bucket.tokens+now.Sub(bucket.last).Seconds()*refill)
	bucket.last = now
	if bucket.tokens < 1 {
		return limiter.LimiterDecision{RetryAfter: time.Duration(math.Ceil((1 - bucket.tokens) / refill * float64(time.Second)))}, nil
	}
	bucket.tokens--
	if b.settings.Mode == "ambiguous" {
		return limiter.LimiterDecision{}, errors.New("remote consumed; reply lost")
	}
	return limiter.LimiterDecision{Allowed: true}, nil
}
func fixtureLimiterFactory(runtime bool) *extension.Factory {
	storage := newQuotaStorage()
	f := &extension.Factory{Validate: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		c := limiterSettings{Namespace: "default", Mode: "healthy"}
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Namespace == "" || len(c.Namespace) > 64 {
			return nil, errors.New("invalid limiter configuration")
		}
		switch c.Mode {
		case "healthy", "error", "timeout", "ambiguous":
		default:
			return nil, errors.New("invalid mode")
		}
		return json.Marshal(c)
	}}
	if runtime {
		f.Prepare = func(ctx context.Context, c extension.PreparedConfig, _ extension.Snapshot) (extension.Resource, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var settings limiterSettings
			if json.Unmarshal(c.Envelope.Config, &settings) != nil {
				return nil, errors.New("invalid config")
			}
			return newFixtureLimiter(storage, settings), nil
		}
	}
	return f
}
