package httpapi

import (
	"context"
	"sync"
)

type Limits struct {
	GlobalActive, GlobalQueued, ConnectorActive, ConnectorQueued int
}

// admission coordinates both dimensions; waiting never holds an active slot.
type admission struct {
	mu             sync.Mutex
	limits         Limits
	active, queued int
	byID           map[string]int
	waiting        map[string]int
	changed        chan struct{}
}

func newAdmission(l Limits) *admission {
	return &admission{limits: l, byID: map[string]int{}, waiting: map[string]int{}, changed: make(chan struct{})}
}
func (a *admission) acquire(ctx context.Context, id string) (func(), error) {
	a.mu.Lock()
	queued := false
	for {
		if ctx.Err() != nil {
			if queued {
				a.queued--
				a.waiting[id]--
			}
			a.mu.Unlock()
			return nil, ctx.Err()
		}
		if a.active < a.limits.GlobalActive && a.byID[id] < a.limits.ConnectorActive {
			if queued {
				a.queued--
				a.waiting[id]--
			}
			a.active++
			a.byID[id]++
			a.mu.Unlock()
			return func() {
				a.mu.Lock()
				a.active--
				a.byID[id]--
				if a.byID[id] == 0 {
					delete(a.byID, id)
				}
				close(a.changed)
				a.changed = make(chan struct{})
				a.mu.Unlock()
			}, nil
		}
		if !queued {
			if a.queued >= a.limits.GlobalQueued || a.waiting[id] >= a.limits.ConnectorQueued {
				a.mu.Unlock()
				return nil, failure("capacity_exceeded")
			}
			a.queued++
			a.waiting[id]++
			queued = true
		}
		ch := a.changed
		a.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-ch:
		}
		a.mu.Lock()
	}
}
