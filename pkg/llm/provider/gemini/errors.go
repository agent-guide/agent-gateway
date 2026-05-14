package gemini

import (
	"errors"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"google.golang.org/genai"
)

// normalizeError converts Gemini SDK errors into provider.UpstreamError.
func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	if fields := provider.UpstreamErrorFields(err); len(fields) > 0 {
		return err
	}

	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	upstreamErr := &provider.UpstreamError{Status: apiErr.Code}
	if apiErr.Status != "" {
		upstreamErr.StatusText = apiErr.Status
	}
	if apiErr.Message != "" {
		upstreamErr.Body = apiErr.Message
	}
	return upstreamErr
}
