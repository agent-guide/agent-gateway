package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
	acpservice "github.com/agent-guide/agent-gateway/pkg/acp/service"
	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

type instance struct {
	cfg       acpservice.ServiceConfig
	cwd       string
	model     string
	sessionID string
	t         *acptransport.Transport
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
	command, args, err := processArgs(cfg.AgentType, cwd)
	if err != nil {
		return nil, err
	}
	mode := acptransport.PermissionModeDeny
	if cfg.PermissionMode == baseacp.PermissionModeAutoApprove {
		mode = acptransport.PermissionModeAutoApprove
	}
	t, err := acptransport.Open(ctx, acptransport.ProcessConfig{
		Command:        command,
		Args:           args,
		Dir:            cwd,
		PermissionMode: mode,
	})
	if err != nil {
		return nil, err
	}
	inst := &instance{cfg: cfg, cwd: cwd, model: model, sessionID: strings.TrimSpace(req.SessionID), t: t}
	if err := inst.initialize(ctx, req.FreshSession); err != nil {
		_ = t.Close()
		return nil, err
	}
	return inst, nil
}

func (i *instance) initialize(ctx context.Context, fresh bool) error {
	if _, err := i.t.Request(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
		},
	}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if i.sessionID != "" && !fresh {
		if _, err := i.t.Request(ctx, "session/load", map[string]any{"sessionId": i.sessionID, "cwd": i.cwd}); err != nil {
			return fmt.Errorf("session/load: %w", err)
		}
		return nil
	}
	result, err := i.t.Request(ctx, "session/new", sessionNewParams(i.cwd, i.model))
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	i.sessionID = parseSessionID(result)
	if i.sessionID == "" {
		return fmt.Errorf("session/new returned empty sessionId")
	}
	return nil
}

func (i *instance) prompt(ctx context.Context, req TurnRequest, emit EventSink) (string, error) {
	if emit != nil && i.sessionID != "" {
		if err := emit(TurnEvent{Event: "session", SessionID: i.sessionID}); err != nil {
			return "", err
		}
	}
	updates, unsubscribe := i.t.Updates(256)
	defer unsubscribe()

	done := make(chan error, 1)
	go func() {
		_, err := i.t.Request(ctx, "session/prompt", promptParams(i.cfg.AgentType, i.sessionID, req.Input, firstNonEmpty(strings.TrimSpace(req.Model), i.model)))
		done <- err
	}()

	for {
		select {
		case <-ctx.Done():
			i.cancel()
			return "cancelled", nil
		case err := <-done:
			if err != nil {
				return "end_turn", fmt.Errorf("session/prompt: %w", err)
			}
			return "end_turn", nil
		case msg, ok := <-updates:
			if !ok {
				return "end_turn", nil
			}
			if msg.Method != "session/update" {
				continue
			}
			delta := parseDelta(msg.Params)
			if delta == "" {
				continue
			}
			if emit != nil {
				if err := emit(TurnEvent{Event: "delta", Text: delta}); err != nil {
					i.cancel()
					return "cancelled", err
				}
			}
		}
	}
}

func (i *instance) cancel() {
	if i == nil || i.t == nil || i.sessionID == "" {
		return
	}
	_ = i.t.Notify("session/cancel", map[string]any{"sessionId": i.sessionID})
}

func (i *instance) close() error {
	if i == nil || i.t == nil {
		return nil
	}
	return i.t.Close()
}

func processArgs(agentType, cwd string) (string, []string, error) {
	switch agentType {
	case baseacp.AgentTypeOpencode:
		return "opencode", []string{"acp", "--cwd", cwd}, nil
	case baseacp.AgentTypeCodex:
		return "codex", []string{"acp", "--cwd", cwd}, nil
	default:
		return "", nil, fmt.Errorf("unsupported acp agent_type %q", agentType)
	}
}

func sessionNewParams(cwd, model string) map[string]any {
	params := map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	}
	if model != "" {
		params["model"] = model
		params["modelId"] = model
	}
	return params
}

func promptParams(agentType, sessionID, input, model string) map[string]any {
	params := map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]any{{
			"type": "text",
			"text": input,
		}},
	}
	if model != "" {
		if agentType == baseacp.AgentTypeOpencode {
			params["modelId"] = model
		} else {
			params["model"] = model
		}
	}
	return params
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

func parseDelta(raw json.RawMessage) string {
	var payload struct {
		Delta  string `json:"delta"`
		Update struct {
			SessionUpdate string          `json:"sessionUpdate"`
			Content       json.RawMessage `json:"content"`
		} `json:"update"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if payload.Delta != "" {
		return payload.Delta
	}
	switch strings.TrimSpace(payload.Update.SessionUpdate) {
	case "agent_message_chunk", "thought_message_chunk":
	default:
		return ""
	}
	var text string
	if err := json.Unmarshal(payload.Update.Content, &text); err == nil {
		return text
	}
	var content struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload.Update.Content, &content); err == nil {
		return content.Text
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
