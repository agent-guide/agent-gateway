package adminclient

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"net/http"
	"net/url"
)

func (c *Client) ListLogicalModels(ctx context.Context) ([]modelcatalog.LogicalModel, error) {
	var resp itemsResponse[modelcatalog.LogicalModel]
	if err := c.do(ctx, http.MethodGet, "/admin/llm/models/logical", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}
func (c *Client) GetLogicalModel(ctx context.Context, id string) (*modelcatalog.LogicalModel, error) {
	var resp modelcatalog.LogicalModel
	if err := c.do(ctx, http.MethodGet, "/admin/llm/models/logical/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
func (c *Client) CreateLogicalModel(ctx context.Context, m modelcatalog.LogicalModel) (*modelcatalog.LogicalModel, error) {
	var resp modelcatalog.LogicalModel
	if err := c.do(ctx, http.MethodPost, "/admin/llm/models/logical", m, &resp, true, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}
func (c *Client) UpdateLogicalModel(ctx context.Context, id string, m modelcatalog.LogicalModel) (*modelcatalog.LogicalModel, error) {
	var resp modelcatalog.LogicalModel
	if err := c.do(ctx, http.MethodPut, "/admin/llm/models/logical/"+url.PathEscape(id), m, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
func (c *Client) DeleteLogicalModel(ctx context.Context, id string) (*StatusResponse, error) {
	var resp StatusResponse
	if err := c.do(ctx, http.MethodDelete, "/admin/llm/models/logical/"+url.PathEscape(id), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
