package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
	"github.com/agent-guide/agent-gateway/pkg/acp/agentspi"
	"github.com/agent-guide/agent-gateway/pkg/acp/runtime/acpupdate"
	acpservice "github.com/agent-guide/agent-gateway/pkg/acp/service"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

type instance struct {
	cfg       acpservice.ServiceConfig
	cwd       string
	model     string
	sessionID string
	agent     agentspi.Agent
	t         acptransport.Transport
}

func newInstance(ctx context.Context, cfg acpservice.ServiceConfig, req TurnRequest) (*instance, error) {
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = cfg.CWD
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = cfg.DefaultModel
	}
	agent, err := agentspi.New(cfg.AgentType, agentspi.OpenRequest{Service: cfg, CWD: cwd})
	if err != nil {
		return nil, err
	}
	t, err := agent.Open(ctx, acptransport.Handlers{Permission: permissionHandler(cfg.PermissionMode)})
	if err != nil {
		return nil, err
	}
	inst := &instance{cfg: cfg, cwd: cwd, model: model, sessionID: strings.TrimSpace(req.SessionID), agent: agent, t: t}
	// Bound the setup handshake so a wedged agent does not hang the turn until
	// the client disconnects. Streaming (prompt) is intentionally not bounded.
	setupCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	if err := inst.initialize(setupCtx, req.FreshSession); err != nil {
		_ = t.Close()
		return nil, err
	}
	return inst, nil
}

func (i *instance) initialize(ctx context.Context, fresh bool) error {
	if _, err := i.t.Request(ctx, "initialize", i.agent.InitializeParams()); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if i.sessionID != "" && !fresh {
		if _, err := i.t.Request(ctx, "session/load", i.agent.SessionLoadParams(i.sessionID)); err != nil {
			return fmt.Errorf("session/load: %w", err)
		}
		return nil
	}
	result, err := i.t.Request(ctx, "session/new", i.agent.SessionNewParams(i.model))
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	i.sessionID = parseSessionID(result)
	if i.sessionID == "" {
		return fmt.Errorf("session/new returned empty sessionId")
	}
	if err := i.applySessionModel(ctx); err != nil {
		return err
	}
	return i.applyConfigOverrides(ctx, i.cfg.ConfigOverrides)
}

// applyConfigOverrides replays config options via session/set_config_option
// (`{sessionId, configId, value}`, verified against the ACP v1 schema and the
// real opencode binary). A rejected option fails the setup/turn rather than
// being silently dropped.
func (i *instance) applyConfigOverrides(ctx context.Context, overrides map[string]string) error {
	for id, value := range overrides {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, err := i.t.Request(ctx, "session/set_config_option", map[string]any{
			"sessionId": i.sessionID,
			"configId":  id,
			"value":     value,
		}); err != nil {
			return fmt.Errorf("set_config_option %q: %w", id, err)
		}
	}
	return nil
}

// applySessionModel applies the configured model to a freshly created session.
// ACP defines no standard model-selection method, so the model is applied only
// when the agent declares how through SessionModelSelector; otherwise the
// configured model is left to the agent/adapter default rather than being
// smuggled into session/new as a non-standard field.
func (i *instance) applySessionModel(ctx context.Context) error {
	if i.model == "" {
		return nil
	}
	selector, ok := i.agent.(agentspi.SessionModelSelector)
	if !ok {
		return nil
	}
	if _, err := selector.SelectSessionModel(ctx, i.t, i.sessionID, i.model, nil); err != nil {
		return fmt.Errorf("select session model: %w", err)
	}
	return nil
}

// terminalDrainTimeout bounds how long the driver waits for a terminal update
// after the prompt result, for agents that signal completion out of band.
const terminalDrainTimeout = 2 * time.Second

// initializeTimeout bounds the setup handshake (initialize + session/new|load +
// model selection). It is a var so tests can shorten it.
var initializeTimeout = 30 * time.Second

func (i *instance) prompt(ctx context.Context, req TurnRequest, emit EventSink) (string, error) {
	if emit != nil && i.sessionID != "" {
		if err := emit(TurnEvent{Event: "session", SessionID: i.sessionID}); err != nil {
			return "", err
		}
	}
	// Per-turn config overrides apply to the (shared) session before the prompt.
	if err := i.applyConfigOverrides(ctx, req.ConfigOverrides); err != nil {
		return "", err
	}
	updates, unsubscribe := i.t.Updates(256)
	defer unsubscribe()

	done := make(chan promptResult, 1)
	go func() {
		raw, err := i.t.Request(ctx, "session/prompt", i.agent.PromptParams(i.sessionID, req.Input, firstNonEmpty(strings.TrimSpace(req.Model), i.model)))
		done <- promptResult{stopReason: parseStopReason(raw), err: err}
	}()

	emitUpdate := func(msg acptransport.Message) error {
		if emit == nil || msg.Method != "session/update" {
			return nil
		}
		for _, ev := range acpupdate.Parse(msg.Params) {
			if ev.Kind == acpupdate.KindReasoning && !i.acceptReasoning(msg.Params) {
				continue
			}
			if err := emit(TurnEvent{Event: string(ev.Kind), Text: ev.Text, Data: ev.Data}); err != nil {
				return err
			}
		}
		return nil
	}

	terminalDetector, hasTerminal := i.agent.(agentspi.TerminalUpdateDetector)
	stopReason := "end_turn"
	resultReceived := false
	var drainTimer <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			i.cancel()
			return "cancelled", nil
		case res := <-done:
			if res.err != nil {
				return "end_turn", fmt.Errorf("session/prompt: %w", res.err)
			}
			if res.stopReason != "" {
				stopReason = res.stopReason
			}
			resultReceived = true
			// Per ACP, session/update notifications for a turn precede its
			// session/prompt result, so anything buffered now belongs to this
			// turn. Drain it before returning so the tail of the response is
			// never dropped by the select racing the result against updates.
			if !hasTerminal {
				if err := drainBuffered(updates, emitUpdate); err != nil {
					i.cancel()
					return "cancelled", err
				}
				return stopReason, nil
			}
			drainTimer = time.After(terminalDrainTimeout)
		case msg, ok := <-updates:
			if !ok {
				return stopReason, nil
			}
			if err := emitUpdate(msg); err != nil {
				i.cancel()
				return "cancelled", err
			}
			if resultReceived && hasTerminal && terminalDetector.IsTerminalUpdate(msg.Params) {
				return stopReason, nil
			}
		case <-drainTimer:
			return stopReason, nil
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

func (i *instance) cancel() {
	if i == nil || i.t == nil || i.sessionID == "" {
		return
	}
	i.agent.Cancel(context.Background(), i.t, i.sessionID)
}

func (i *instance) close() error {
	if i == nil || i.t == nil {
		return nil
	}
	return i.t.Close()
}

func (i *instance) alive() bool {
	return i != nil && i.t != nil && i.t.Alive()
}

func parseSessionID(raw json.RawMessage) string {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if id, ok := payload["sessionId"].(string); ok {
		return strings.TrimSpace(id)
	}
	if nested, ok := payload["session"].(map[string]any); ok {
		if id, ok := nested["sessionId"].(string); ok {
			return strings.TrimSpace(id)
		}
		if id, ok := nested["id"].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// acceptReasoning lets an agent suppress agent_thought_chunk updates that are
// not genuine reasoning (ReasoningUpdateFilter). Without the capability, every
// reasoning update is forwarded.
func (i *instance) acceptReasoning(params json.RawMessage) bool {
	if filter, ok := i.agent.(agentspi.ReasoningUpdateFilter); ok {
		return filter.AcceptReasoningUpdate(params)
	}
	return true
}

func permissionHandler(mode string) func(context.Context, json.RawMessage) acptransport.PermissionResponse {
	return func(_ context.Context, params json.RawMessage) acptransport.PermissionResponse {
		// Fail closed unless the service explicitly opts into auto-approval.
		if mode != baseacp.PermissionModeAutoApprove {
			return acptransport.PermissionResponse{Outcome: acptransport.PermissionOutcomeCancelled}
		}
		if id := acptransport.AllowOptionID(params); id != "" {
			return acptransport.PermissionResponse{Outcome: acptransport.PermissionOutcomeSelected, SelectedOptionID: id}
		}
		return acptransport.PermissionResponse{Outcome: acptransport.PermissionOutcomeCancelled}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
