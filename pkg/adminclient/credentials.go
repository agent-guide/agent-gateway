package adminclient

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ListCredentials(ctx context.Context, opts CredentialListOptions) ([]Credential, error) {
	query := url.Values{}
	if opts.ProviderType != "" {
		query.Set("provider_type", opts.ProviderType)
	}
	if opts.ProviderID != "" {
		query.Set("provider_id", opts.ProviderID)
	}
	if opts.Source != "" {
		query.Set("source", opts.Source)
	}
	var resp itemsResponse[Credential]
	if err := c.do(ctx, http.MethodGet, withQuery("/admin/credentials", query), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetCredential(ctx context.Context, id string) (*Credential, error) {
	var resp Credential
	if err := c.do(ctx, http.MethodGet, "/admin/credentials/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateCredential(ctx context.Context, req CreateCredentialRequest) (*ManagedCredential, error) {
	var resp ManagedCredential
	if err := c.do(ctx, http.MethodPost, "/admin/credentials", req, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateCredential(ctx context.Context, id string, req UpdateCredentialRequest) (*ManagedCredential, error) {
	var resp ManagedCredential
	if err := c.do(ctx, http.MethodPut, "/admin/credentials/"+url.PathEscape(id), req, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteCredential(ctx context.Context, id string) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.do(ctx, http.MethodDelete, "/admin/credentials/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
