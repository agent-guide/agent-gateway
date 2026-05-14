package anthropic

import (
	"errors"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	anthropicapi "github.com/anthropics/anthropic-sdk-go"
)

// normalizeError converts Anthropic SDK errors into provider.UpstreamError.
func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	if fields := provider.UpstreamErrorFields(err); len(fields) > 0 {
		return err
	}

	var apiErr *anthropicapi.Error
	if !errors.As(err, &apiErr) || apiErr == nil {
		return err
	}

	upstreamErr := &provider.UpstreamError{Status: apiErr.StatusCode}
	if apiErr.Response != nil && apiErr.Response.Status != "" {
		upstreamErr.StatusText = apiErr.Response.Status
	}
	if body := apiErr.RawJSON(); body != "" {
		upstreamErr.Body = body
	}
	return upstreamErr
}
