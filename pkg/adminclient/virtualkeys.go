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

func (c *Client) GetVirtualKey(ctx context.Context, id string) (*VirtualKey, error) {
	var resp VirtualKey
	if err := c.do(ctx, http.MethodGet, "/admin/virtual_keys/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
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

func (c *Client) UpdateVirtualKey(ctx context.Context, id string, req VirtualKeyConfig) (*VirtualKeyConfig, error) {
	var resp VirtualKeyConfig
	if err := c.do(ctx, http.MethodPut, "/admin/virtual_keys/"+url.PathEscape(id), req, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteVirtualKey(ctx context.Context, id string) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.do(ctx, http.MethodDelete, "/admin/virtual_keys/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) EnableVirtualKey(ctx context.Context, id string) (*VirtualKey, error) {
	return c.setVirtualKeyEnabled(ctx, id, true)
}

func (c *Client) DisableVirtualKey(ctx context.Context, id string) (*VirtualKey, error) {
	return c.setVirtualKeyEnabled(ctx, id, false)
}

func (c *Client) setVirtualKeyEnabled(ctx context.Context, id string, enabled bool) (*VirtualKey, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var resp VirtualKey
	if err := c.do(ctx, http.MethodPost, "/admin/virtual_keys/"+url.PathEscape(id)+"/"+action, nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
