package virtualkey

import (
	"context"
	"errors"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey/limiter"
	"math"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/google/uuid"
)

type policyState struct {
	request limiter.LimiterRequest
	backend limiter.LimiterBackend
}
type limiterController struct {
	mu         sync.Mutex
	memory     *limiter.MemoryBackend
	extensions *extension.Manager
	states     map[limiterKey]policyState
	policies   map[string]*VirtualKeyRateLimits
	deleted    map[string]bool
	identity   string
	sequence   uint64
	closed     bool
	life       context.Context
	cancel     context.CancelFunc
	active     sync.WaitGroup
	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErr   error
	publisher  event.Publisher
	types      map[string]bool
	counts     map[LimiterLabels]uint64
}

// LimiterLabels contain bounded dimension, outcome and backend type only.
type LimiterLabels struct{ Dimension, Outcome, BackendType string }

func newLimiterController(clock Clock) *limiterController {
	ctx, cancel := context.WithCancel(context.Background())
	return &limiterController{memory: limiter.NewMemoryBackend(clock), states: map[limiterKey]policyState{}, policies: map[string]*VirtualKeyRateLimits{}, deleted: map[string]bool{}, life: ctx, cancel: cancel, types: map[string]bool{}, closeDone: make(chan struct{}), counts: map[LimiterLabels]uint64{}}
}
func (c *limiterController) selection() (*limiter.Selection, *extension.Lease, error) {
	if c.extensions == nil {
		return limiter.DefaultSelection(), nil, nil
	}
	lease, err := c.extensions.Acquire()
	if err != nil {
		return nil, nil, limiter.ErrLimiterUnavailable
	}
	r, ok := lease.Snapshot.Resource(limiter.ModuleID)
	if !ok {
		return limiter.DefaultSelection(), lease, nil
	}
	s, ok := r.(*limiter.Selection)
	if !ok {
		lease.Release()
		return nil, nil, limiter.ErrLimiterUnavailable
	}
	return s, lease, nil
}
func backendCall[T any](fn func() (T, error)) (v T, err error) {
	defer func() {
		if recover() != nil {
			err = limiter.ErrLimiterUnavailable
		}
	}()
	return fn()
}
func retireStates(ctx context.Context, states []policyState) {
	for _, s := range states {
		_, _ = backendCall(func() (bool, error) { return false, s.backend.Retire(ctx, s.request.Generation) })
	}
}
func (c *limiterController) remove(ctx context.Context, id string, policy *VirtualKeyRateLimits, deleted bool) {
	selection, lease, _ := c.selection()
	if lease != nil {
		defer lease.Release()
	}
	c.mu.Lock()
	c.policies[id] = cloneRateLimits(policy)
	c.deleted[id] = deleted
	retired := []policyState{}
	for k, s := range c.states {
		next, _ := rateLimitForDimension(policy, k.dimension)
		if k.virtualKeyID == id && (deleted || next == nil || s.request.Limit != (limiter.Policy{RequestsPerMinute: next.RequestsPerMinute, Burst: next.Burst})) {
			if selection != nil && selection.Identity() == c.identity {
				b := selection.Backend()
				if b == nil {
					b = c.memory
				}
				s.backend = b
			}
			retired = append(retired, s)
			delete(c.states, k)
		}
	}
	c.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	retireStates(bounded, retired)
	if len(retired) > 0 {
		c.emitReset(ctx)
	}
}
func (c *limiterController) remember(key VirtualKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.policies[key.ID]; !exists && !c.deleted[key.ID] {
		c.policies[key.ID] = cloneRateLimits(key.RateLimits)
	}
}
func (c *limiterController) admit(ctx context.Context, key VirtualKey, dim RateLimitDimension) (result Admission, err error) {
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	s, lease, err := c.selection()
	if err != nil {
		return result, err
	}
	if lease != nil {
		defer lease.Release()
	}
	c.mu.Lock()
	if c.closed || c.deleted[key.ID] {
		c.mu.Unlock()
		return result, limiter.ErrLimiterUnavailable
	}
	limits := key.RateLimits
	if p, ok := c.policies[key.ID]; ok {
		limits = p
	}
	limit, err := rateLimitForDimension(limits, dim)
	if err != nil || key.ID == "" {
		c.mu.Unlock()
		return result, limiter.ErrLimiterUnavailable
	}
	result.BackendID = s.Configuration().BackendModuleID
	result.BackendType = s.Configuration().BackendType
	if limit == nil {
		c.mu.Unlock()
		result.Allowed = true
		return result, nil
	}
	result.RequestsPerMinute = limit.RequestsPerMinute
	result.Burst = limit.Burst
	sequence := uint64(0)
	if lease != nil {
		sequence = lease.Snapshot.Sequence()
	}
	if c.identity != s.Identity() && sequence < c.sequence {
		c.mu.Unlock()
		return result, limiter.ErrLimiterUnavailable
	}
	retired := []policyState{}
	if c.identity != s.Identity() {
		for k, state := range c.states {
			retired = append(retired, state)
			delete(c.states, k)
		}
		c.identity = s.Identity()
	}
	c.sequence = max(c.sequence, sequence)
	k := limiterKey{virtualKeyID: key.ID, dimension: dim}
	state, ok := c.states[k]
	backend := s.Backend()
	if backend == nil {
		backend = c.memory
	}
	if ok {
		state.backend = backend
		c.states[k] = state
	}
	if !ok || state.request.Limit != (limiter.Policy{RequestsPerMinute: limit.RequestsPerMinute, Burst: limit.Burst}) {
		if ok {
			retired = append(retired, state)
		}
		state = policyState{backend: backend, request: limiter.LimiterRequest{Generation: uuid.NewString(), VirtualKeyID: key.ID, Dimension: limiter.Dimension(dim), Limit: limiter.Policy{RequestsPerMinute: limit.RequestsPerMinute, Burst: limit.Burst}}}
		c.states[k] = state
	}
	c.active.Add(1)
	c.mu.Unlock()
	defer c.active.Done()
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	stopLife := context.AfterFunc(c.life, cancel)
	defer stopLife()
	if lease != nil {
		stop := context.AfterFunc(lease.Snapshot.Context(), cancel)
		defer stop()
	}
	// Retirement has its own cleanup budget; it must not consume the fresh
	// generation's admission budget. Both phases still obey caller and lifecycle
	// cancellation, and execute under the same retained extension lease.
	retirement, stopRetirement := context.WithTimeout(operation, 100*time.Millisecond)
	retireStates(retirement, retired)
	stopRetirement()
	bounded, stopAdmission := context.WithTimeout(operation, s.Budget())
	defer stopAdmission()
	decision, callErr := backendCall(func() (limiter.LimiterDecision, error) { return state.backend.Admit(bounded, state.request) })
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if c.life.Err() != nil {
		err = limiter.ErrLimiterUnavailable
	} else if lease != nil && lease.Snapshot.Context().Err() != nil {
		err = context.Canceled
	} else if errors.Is(callErr, limiter.ErrLimiterRetired) {
		err = limiter.ErrLimiterRetired
	} else if bounded.Err() != nil || callErr != nil || decision.RetryAfter < 0 {
		err = limiter.ErrLimiterUnavailable
	} else {
		result.Allowed = decision.Allowed
		if !result.Allowed {
			result.RetryAfterSeconds = max(1, int(math.Ceil(decision.RetryAfter.Seconds())))
		}
	}
	outcome := "allow"
	if err != nil {
		outcome = "unavailable"
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, limiter.ErrLimiterRetired) && s.Configuration().FailureMode == "open" && c.life.Err() == nil {
			result.Allowed = true
			result.Degraded = true
			err = nil
			outcome = "degraded"
		}
	} else if !result.Allowed {
		outcome = "deny"
	}
	c.mu.Lock()
	labelType := result.BackendType
	if !c.types[labelType] && len(c.types) >= 32 {
		labelType = "other"
	} else {
		c.types[labelType] = true
	}
	c.counts[LimiterLabels{Dimension: string(dim), Outcome: outcome, BackendType: labelType}]++
	c.mu.Unlock()
	return result, err
}
func (c *limiterController) reset() {
	c.mu.Lock()
	retired := []policyState{}
	for _, s := range c.states {
		retired = append(retired, s)
	}
	c.states = map[limiterKey]policyState{}
	c.policies = map[string]*VirtualKeyRateLimits{}
	c.deleted = map[string]bool{}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	retireStates(ctx, retired)
	if len(retired) > 0 {
		c.emitReset(ctx)
	}
}
func (c *limiterController) close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		c.mu.Unlock()
		budget, cancel := context.WithTimeout(ctx, extension.LifecycleBudget)
		go func() {
			defer close(c.closeDone)
			defer cancel()
			done := make(chan struct{})
			go func() { c.active.Wait(); close(done) }()
			select {
			case <-done:
			case <-budget.Done():
				c.closeErr = budget.Err()
			}
			if err := c.memory.Close(budget); c.closeErr == nil {
				c.closeErr = err
			}
		}()
	})
	select {
	case <-c.closeDone:
		return c.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetExtensions binds the already assembled extension manager during bootstrap.
func (m *VirtualKeyManager) SetExtensions(e *extension.Manager) { m.limiters.extensions = e }

// LimiterCounters returns an owned low-cardinality snapshot.
func (m *VirtualKeyManager) LimiterCounters() map[LimiterLabels]uint64 {
	m.limiters.mu.Lock()
	defer m.limiters.mu.Unlock()
	out := map[LimiterLabels]uint64{}
	for k, v := range m.limiters.counts {
		out[k] = v
	}
	return out
}

// Close stops new admission and shares the caller's deadline with active calls.
func (m *VirtualKeyManager) Close(ctx context.Context) error { return m.limiters.close(ctx) }

func (c *limiterController) emitReset(ctx context.Context) {
	if c.publisher == nil {
		return
	}
	v, _ := attribution.FromContext(ctx)
	c.publisher.Publish(ctx, event.Envelope{SchemaVersion: 1, ID: uuid.NewString(), Family: "extension.mutation", Timestamp: time.Now().UTC(), Attribution: v, Outcome: "success", Mutation: &event.ExtensionMutation{ModuleID: limiter.ModuleID, Operation: "reset", StateReset: true}})
}

// SetPublisher injects the existing event authority at bootstrap.
func (m *VirtualKeyManager) SetPublisher(p event.Publisher) { m.limiters.publisher = p }
