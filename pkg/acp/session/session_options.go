package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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
		if err := i.setConfigOption(ctx, id, value); err != nil {
			return err
		}
	}
	return nil
}

func (i *instance) setConfigOption(ctx context.Context, id, value string) error {
	result, err := i.request(ctx, "session/set_config_option", map[string]any{
		"sessionId": i.sessionID,
		"configId":  id,
		"value":     value,
	})
	if err != nil {
		return fmt.Errorf("set_config_option %q: %w", id, err)
	}
	i.cacheSessionConfigOptions(result)
	return nil
}

func (i *instance) applySessionModel(ctx context.Context, result json.RawMessage) error {
	if i.model == "" {
		return nil
	}
	var setup struct {
		Options []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
			Type     string `json:"type"`
		} `json:"configOptions"`
	}
	if err := json.Unmarshal(result, &setup); err != nil {
		return fmt.Errorf("decode ACP session config options: %w", err)
	}
	for _, option := range setup.Options {
		if option.Category == "model" && option.Type == "select" && option.ID != "" {
			return i.setConfigOption(ctx, option.ID, i.model)
		}
	}
	return fmt.Errorf("%w: ACP agent %q did not advertise a model selection option", ErrInvalidRequest, i.agent.Name())
}
