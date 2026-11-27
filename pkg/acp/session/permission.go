package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
	"go.uber.org/zap"
)

// ErrPermissionNotFound reports a decision for a permission request that is
// unknown, already answered, or expired (the in-agent wait timed out and
// failed closed).
var ErrPermissionNotFound = fmt.Errorf("acp permission request not found")

// pendingPermission is one in-flight interactive session/request_permission
// waiting for a client decision.
type pendingPermission struct {
	info     PendingPermissionInfo
	decision chan acptransport.PermissionResponse
}

// permissionBroker tracks in-flight interactive permission requests so the
// north-side decision endpoint can resolve them by request id. Every entry is
// one-shot: resolving removes it, and the waiting handler removes it on
// timeout or turn teardown (fail closed).
type permissionBroker struct {
	mu      sync.Mutex
	pending map[string]*pendingPermission
}

func newPermissionBroker() *permissionBroker {
	return &permissionBroker{pending: map[string]*pendingPermission{}}
}

func (b *permissionBroker) create(ownerID, sessionID string, data json.RawMessage) (*pendingPermission, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate permission request id: %w", err)
	}
	entry := &pendingPermission{
		info: PendingPermissionInfo{
			RequestID: "perm-" + hex.EncodeToString(raw),
			OwnerID:   ownerID,
			SessionID: sessionID,
			CreatedAt: time.Now().UTC(),
			Data:      data,
		},
		decision: make(chan acptransport.PermissionResponse, 1),
	}
	b.mu.Lock()
	b.pending[entry.info.RequestID] = entry
	b.mu.Unlock()
	return entry, nil
}

func (b *permissionBroker) remove(requestID string) {
	b.mu.Lock()
	delete(b.pending, requestID)
	b.mu.Unlock()
}

// resolve delivers a decision to the waiting permission handler. The entry is
// removed before delivery, so each request resolves at most once.
func (b *permissionBroker) resolve(requestID string, resp acptransport.PermissionResponse) error {
	requestID = strings.TrimSpace(requestID)
	b.mu.Lock()
	entry := b.pending[requestID]
	delete(b.pending, requestID)
	b.mu.Unlock()
	if entry == nil {
		return ErrPermissionNotFound
	}
	entry.decision <- resp
	return nil
}

func (b *permissionBroker) list() []PendingPermissionInfo {
	b.mu.Lock()
	out := make([]PendingPermissionInfo, 0, len(b.pending))
	for _, entry := range b.pending {
		out = append(out, entry.info)
	}
	b.mu.Unlock()
	sort.Slice(out, func(a, b int) bool { return out[a].RequestID < out[b].RequestID })
	return out
}

// permissionFunc applies the pooled Agent's policy while a turn is attached.
func (i *instance) permissionFunc() func(context.Context, json.RawMessage) acptransport.PermissionResponse {
	switch i.cfg.PermissionMode {
	case baseacp.PermissionModeInteractive:
		return i.interactivePermission
	case baseacp.PermissionModeAutoApprove:
		return func(_ context.Context, params json.RawMessage) acptransport.PermissionResponse {
			if i.turnSink() != nil {
				if id := acptransport.AllowOptionID(params); id != "" {
					return acptransport.PermissionResponse{Outcome: acptransport.PermissionOutcomeSelected, SelectedOptionID: id}
				}
			}
			return acptransport.PermissionResponse{Outcome: acptransport.PermissionOutcomeCancelled}
		}
	default:
		return nil
	}
}

func (i *instance) interactivePermission(ctx context.Context, params json.RawMessage) acptransport.PermissionResponse {
	cancelled := acptransport.PermissionResponse{Outcome: acptransport.PermissionOutcomeCancelled}
	i.emitMu.Lock()
	diagnosticCtx := i.turnContext
	i.emitMu.Unlock()
	if diagnosticCtx == nil {
		diagnosticCtx = ctx
	}
	finish := diagnosticPhase(diagnosticCtx, "permission wait")
	var waitErr error
	defer func() { finish(waitErr) }()
	emit := i.turnSink()
	if emit == nil || i.permissions == nil {
		// No streaming turn client to ask — fail closed.
		waitErr = errors.New("permission client unavailable")
		return cancelled
	}
	pending, err := i.permissions.create(i.cfg.OwnerID, i.sessionID, params)
	if err != nil {
		waitErr = err
		return cancelled
	}
	defer i.permissions.remove(pending.info.RequestID)
	expiresAt, _ := ctx.Deadline()
	if err := emit(TurnEvent{Event: "permission", RequestID: pending.info.RequestID, SessionID: i.sessionID, PermissionExpiresAt: expiresAt, Data: params}); err != nil {
		waitErr = err
		return cancelled
	}
	select {
	case resp := <-pending.decision:
		diagnostic(diagnosticCtx, "permission decision", zap.String("outcome", resp.Outcome))
		return resp
	case <-ctx.Done():
		// The transport's fail-closed permission timeout (or teardown) fired
		// before the client answered.
		waitErr = ctx.Err()
		return cancelled
	}
}
