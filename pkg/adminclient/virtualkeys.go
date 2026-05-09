package adminclient

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ListVirtualKeys(ctx context.Context, opts VirtualKeyListOptions) ([]VirtualKey, error) {
	query := url.Values{}
	if opts.Tag != "" {
		query.Set("tag", opts.Tag)
	}
	var resp itemsResponse[VirtualKey]
	if err := c.do(ctx, http.MethodGet, withQuery("/admin/virtual_keys", query), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetVirtualKey(ctx context.Context, key string) (*VirtualKey, error) {
	var resp VirtualKey
	if err := c.do(ctx, http.MethodGet, "/admin/virtual_keys/"+url.PathEscape(key), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateVirtualKey(ctx context.Context, key VirtualKeyConfig) (*VirtualKeyConfig, error) {
	var resp VirtualKeyConfig
	if err := c.do(ctx, http.MethodPost, "/admin/virtual_keys", key, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateVirtualKey(ctx context.Context, key string, req VirtualKeyConfig) (*VirtualKeyConfig, error) {
	var resp VirtualKeyConfig
	if err := c.do(ctx, http.MethodPut, "/admin/virtual_keys/"+url.PathEscape(key), req, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteVirtualKey(ctx context.Context, key string) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.do(ctx, http.MethodDelete, "/admin/virtual_keys/"+url.PathEscape(key), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) EnableVirtualKey(ctx context.Context, key string) (*VirtualKey, error) {
	return c.setVirtualKeyEnabled(ctx, key, true)
}

func (c *Client) DisableVirtualKey(ctx context.Context, key string) (*VirtualKey, error) {
	return c.setVirtualKeyEnabled(ctx, key, false)
}

func (c *Client) setVirtualKeyEnabled(ctx context.Context, key string, enabled bool) (*VirtualKey, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var resp VirtualKey
	if err := c.do(ctx, http.MethodPost, "/admin/virtual_keys/"+url.PathEscape(key)+"/"+action, nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
