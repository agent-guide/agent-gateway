package session

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"

	acpclient "github.com/agent-guide/agent-gateway/pkg/acp/client"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
	"go.uber.org/zap"
)

type instance struct {
	cfg   Config
	model string
	agent acpclient.Agent
	t     acptransport.Transport

	// sessionID is the ACP wire ID. The pool tracks ownership separately.
	sessionID string
	closed    atomic.Bool

	meta      sessionMetaCache
	metaUnsub func() error

	// permissions is the manager-level broker for interactive permission
	// requests; turnEmit is the active turn's event sink (set for the duration
	// of a prompt so the permission handler can reach the turn client).
	permissions *permissionBroker
	emitMu      sync.Mutex
	turnEmit    EventSink
	turnContext context.Context
}

func (i *instance) setTurnSink(emit EventSink) {
	i.emitMu.Lock()
	i.turnEmit = emit
	i.emitMu.Unlock()
}

func (i *instance) turnSink() EventSink {
	i.emitMu.Lock()
	defer i.emitMu.Unlock()
	return i.turnEmit
}

func newInstance(ctx context.Context, cfg Config, req TurnRequest, permissions *permissionBroker, observe func(SessionCapabilities), observeNotify func(string, json.RawMessage)) (*instance, error) {
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = cfg.CWD
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = cfg.DefaultModel
	}
	agent, err := cfg.newAgent(cwd)
	if err != nil {
		return nil, err
	}
	inst := &instance{cfg: cfg, model: model, sessionID: strings.TrimSpace(req.SessionID), agent: agent, permissions: permissions}
	opened := diagnosticPhase(ctx, "process open")
	t, err := agent.Open(ctx, acptransport.Handlers{Permission: inst.permissionFunc()})
	opened(err)
	if err != nil {
		return nil, err
	}
	inst.t = t
	if observeNotify != nil {
		inst.t = observedTransport{Transport: t, notify: observeNotify}
	}
	// Subscribe for the whole instance lifetime so state pushed outside a turn
	// is not lost — the real opencode binary pushes available_commands_update
	// right after session/new, before any prompt.
	metaCh, _, metaUnsub := t.Updates(64)
	inst.metaUnsub = metaUnsub
	go inst.consumeMetadata(metaCh)
	// Bound the setup handshake so a wedged agent does not hang the turn until
	// the client disconnects. Streaming (prompt) is intentionally not bounded.
	setupCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	initialized := diagnosticPhase(ctx, "initialize session")
	if err := inst.initialize(setupCtx, req.FreshSession, observe); err != nil {
		initialized(err)
		_ = inst.close()
		return nil, err
	}
	initialized(nil)
	diagnostic(ctx, "session ready", zap.String("session_id", inst.sessionID))
	return inst, nil
}

// observedTransport is a narrow wire tap used by native-agent acceptance
// tests. The observer runs only after the JSON-RPC notification was written to
// the real transport, so tests can assert the session/cancel frame itself.
type observedTransport struct {
	acptransport.Transport
	notify func(string, json.RawMessage)
}

func (t observedTransport) Notify(method string, params any) error {
	if err := t.Transport.Notify(method, params); err != nil {
		return err
	}
	raw, _ := json.Marshal(params)
	t.notify(method, raw)
	return nil
}

func (i *instance) cancel() error {
	if i == nil || i.t == nil || i.sessionID == "" {
		return ErrRunNotReady
	}
	return i.agent.Cancel(context.Background(), i.t, i.sessionID)
}

func (i *instance) close() error {
	if i == nil || i.t == nil || !i.closed.CompareAndSwap(false, true) {
		return nil
	}
	// End the metadata subscription first so its consumer goroutine exits.
	if i.metaUnsub != nil {
		_ = i.metaUnsub()
	}
	return i.t.Close()
}

func (i *instance) alive() bool {
	return i != nil && i.t != nil && !i.closed.Load() && i.t.Alive()
}

// lockedEventSink serializes writes to a turn's event sink: during an
// interactive permission wait the permission handler emits from the transport
// goroutine concurrently with the prompt loop.
func lockedEventSink(emit EventSink) EventSink {
	if emit == nil {
		return nil
	}
	var mu sync.Mutex
	return func(ev TurnEvent) error {
		mu.Lock()
		defer mu.Unlock()
		return emit(ev)
	}
}
