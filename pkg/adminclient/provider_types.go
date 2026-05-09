package adminclient

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ListProviderTypes(ctx context.Context) ([]ProviderType, error) {
	var resp itemsResponse[ProviderType]
	if err := c.do(ctx, http.MethodGet, "/admin/provider_types", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) EnableProviderType(ctx context.Context, providerType string) (*boolStatusResponse, error) {
	return c.setProviderTypeEnabled(ctx, providerType, true)
}

func (c *Client) DisableProviderType(ctx context.Context, providerType string) (*boolStatusResponse, error) {
	return c.setProviderTypeEnabled(ctx, providerType, false)
}

func (c *Client) setProviderTypeEnabled(ctx context.Context, providerType string, enabled bool) (*boolStatusResponse, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var resp boolStatusResponse
	if err := c.do(ctx, http.MethodPost, "/admin/provider_types/"+url.PathEscape(providerType)+"/"+action, nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
