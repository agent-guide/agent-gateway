package adminclient

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ListRoutes(ctx context.Context, opts RouteListOptions) ([]Route, error) {
	query := url.Values{}
	if opts.Tag != "" {
		query.Set("tag", opts.Tag)
	}
	if opts.TagPrefix != "" {
		query.Set("tag_prefix", opts.TagPrefix)
	}
	var resp itemsResponse[Route]
	if err := c.do(ctx, http.MethodGet, withQuery("/admin/routes", query), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetRoute(ctx context.Context, id string) (*Route, error) {
	var resp Route
	if err := c.do(ctx, http.MethodGet, "/admin/routes/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateRoute(ctx context.Context, route RouteConfig) (*RouteConfig, error) {
	var resp RouteConfig
	if err := c.do(ctx, http.MethodPost, "/admin/routes", route, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateRoute(ctx context.Context, id string, route RouteConfig) (*RouteConfig, error) {
	var resp RouteConfig
	if err := c.do(ctx, http.MethodPut, "/admin/routes/"+url.PathEscape(id), route, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteRoute(ctx context.Context, id string) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.do(ctx, http.MethodDelete, "/admin/routes/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) EnableRoute(ctx context.Context, id string) (*Route, error) {
	return c.setRouteEnabled(ctx, id, true)
}

func (c *Client) DisableRoute(ctx context.Context, id string) (*Route, error) {
	return c.setRouteEnabled(ctx, id, false)
}

func (c *Client) setRouteEnabled(ctx context.Context, id string, enabled bool) (*Route, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var resp Route
	if err := c.do(ctx, http.MethodPost, "/admin/routes/"+url.PathEscape(id)+"/"+action, nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
