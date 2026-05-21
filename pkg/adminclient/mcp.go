package adminclient

import (
	"context"
	"net/http"
	"net/url"

	adminapi "github.com/agent-guide/agent-gateway/pkg/admin"
	mcproute "github.com/agent-guide/agent-gateway/pkg/gateway/mcproute"
	mcpservice "github.com/agent-guide/agent-gateway/pkg/mcp/service"
)

type MCPServiceConfig = mcpservice.MCPServiceConfig
type MCPServiceView = adminapi.MCPServiceView
type MCPRouteConfig = mcproute.MCPRouteConfig
type MCPRouteView = adminapi.MCPRouteView

func (c *Client) ListMCPServices(ctx context.Context) ([]MCPServiceView, error) {
	var resp itemsResponse[MCPServiceView]
	if err := c.do(ctx, http.MethodGet, "/admin/mcp/services", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) CreateMCPService(ctx context.Context, cfg MCPServiceConfig) (*MCPServiceView, error) {
	var resp MCPServiceView
	if err := c.do(ctx, http.MethodPost, "/admin/mcp/services", cfg, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateMCPService(ctx context.Context, id string, cfg MCPServiceConfig) (*MCPServiceView, error) {
	var resp MCPServiceView
	if err := c.do(ctx, http.MethodPut, "/admin/mcp/services/"+url.PathEscape(id), cfg, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListMCPRoutes(ctx context.Context) ([]MCPRouteView, error) {
	var resp itemsResponse[MCPRouteView]
	if err := c.do(ctx, http.MethodGet, "/admin/mcp/routes", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) CreateMCPRoute(ctx context.Context, cfg MCPRouteConfig) (*MCPRouteView, error) {
	var resp MCPRouteView
	if err := c.do(ctx, http.MethodPost, "/admin/mcp/routes", cfg, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateMCPRoute(ctx context.Context, id string, cfg MCPRouteConfig) (*MCPRouteView, error) {
	var resp MCPRouteView
	if err := c.do(ctx, http.MethodPut, "/admin/mcp/routes/"+url.PathEscape(id), cfg, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
