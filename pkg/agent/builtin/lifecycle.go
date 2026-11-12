package builtin

import (
	"context"
	"fmt"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/agent"
)

// PrepareDefinitions retires caches in the existing Agent definition lifecycle.
// Immutable graphs retained by already-running turns remain ordinary GC objects.
func (h *Host) PrepareDefinitions(_ context.Context, agents []agent.Agent) agent.DefinitionCommit {
	next := map[string]time.Time{}
	for _, a := range agents {
		if !a.Disabled && a.Runtime.Type == agent.RuntimeTypeBuiltin {
			next[a.ID] = a.UpdatedAt
		}
	}
	return func() agent.DefinitionCleanup {
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return nil
		}
		var removed []string
		for id := range h.versions {
			if _, ok := next[id]; !ok {
				removed = append(removed, id)
			}
		}
		for id, entry := range h.entries {
			v, ok := next[id]
			if !ok || !v.Equal(entry.updatedAt) {
				delete(h.entries, id)
			}
		}
		// Claim exact old continuations before publication. Delayed cleanup from an
		// earlier update must never remove permissions created by later definitions.
		var retired []*pendingPermission
		h.permissions.mu.Lock()
		for request, p := range h.permissions.byRequest {
			v, ok := next[p.agentID]
			if !ok || !p.updatedAt.Equal(v) {
				retired = append(retired, p)
				delete(h.permissions.byRequest, request)
			}
		}
		h.permissions.mu.Unlock()
		h.sessions.mu.Lock()
		for _, p := range retired {
			if session := h.sessions.agents[p.agentID][p.sessionID]; session != nil {
				session.pendingPermission = false
			}
		}
		for _, id := range removed {
			delete(h.sessions.agents, id)
		}
		h.sessions.mu.Unlock()
		h.versions = next
		h.tracked = true
		h.mu.Unlock()
		return func(context.Context) {
			for _, p := range retired {
				_ = h.checkpoints.Delete(context.Background(), p.requestID)
				h.recordPermissionExpiry(p)
			}
		}
	}
}

func (h *Host) CloseContext(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		h.cancel()
		h.entries = map[string]*hostEntry{}
		h.mu.Unlock()
		go func() {
			h.work.Wait()
			h.permissions.mu.Lock()
			h.permissions.byRequest = map[string]*pendingPermission{}
			h.permissions.mu.Unlock()
			h.checkpoints.mu.Lock()
			h.checkpoints.data = map[string][]byte{}
			h.checkpoints.mu.Unlock()
			h.sessions.mu.Lock()
			h.sessions.agents = map[string]map[string]*session{}
			h.sessions.mu.Unlock()
			close(h.closeDone)
		}()
	})
	select {
	case <-h.closeDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("builtin shutdown: %w", ctx.Err())
	}
}

// GuardContinuation registers bounded permission state atomically with definition
// retirement. The callback must perform only in-memory work.
func (h *Host) GuardContinuation(id string, version time.Time, register func() error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.tracked && !h.versions[id].Equal(version) {
		return fmt.Errorf("%w: builtin definition retired", ErrInvalidRequest)
	}
	return register()
}
