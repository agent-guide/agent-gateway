package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/acp/session/acpupdate"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
	"go.uber.org/zap"
)

// terminalDrainTimeout caps the quiet-period drain after the prompt result.
const terminalDrainTimeout = 2 * time.Second

// postResultIdleGrace is how long the post-result drain waits for further
// updates before concluding the turn. A real agent can race the session/prompt response against
// the final agent_message_chunk updates (observed live: the reply chunks can
// arrive after the result), so returning on the result with only a buffered
// drain drops the reply tail. It is a var so tests can shorten it.
var postResultIdleGrace = 250 * time.Millisecond

func (i *instance) prompt(ctx context.Context, req TurnRequest, emit EventSink) (stop string, promptErr error) {
	counts := map[string]int{}
	lastUpdate := time.Now()
	defer func() {
		err := promptErr
		if stop == "cancelled" {
			err = errors.Join(err, cancelledDiagnosticError(ctx))
		}
		diagnostic(ctx, "prompt finished", zap.String("session_id", i.sessionID), zap.Any("event_counts", counts), zap.Bool("success", err == nil), zap.Bool("cancelled", errors.Is(err, context.Canceled)), zap.Bool("deadline_exceeded", errors.Is(err, context.DeadlineExceeded)))
	}()
	i.emitMu.Lock()
	i.turnContext = ctx
	i.emitMu.Unlock()
	defer func() { i.emitMu.Lock(); i.turnContext = nil; i.emitMu.Unlock() }()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	// Serialize sink writes (the interactive permission handler emits from the
	// transport goroutine) and register the sink for the duration of the turn
	// so permission requests can reach this turn's client.
	if emit != nil {
		next := emit
		firstWrites := map[string]bool{}
		emit = func(ev TurnEvent) error {
			first := !firstWrites[ev.Event]
			firstWrites[ev.Event] = true
			if first {
				diagnostic(ctx, "client event started", zap.String("event_kind", ev.Event))
			}
			start := time.Now()
			err := next(ev)
			if first || time.Since(start) >= time.Second || err != nil {
				diagnostic(ctx, "client write", zap.String("event_kind", ev.Event), zap.Int64("duration_ms", time.Since(start).Milliseconds()), zap.Bool("success", err == nil))
			}
			return err
		}
	}
	emit = lockedEventSink(emit)
	i.setTurnSink(emit)
	defer i.setTurnSink(nil)
	if emit != nil && i.sessionID != "" {
		if err := emit(TurnEvent{Event: "session", SessionID: i.sessionID}); err != nil {
			return "", err
		}
	}
	// Per-turn config overrides apply to the (shared) session before the prompt.
	overrides := diagnosticPhase(ctx, "turn config overrides")
	if err := i.applyConfigOverrides(ctx, req.ConfigOverrides); err != nil {
		overrides(err)
		return "", err
	}
	overrides(nil)
	// Replay the cached session state (config options, slash commands, title,
	// mode, usage) so a client joining a pooled instance sees the current state
	// even though the originating updates were delivered during an earlier turn
	// or during setup.
	if emit != nil {
		for _, ev := range i.meta.turnStartEvents() {
			if err := emit(ev); err != nil {
				return "", err
			}
		}
	}
	updates, overflow, unsubscribe := i.t.Updates(256)
	defer func() {
		// The request, cancellation, or sink can fail before the loop reads
		// the overflow signal. Check the final subscription state on every exit.
		if err := unsubscribe(); err != nil && !errors.Is(promptErr, err) {
			promptErr = errors.Join(err, promptErr)
		}
		if errors.Is(promptErr, acptransport.ErrUpdateOverflow) {
			stop = ""
			_ = i.close()
		}
	}()
	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()

	done := make(chan promptResult, 1)
	go func() {
		raw, err := i.request(requestCtx, "session/prompt", i.agent.PromptParams(i.sessionID, req.Input))
		done <- promptResult{stopReason: parseStopReason(raw), err: err}
	}()

	emitUpdate := func(msg acptransport.Message) error {
		if msg.Method != "session/update" {
			return nil
		}
		lastUpdate = time.Now()
		for _, ev := range acpupdate.Parse(msg.Params) {
			kind := string(ev.Kind)
			counts[kind]++
			if counts[kind] == 1 {
				diagnostic(ctx, "first update", zap.String("event_kind", kind), zap.String("session_id", i.sessionID))
			}
			if ev.Kind == acpupdate.KindToolCall {
				var tool struct {
					Status string `json:"status"`
				}
				_ = json.Unmarshal(ev.Data, &tool)
				switch tool.Status {
				case "pending", "in_progress", "completed", "failed":
				default:
					tool.Status = "unknown"
				}
				diagnostic(ctx, "tool update", zap.String("tool_status", tool.Status), zap.Int("tool_update_count", counts[kind]))
			}
			if emit == nil {
				continue
			}
			if err := emit(TurnEvent{Event: string(ev.Kind), Text: ev.Text, Data: ev.Data}); err != nil {
				return err
			}
		}
		return nil
	}

	stopReason := "end_turn"
	resultReceived := false
	var drainTimer <-chan time.Time
	var idleTimer <-chan time.Time

	finish := func() (string, error) {
		if err := unsubscribe(); err != nil {
			return "", err
		}
		if err := drainBuffered(updates, emitUpdate); err != nil {
			return "", err
		}
		return stopReason, nil
	}

	for {
		select {
		case <-ticker.C:
			diagnostic(ctx, "prompt waiting", zap.Bool("result_received", resultReceived), zap.Int64("since_last_update_ms", time.Since(lastUpdate).Milliseconds()), zap.Any("event_counts", counts), zap.String("session_id", i.sessionID))
		case <-ctx.Done():
			diagnostic(ctx, "prompt cancelled")
			// Exact-run cancellation writes session/cancel before cancelling
			// the context. Do not emit a second native frame when that marked
			// cancellation reaches the prompt loop. Ordinary parent timeout or
			// disconnect still needs to propagate cancellation to the agent.
			if !errors.Is(context.Cause(ctx), errNativeCancelSent) {
				i.cancel()
			}
			return "cancelled", nil
		case <-overflow:
			return "", acptransport.ErrUpdateOverflow
		case res := <-done:
			if res.err != nil {
				return "end_turn", fmt.Errorf("session/prompt: %w", res.err)
			}
			if res.stopReason != "" {
				stopReason = res.stopReason
			}
			resultReceived = true
			diagnostic(ctx, "result drain started")
			// Some agents send final chunks shortly after the prompt response.
			drainTimer = time.After(terminalDrainTimeout)
			idleTimer = time.After(postResultIdleGrace)
		case msg, ok := <-updates:
			if !ok {
				if !resultReceived {
					return "", fmt.Errorf("acp updates closed before session/prompt completed")
				}
				return finish()
			}
			if err := emitUpdate(msg); err != nil {
				i.cancel()
				return "cancelled", err
			}
			if resultReceived {
				idleTimer = time.After(postResultIdleGrace)
			}
		case <-idleTimer:
			return finish()
		case <-drainTimer:
			return finish()
		}
	}
}

type promptResult struct {
	stopReason string
	err        error
}

// drainBuffered emits every update already queued on the channel without
// blocking, stopping as soon as the channel is empty or closed.
func drainBuffered(updates <-chan acptransport.Message, emit func(acptransport.Message) error) error {
	for {
		select {
		case msg, ok := <-updates:
			if !ok {
				return nil
			}
			if err := emit(msg); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func parseStopReason(raw json.RawMessage) string {
	var payload struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.StopReason)
}
