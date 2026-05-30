package adminclient

import (
	"context"
	"net/http"
)

func (c *Client) ListProviderTypes(ctx context.Context) ([]ProviderType, error) {
	var resp itemsResponse[ProviderType]
	if err := c.do(ctx, http.MethodGet, "/admin/provider_types", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}
