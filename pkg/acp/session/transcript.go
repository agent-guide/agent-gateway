package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/acp/session/acpupdate"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

// loadAgentTranscript replays one session through a transient connection and
// returns the coalesced message transcript. Per ACP, session/load replays the
// session history as session/update notifications that all precede the
// session/load response, so the collector consumes updates concurrently with
// the request and drains anything still buffered once the response arrives.
func loadAgentTranscript(ctx context.Context, cfg Config, req TranscriptRequest, observe func(SessionCapabilities)) (TranscriptResponse, error) {
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		return TranscriptResponse{}, fmt.Errorf("%w: session_id is required", ErrInvalidRequest)
	}
	openCWD := strings.TrimSpace(req.CWD)
	if openCWD == "" {
		openCWD = cfg.CWD
	}
	agent, err := cfg.newAgent(openCWD)
	if err != nil {
		return TranscriptResponse{}, err
	}
	t, err := agent.Open(ctx, acptransport.Handlers{})
	if err != nil {
		return TranscriptResponse{}, err
	}
	defer func() { _ = t.Close() }()

	setupCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	caps, err := initializeAgent(setupCtx, t.Request, agent.InitializeParams(), observe)
	if err != nil {
		return TranscriptResponse{}, err
	}
	if !caps.Load {
		return TranscriptResponse{}, fmt.Errorf("%w: acp agent %q does not advertise session/load", ErrCapabilityNotSupported, agent.Name())
	}

	// Subscribe before sending session/load so no replayed update is missed.
	updates, overflow, unsubscribe := t.Updates(256)
	defer unsubscribe()

	done := make(chan error, 1)
	go func() {
		_, err := t.Request(ctx, "session/load", agent.SessionLoadParams(sessionID))
		done <- err
	}()

	var collector transcriptCollector
	collect := func(msg acptransport.Message) error {
		if msg.Method != "session/update" {
			return nil
		}
		if role, text, ok := acpupdate.ParseReplayChunk(msg.Params); ok {
			collector.add(role, text)
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return TranscriptResponse{}, ctx.Err()
		case <-overflow:
			return TranscriptResponse{}, acptransport.ErrUpdateOverflow
		case err := <-done:
			if err != nil {
				return TranscriptResponse{}, fmt.Errorf("session/load: %w", err)
			}
			if err := unsubscribe(); err != nil {
				return TranscriptResponse{}, err
			}
			if err := drainBuffered(updates, collect); err != nil {
				return TranscriptResponse{}, err
			}
			return TranscriptResponse{SessionID: sessionID, Messages: collector.finish()}, nil
		case msg, ok := <-updates:
			if !ok {
				return TranscriptResponse{}, fmt.Errorf("acp transport closed during session/load")
			}
			_ = collect(msg)
		}
	}
}

// transcriptCollector coalesces consecutive replayed chunks of the same role
// into one message, preserving order.
type transcriptCollector struct {
	messages []TranscriptMessage
}

func (c *transcriptCollector) add(role, text string) {
	if text == "" {
		return
	}
	if n := len(c.messages); n > 0 && c.messages[n-1].Role == role {
		c.messages[n-1].Text += text
		return
	}
	c.messages = append(c.messages, TranscriptMessage{Role: role, Text: text})
}

func (c *transcriptCollector) finish() []TranscriptMessage {
	if c.messages == nil {
		return []TranscriptMessage{}
	}
	return c.messages
}
