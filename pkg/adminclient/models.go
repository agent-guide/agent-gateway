package adminclient

import (
	"context"
	"net/http"
	"net/url"

	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
)

type ProviderModelListOptions struct {
	ProviderID string
}

func (c *Client) ListDiscoveredModels(ctx context.Context, providerID string) ([]DiscoveredModel, error) {
	var resp itemsResponse[DiscoveredModel]
	if err := c.do(ctx, http.MethodGet, "/admin/llm/models/providers/"+url.PathEscape(providerID)+"/discovered", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

type RefreshDiscoveredModelsResponse struct {
	ProviderID string            `json:"provider_id"`
	Items      []DiscoveredModel `json:"items"`
}

func (c *Client) RefreshProviderModels(ctx context.Context, providerID string) (*RefreshDiscoveredModelsResponse, error) {
	var resp RefreshDiscoveredModelsResponse
	if err := c.do(ctx, http.MethodPost, "/admin/llm/models/providers/"+url.PathEscape(providerID)+"/refresh", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListProviderModels(ctx context.Context, opts ProviderModelListOptions) ([]ProviderModel, error) {
	path := "/admin/llm/models/provider"
	query := url.Values{}
	if opts.ProviderID != "" {
		query.Set("provider_id", opts.ProviderID)
		path = withQuery(path, query)
	}
	var resp itemsResponse[ProviderModel]
	if err := c.do(ctx, http.MethodGet, path, nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetProviderModel(ctx context.Context, providerID, upstreamModel string) (*ProviderModel, error) {
	var resp ProviderModel
	if err := c.do(ctx, http.MethodGet, "/admin/llm/models/provider/"+url.PathEscape(providerID)+"/"+url.PathEscape(upstreamModel), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateProviderModel(ctx context.Context, model modelcatalog.ProviderModel) (*ProviderModel, error) {
	var resp ProviderModel
	if err := c.do(ctx, http.MethodPost, "/admin/llm/models/provider", model, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateProviderModel(ctx context.Context, providerID, upstreamModel string, model modelcatalog.ProviderModel) (*ProviderModel, error) {
	var resp ProviderModel
	if err := c.do(ctx, http.MethodPut, "/admin/llm/models/provider/"+url.PathEscape(providerID)+"/"+url.PathEscape(upstreamModel), model, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteProviderModel(ctx context.Context, providerID, upstreamModel string) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.do(ctx, http.MethodDelete, "/admin/llm/models/provider/"+url.PathEscape(providerID)+"/"+url.PathEscape(upstreamModel), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
