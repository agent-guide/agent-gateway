package client

import (
	"context"
	"net/http"
	"net/url"

	adminapi "github.com/agent-guide/agent-gateway/pkg/admin"
	connectorpkg "github.com/agent-guide/agent-gateway/pkg/connector"
	mcproute "github.com/agent-guide/agent-gateway/pkg/gateway/mcproute"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	mcptracking "github.com/agent-guide/agent-gateway/pkg/mcp/tracking"
)

type ConnectorConfig = connectorpkg.ConnectorConfig
type ConnectorView = adminapi.ConnectorView
type MCPRoute = mcproute.MCPRoute
type MCPRouteView = adminapi.MCPRouteView
type MCPRuntimeView = adminapi.MCPDispatcherRuntimeView
type MCPRuntimeInFlightRequest = mcptracking.InFlightRequest
type MCPRuntimeProgressNotification = mcptracking.ProgressNotification
type MCPRuntimeCompletedRequest = mcptracking.CompletedRequest
type ConnectorSessionView = adminapi.GatewaySessionView
type MCPTool = basemcp.Tool
type MCPToolCallRequest = adminapi.MCPToolCallRequest
type MCPToolResult = basemcp.ToolResult
type MCPResource = basemcp.Resource
type MCPResourceTemplate = basemcp.ResourceTemplate
type MCPResourceReadRequest = adminapi.MCPResourceReadRequest
type MCPResourceReadResult = basemcp.ResourceReadResult
type MCPPrompt = basemcp.Prompt
type MCPPromptGetRequest = adminapi.MCPPromptGetRequest
type MCPPromptResult = basemcp.PromptResult
type ConnectorCapabilities = map[string]any

func (c *Client) ListConnectors(ctx context.Context) ([]ConnectorView, error) {
	var resp itemsResponse[ConnectorView]
	if err := c.do(ctx, http.MethodGet, "/admin/connectors", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) CreateConnector(ctx context.Context, cfg ConnectorConfig) (*ConnectorView, error) {
	var resp ConnectorView
	if err := c.do(ctx, http.MethodPost, "/admin/connectors", cfg, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetConnector(ctx context.Context, id string) (*ConnectorView, error) {
	var resp ConnectorView
	if err := c.do(ctx, http.MethodGet, "/admin/connectors/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateConnector(ctx context.Context, id string, cfg ConnectorConfig) (*ConnectorView, error) {
	var resp ConnectorView
	if err := c.do(ctx, http.MethodPut, "/admin/connectors/"+url.PathEscape(id), cfg, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteConnector(ctx context.Context, id string) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.do(ctx, http.MethodDelete, "/admin/connectors/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetConnectorSession(ctx context.Context, id string) (*ConnectorSessionView, error) {
	var resp struct {
		Session *ConnectorSessionView `json:"session"`
	}
	if err := c.do(ctx, http.MethodGet, "/admin/connectors/"+url.PathEscape(id)+"/sessions", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Session, nil
}

func (c *Client) GetConnectorCapabilities(ctx context.Context, id string) (*ConnectorCapabilities, error) {
	var resp ConnectorCapabilities
	if err := c.do(ctx, http.MethodGet, "/admin/connectors/"+url.PathEscape(id)+"/capabilities", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListConnectorTools(ctx context.Context, id string) ([]MCPTool, error) {
	var resp itemsResponse[MCPTool]
	if err := c.do(ctx, http.MethodGet, "/admin/connectors/"+url.PathEscape(id)+"/tools", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) CallConnectorTool(ctx context.Context, id string, req MCPToolCallRequest) (*MCPToolResult, error) {
	var resp MCPToolResult
	if err := c.do(ctx, http.MethodPost, "/admin/connectors/"+url.PathEscape(id)+"/tools/call", req, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListConnectorResources(ctx context.Context, id string) ([]MCPResource, error) {
	var resp itemsResponse[MCPResource]
	if err := c.do(ctx, http.MethodGet, "/admin/connectors/"+url.PathEscape(id)+"/resources", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) ListConnectorResourceTemplates(ctx context.Context, id string) ([]MCPResourceTemplate, error) {
	var resp itemsResponse[MCPResourceTemplate]
	if err := c.do(ctx, http.MethodGet, "/admin/connectors/"+url.PathEscape(id)+"/resource-templates", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) ReadConnectorResource(ctx context.Context, id string, req MCPResourceReadRequest) (*MCPResourceReadResult, error) {
	var resp MCPResourceReadResult
	if err := c.do(ctx, http.MethodPost, "/admin/connectors/"+url.PathEscape(id)+"/resources/read", req, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListConnectorPrompts(ctx context.Context, id string) ([]MCPPrompt, error) {
	var resp itemsResponse[MCPPrompt]
	if err := c.do(ctx, http.MethodGet, "/admin/connectors/"+url.PathEscape(id)+"/prompts", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetConnectorPrompt(ctx context.Context, id string, req MCPPromptGetRequest) (*MCPPromptResult, error) {
	var resp MCPPromptResult
	if err := c.do(ctx, http.MethodPost, "/admin/connectors/"+url.PathEscape(id)+"/prompts/get", req, &resp, true, http.StatusOK); err != nil {
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

func (c *Client) GetMCPRoute(ctx context.Context, id string) (*MCPRouteView, error) {
	var resp MCPRouteView
	if err := c.do(ctx, http.MethodGet, "/admin/mcp/routes/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateMCPRoute(ctx context.Context, cfg MCPRoute) (*MCPRouteView, error) {
	body, err := withoutManagedTimestamps(cfg)
	if err != nil {
		return nil, err
	}
	var resp MCPRouteView
	if err := c.do(ctx, http.MethodPost, "/admin/mcp/routes", body, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateMCPRoute(ctx context.Context, id string, cfg MCPRoute) (*MCPRouteView, error) {
	body, err := withoutManagedTimestamps(cfg)
	if err != nil {
		return nil, err
	}
	var resp MCPRouteView
	if err := c.do(ctx, http.MethodPut, "/admin/mcp/routes/"+url.PathEscape(id), body, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteMCPRoute(ctx context.Context, id string) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.do(ctx, http.MethodDelete, "/admin/mcp/routes/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetMCPRuntime(ctx context.Context) (*MCPRuntimeView, error) {
	var resp MCPRuntimeView
	if err := c.do(ctx, http.MethodGet, "/admin/mcp/runtime", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListMCPRuntimeInFlight(ctx context.Context) ([]MCPRuntimeInFlightRequest, error) {
	var resp itemsResponse[MCPRuntimeInFlightRequest]
	if err := c.do(ctx, http.MethodGet, "/admin/mcp/runtime/inflight", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) ListMCPRuntimeProgress(ctx context.Context) ([]MCPRuntimeProgressNotification, error) {
	var resp itemsResponse[MCPRuntimeProgressNotification]
	if err := c.do(ctx, http.MethodGet, "/admin/mcp/runtime/progress", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) ListMCPRuntimeHistory(ctx context.Context, routeID string) ([]MCPRuntimeCompletedRequest, error) {
	var resp itemsResponse[MCPRuntimeCompletedRequest]
	path := "/admin/mcp/runtime/history"
	if routeID != "" {
		path += "?route_id=" + url.QueryEscape(routeID)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}
