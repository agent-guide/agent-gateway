package adminclient

import (
	"context"
	"net/http"
)

func (c *Client) ListLLMAPIHandlerTypes(ctx context.Context) ([]LLMAPIHandlerType, error) {
	var resp itemsResponse[LLMAPIHandlerType]
	if err := c.do(ctx, http.MethodGet, "/admin/llm_api_handler_types", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}
