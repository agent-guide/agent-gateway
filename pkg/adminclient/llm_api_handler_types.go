package adminclient

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ListLLMAPIHandlerTypes(ctx context.Context) ([]LLMAPIHandlerType, error) {
	var resp itemsResponse[LLMAPIHandlerType]
	if err := c.do(ctx, http.MethodGet, "/admin/llm_api_handler_types", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) EnableLLMAPIHandlerType(ctx context.Context, handlerType string) (*boolStatusResponse, error) {
	return c.setLLMAPIHandlerTypeEnabled(ctx, handlerType, true)
}

func (c *Client) DisableLLMAPIHandlerType(ctx context.Context, handlerType string) (*boolStatusResponse, error) {
	return c.setLLMAPIHandlerTypeEnabled(ctx, handlerType, false)
}

func (c *Client) setLLMAPIHandlerTypeEnabled(ctx context.Context, handlerType string, enabled bool) (*boolStatusResponse, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var resp boolStatusResponse
	if err := c.do(ctx, http.MethodPost, "/admin/llm_api_handler_types/"+url.PathEscape(handlerType)+"/"+action, nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
