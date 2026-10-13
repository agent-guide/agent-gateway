package adminclient

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"net/http"
	"net/url"
)

func (c *Client) Manifest(ctx context.Context) (*distribution.Assembly, error) {
	var out distribution.Assembly
	err := c.do(ctx, http.MethodGet, "/admin/manifest", nil, &out, true, http.StatusOK)
	return &out, err
}
func (c *Client) ListExtensions(ctx context.Context) ([]extension.Envelope, error) {
	var out itemsResponse[extension.Envelope]
	err := c.do(ctx, http.MethodGet, "/admin/extensions", nil, &out, true, http.StatusOK)
	return out.Items, err
}
func (c *Client) GetExtension(ctx context.Context, id string) (*extension.Envelope, error) {
	var out extension.Envelope
	err := c.do(ctx, http.MethodGet, "/admin/extensions/"+url.PathEscape(id), nil, &out, true, http.StatusOK)
	return &out, err
}
func (c *Client) PutExtension(ctx context.Context, e extension.Envelope) (*extension.Envelope, error) {
	var out extension.Envelope
	err := c.do(ctx, http.MethodPut, "/admin/extensions/"+url.PathEscape(e.ModuleID), e, &out, true, http.StatusOK)
	return &out, err
}
func (c *Client) DeleteExtension(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/admin/extensions/"+url.PathEscape(id), nil, nil, true, http.StatusOK)
}
