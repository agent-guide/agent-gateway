package openaibase

import (
	"errors"
	"strconv"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

// NormalizeError converts OpenAI-compatible SDK errors into provider.UpstreamError.
func NormalizeError(err error) error {
	if err == nil {
		return nil
	}
	if fields := provider.UpstreamErrorFields(err); len(fields) > 0 {
		return err
	}
	var upstreamErr *provider.UpstreamError
	if errors.As(err, &upstreamErr) {
		return err
	}
	msg := strings.TrimSpace(err.Error())
	statusCodeLabel := "status code:"
	codeIndex := strings.Index(msg, statusCodeLabel)
	if codeIndex < 0 {
		return err
	}

	rest := strings.TrimSpace(msg[codeIndex+len(statusCodeLabel):])
	codePart := rest
	if comma := strings.Index(codePart, ","); comma >= 0 {
		codePart = codePart[:comma]
	}
	code, convErr := strconv.Atoi(strings.TrimSpace(codePart))
	if convErr != nil {
		return err
	}
	upstreamErr = &provider.UpstreamError{Status: code}

	statusText := fieldValueFromMessage(msg, "status:")
	if statusText != "" {
		upstreamErr.StatusText = statusText
	}
	body := fieldValueFromMessage(msg, "message:")
	if body != "" {
		upstreamErr.Body = body
	}
	return upstreamErr
}

func fieldValueFromMessage(msg string, label string) string {
	idx := strings.Index(msg, label)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(msg[idx+len(label):])
	if comma := strings.Index(rest, ","); comma >= 0 {
		return strings.TrimSpace(rest[:comma])
	}
	return rest
}
