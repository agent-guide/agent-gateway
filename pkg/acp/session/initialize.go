package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	acpclient "github.com/agent-guide/agent-gateway/pkg/acp/client"
)

// initializeTimeout bounds the setup handshake (initialize + session/new|load +
// model selection). It is a var so tests can shorten it.
var initializeTimeout = 30 * time.Second

// SessionCapabilities holds the optional features reported at ACP initialize.
type SessionCapabilities struct {
	List bool
	Load bool
}

func initializeAgent(
	ctx context.Context,
	request func(context.Context, string, any) (json.RawMessage, error),
	params map[string]any,
	observe func(SessionCapabilities),
) (SessionCapabilities, error) {
	raw, err := request(ctx, "initialize", params)
	if err != nil {
		return SessionCapabilities{}, fmt.Errorf("initialize: %w", err)
	}
	var result struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
			Session     struct {
				List *struct{} `json:"list"`
			} `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return SessionCapabilities{}, fmt.Errorf("decode ACP initialize result: %w", err)
	}
	if result.ProtocolVersion != acpclient.ProtocolVersion {
		return SessionCapabilities{}, fmt.Errorf("ACP protocol version %d is unsupported; expected %d", result.ProtocolVersion, acpclient.ProtocolVersion)
	}
	caps := SessionCapabilities{List: result.AgentCapabilities.Session.List != nil, Load: result.AgentCapabilities.LoadSession}
	if observe != nil {
		observe(caps)
	}
	return caps, nil
}
