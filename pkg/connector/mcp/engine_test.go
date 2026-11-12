package mcp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	"github.com/agent-guide/agent-gateway/pkg/mcp/transport"
)

type closingTransport struct {
	close func() error
}

func (*closingTransport) Call(context.Context, *transport.Message) (*transport.Message, error) {
	return nil, errors.New("unused")
}
func (c *closingTransport) Close() error { return c.close() }

func TestExpiredSessionRetirementUsesAdmissionAuthorityAndClosesOutsideLock(t *testing.T) {
	var admission sync.Mutex
	active := 2
	engine := NewEngine(func(_ string, detach func()) bool {
		admission.Lock()
		defer admission.Unlock()
		if active > 1 {
			return false
		}
		detach()
		return true
	})
	closed := false
	sentinel := errors.New("session close failure")
	session := &Session{configSignature: "same", lastUsedAt: time.Now().Add(-2 * idleSessionTTL), transport: &closingTransport{close: func() error {
		if !admission.TryLock() {
			t.Error("network close ran under admission lock")
		} else {
			admission.Unlock()
		}
		closed = true
		return sentinel
	}}}
	engine.upstreamSessions["tools"] = session
	if got := engine.lookupSession("tools", "same"); got != session || closed {
		t.Fatal("retired a session owned by competing work")
	}
	active = 1
	// The first lookup touched the session; make it idle again.
	session.lastUsedAt = time.Now().Add(-2 * idleSessionTTL)
	if got := engine.lookupSession("tools", "same"); got != nil || !closed {
		t.Fatal("idle exclusive session was not retired")
	}
	<-engine.BeginClose()
	if !errors.Is(engine.CloseError(), sentinel) {
		t.Fatalf("lost close error: %v", engine.CloseError())
	}
}

func TestEngineShutdownRejectsLateSessionPublication(t *testing.T) {
	engine := NewEngine(nil)
	<-engine.BeginClose()
	sentinel := errors.New("late close failure")
	closed := false
	session := &Session{transport: &closingTransport{close: func() error { closed = true; return sentinel }}}
	if engine.publishSession("late", session) {
		t.Fatal("published a session after close")
	}
	if !closed || !errors.Is(engine.CloseError(), sentinel) {
		t.Fatalf("late resource was not closed with inspectable error: %v", engine.CloseError())
	}
	if got := engine.beginGatewaySession("late", "stdio"); got != nil {
		t.Fatal("published inspection state after close")
	}
}

func TestMCPOnlyOperationsRejectMissingTarget(t *testing.T) {
	engine := NewEngine(nil)
	defer func() { <-engine.BeginClose() }()
	_, err := engine.ListResourcesPage(t.Context(), nil, "")
	var callErr *cr.CallError
	if !errors.As(err, &callErr) || callErr.Category != "unsupported_capability" {
		t.Fatalf("expected unsupported capability: %v", err)
	}
}

type startupTransport struct {
	connect func(context.Context) error
	call    func(*transport.Message) (*transport.Message, error)
	closes  int
}

func (c *startupTransport) Connect(ctx context.Context) error { return c.connect(ctx) }
func (c *startupTransport) Call(_ context.Context, msg *transport.Message) (*transport.Message, error) {
	return c.call(msg)
}
func (c *startupTransport) Close() error { c.closes++; return nil }

func TestSessionStartupHasOneCleanupOwner(t *testing.T) {
	for _, mode := range []string{"connect_failure", "initialize_failure", "caller_cancel", "publication_rejected", "success"} {
		t.Run(mode, func(t *testing.T) {
			engine := NewEngine(nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var lifetime context.Context
			candidate := &startupTransport{}
			candidate.connect = func(ctx context.Context) error {
				lifetime = ctx
				if mode == "connect_failure" {
					return errors.New("connect failed")
				}
				return nil
			}
			candidate.call = func(msg *transport.Message) (*transport.Message, error) {
				if msg.Method == "initialize" {
					if mode == "initialize_failure" {
						return nil, errors.New("initialize failed")
					}
					return &transport.Message{Result: map[string]any{"protocolVersion": ProtocolVersion}}, nil
				}
				if mode == "caller_cancel" {
					cancel()
				}
				if mode == "publication_rejected" {
					engine.stateMu.Lock()
					engine.closed = true
					engine.stateMu.Unlock()
				}
				return nil, nil
			}
			_, err := engine.initializeSession(ctx, "tools", "stdio", "signature", candidate)
			if mode == "success" {
				if err != nil || candidate.closes != 0 {
					t.Fatalf("successful session closed: %v, closes=%d", err, candidate.closes)
				}
				cancel()
				if lifetime.Err() != nil {
					t.Fatal("caller cancellation closed an accepted session")
				}
			} else if err == nil || candidate.closes != 1 {
				t.Fatalf("failed startup cleanup: err=%v closes=%d", err, candidate.closes)
			}
			<-engine.BeginClose()
			if candidate.closes != 1 {
				t.Fatalf("duplicate cleanup: %d", candidate.closes)
			}
			if lifetime.Err() == nil {
				t.Fatal("session lifetime not cancelled")
			}
		})
	}
}
