package adminclient

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ListProviders(ctx context.Context, opts ProviderListOptions) ([]Provider, error) {
	query := url.Values{}
	if opts.ProviderType != "" {
		query.Set("provider_type", opts.ProviderType)
	}
	var resp itemsResponse[Provider]
	if err := c.do(ctx, http.MethodGet, withQuery("/admin/providers", query), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetProvider(ctx context.Context, id string) (*Provider, error) {
	var resp Provider
	if err := c.do(ctx, http.MethodGet, "/admin/providers/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateProvider(ctx context.Context, cfg ProviderConfig) (*Provider, error) {
	var resp Provider
	if err := c.do(ctx, http.MethodPost, "/admin/providers", cfg, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateProvider(ctx context.Context, id string, cfg ProviderConfig) (*Provider, error) {
	var resp Provider
	if err := c.do(ctx, http.MethodPut, "/admin/providers/"+url.PathEscape(id), cfg, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteProvider(ctx context.Context, id string) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.do(ctx, http.MethodDelete, "/admin/providers/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) EnableProvider(ctx context.Context, id string) (*Provider, error) {
	return c.setProviderEnabled(ctx, id, true)
}

func (c *Client) DisableProvider(ctx context.Context, id string) (*Provider, error) {
	return c.setProviderEnabled(ctx, id, false)
}

func (c *Client) setProviderEnabled(ctx context.Context, id string, enabled bool) (*Provider, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var resp Provider
	if err := c.do(ctx, http.MethodPost, "/admin/providers/"+url.PathEscape(id)+"/"+action, nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
