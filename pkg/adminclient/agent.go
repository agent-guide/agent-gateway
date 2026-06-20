package adminclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	adminapi "github.com/agent-guide/agent-gateway/pkg/admin"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
)

type AgentConfig = agentpkg.Agent
type AgentView = adminapi.AgentView

func (c *Client) ListAgents(ctx context.Context) ([]AgentView, error) {
	var resp itemsResponse[AgentView]
	if err := c.do(ctx, http.MethodGet, "/admin/agents", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) CreateAgent(ctx context.Context, cfg AgentConfig) (*AgentView, error) {
	var resp AgentView
	if err := c.do(ctx, http.MethodPost, "/admin/agents", cfg, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetAgent(ctx context.Context, id string) (*AgentView, error) {
	var resp AgentView
	if err := c.do(ctx, http.MethodGet, "/admin/agents/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateAgent(ctx context.Context, id string, cfg AgentConfig) (*AgentView, error) {
	var resp AgentView
	if err := c.do(ctx, http.MethodPut, "/admin/agents/"+url.PathEscape(id), cfg, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteAgent(ctx context.Context, id string) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.do(ctx, http.MethodDelete, "/admin/agents/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetAgentWorkspace returns the raw workspace summary/index for an agent. The
// response is the management read model assembled by the gateway; it is decoded
// as a generic object because it composes references across subsystems.
func (c *Client) GetAgentWorkspace(ctx context.Context, id string) (json.RawMessage, error) {
	return c.getAgentRaw(ctx, id, "workspace")
}

func (c *Client) GetAgentActivity(ctx context.Context, id string) (json.RawMessage, error) {
	return c.getAgentRaw(ctx, id, "activity")
}

func (c *Client) GetAgentUsage(ctx context.Context, id string) (json.RawMessage, error) {
	return c.getAgentRaw(ctx, id, "usage")
}

func (c *Client) GetAgentInteractions(ctx context.Context, id string) (json.RawMessage, error) {
	return c.getAgentRaw(ctx, id, "interactions")
}

func (c *Client) GetAgentResources(ctx context.Context, id string) (json.RawMessage, error) {
	return c.getAgentRaw(ctx, id, "resources")
}

func (c *Client) GetAgentHealth(ctx context.Context, id string) (json.RawMessage, error) {
	return c.getAgentRaw(ctx, id, "health")
}

func (c *Client) getAgentRaw(ctx context.Context, id, suffix string) (json.RawMessage, error) {
	var resp json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/admin/agents/"+url.PathEscape(id)+"/"+suffix, nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp, nil
}
