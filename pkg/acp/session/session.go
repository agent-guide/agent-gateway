package session

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	acptransport "github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

func (i *instance) initialize(ctx context.Context, fresh bool, observe func(SessionCapabilities)) error {
	caps, err := initializeAgent(ctx, i.request, i.agent.InitializeParams(), observe)
	if err != nil {
		return err
	}
	if i.sessionID != "" && !fresh {
		if !caps.Load {
			return fmt.Errorf("%w: acp agent %q does not advertise session/load", ErrCapabilityNotSupported, i.agent.Name())
		}
		result, err := i.request(ctx, "session/load", i.agent.SessionLoadParams(i.sessionID))
		if err != nil {
			return fmt.Errorf("session/load: %w", err)
		}
		i.cacheSessionConfigOptions(result)
		return nil
	}
	result, err := i.request(ctx, "session/new", i.agent.SessionNewParams())
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	var session struct {
		ID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &session); err != nil {
		return fmt.Errorf("decode ACP session/new result: %w", err)
	}
	session.ID = strings.TrimSpace(session.ID)
	if session.ID == "" {
		return fmt.Errorf("session/new returned empty sessionId")
	}
	i.sessionID = session.ID
	i.cacheSessionConfigOptions(result)
	if err := i.applySessionModel(ctx, result); err != nil {
		return err
	}
	return i.applyConfigOverrides(ctx, i.cfg.ConfigOverrides)
}

func listAgentSessions(ctx context.Context, cfg Config, req ListSessionsRequest, observe func(SessionCapabilities)) (ListSessionsResponse, error) {
	openCWD := strings.TrimSpace(req.CWD)
	if openCWD == "" {
		openCWD = cfg.CWD
	}
	agent, err := cfg.newAgent(openCWD)
	if err != nil {
		return ListSessionsResponse{}, err
	}
	t, err := agent.Open(ctx, acptransport.Handlers{})
	if err != nil {
		return ListSessionsResponse{}, err
	}
	defer func() { _ = t.Close() }()

	setupCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	caps, err := initializeAgent(setupCtx, t.Request, agent.InitializeParams(), observe)
	if err != nil {
		return ListSessionsResponse{}, err
	}
	if !caps.List {
		return ListSessionsResponse{}, fmt.Errorf("%w: acp agent %q does not advertise session/list", ErrCapabilityNotSupported, agent.Name())
	}
	// The cwd filter is applied gateway-side instead of being forwarded to the
	// agent: stored-cwd shapes differ per agent (the real opencode binary
	// stores canonical symlink-resolved cwds, the real codex-acp adapter stores
	// the session/new cwd verbatim), so an agent-side exact match silently
	// drops sessions for one agent or the other. Comparing both sides through
	// canonicalCWD is agent-agnostic.
	raw, err := t.Request(ctx, "session/list", agent.SessionListParams(req.Cursor))
	if err != nil {
		return ListSessionsResponse{}, fmt.Errorf("session/list: %w", err)
	}
	out, err := parseListSessionsResponse(raw)
	if err != nil {
		return ListSessionsResponse{}, err
	}
	if filter := canonicalCWD(req.CWD); filter != "" {
		kept := make([]SessionInfo, 0, len(out.Sessions))
		for _, session := range out.Sessions {
			if canonicalCWD(session.CWD) == filter {
				kept = append(kept, session)
			}
		}
		out.Sessions = kept
	}
	if out.Sessions == nil {
		out.Sessions = []SessionInfo{}
	}
	return out, nil
}

// canonicalCWD resolves symlinks in a cwd so both sides of the gateway-side
// session/list filter compare in canonical form (macOS /tmp -> /private/tmp).
// Unresolvable paths are passed through unchanged.
func canonicalCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		return resolved
	}
	return cwd
}

func parseListSessionsResponse(raw json.RawMessage) (ListSessionsResponse, error) {
	var payload struct {
		Sessions []struct {
			SessionID string          `json:"sessionId"`
			CWD       string          `json:"cwd"`
			Title     string          `json:"title"`
			UpdatedAt string          `json:"updatedAt"`
			Meta      json.RawMessage `json:"_meta"`
		} `json:"sessions"`
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ListSessionsResponse{}, fmt.Errorf("decode session/list response: %w", err)
	}
	out := ListSessionsResponse{NextCursor: strings.TrimSpace(payload.NextCursor)}
	out.Sessions = make([]SessionInfo, 0, len(payload.Sessions))
	for _, item := range payload.Sessions {
		sessionID := strings.TrimSpace(item.SessionID)
		cwd := strings.TrimSpace(item.CWD)
		if sessionID == "" || cwd == "" {
			return ListSessionsResponse{}, fmt.Errorf("session/list returned a session without sessionId or cwd")
		}
		info := SessionInfo{
			SessionID: sessionID,
			CWD:       cwd,
			Title:     item.Title,
			Meta:      item.Meta,
		}
		if ts := strings.TrimSpace(item.UpdatedAt); ts != "" {
			updatedAt, err := time.Parse(time.RFC3339, ts)
			if err != nil {
				return ListSessionsResponse{}, fmt.Errorf("session/list returned invalid updatedAt for %q: %w", sessionID, err)
			}
			info.UpdatedAt = &updatedAt
		}
		out.Sessions = append(out.Sessions, info)
	}
	return out, nil
}
