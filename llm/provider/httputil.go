package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
)

type credentialKey struct{}

// WithCredential attaches a credential to the context for per-request auth override.
// The openaibase Base reads this in setHeaders to replace the static APIKey.
func WithCredential(ctx context.Context, cred *credentialmgr.Credential) context.Context {
	return context.WithValue(ctx, credentialKey{}, cred)
}

// CredentialFromContext retrieves the per-request credential from the context.
func CredentialFromContext(ctx context.Context) (*credentialmgr.Credential, bool) {
	cred, ok := ctx.Value(credentialKey{}).(*credentialmgr.Credential)
	return cred, ok && cred != nil
}

// httpStatusError is a simple StatusError implementation for use by providers.
type httpStatusError struct {
	code    int
	message string
}

// NewStatusError creates a StatusError with the given HTTP status code.
func NewStatusError(code int, message string) StatusError {
	return &httpStatusError{code: code, message: message}
}

func (e *httpStatusError) Error() string   { return e.message }
func (e *httpStatusError) StatusCode() int { return e.code }

// CheckResponse returns a StatusError if the HTTP response status is not 2xx.
// It reads up to 4 KB of the body to include in the error message.
func CheckResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return NewStatusError(resp.StatusCode,
		fmt.Sprintf("upstream %d: %s", resp.StatusCode, string(body)))
}

// WrapEinoError wraps an error from an eino provider call as a StatusError.
// If the error already implements StatusError it is returned unchanged.
// Otherwise it is wrapped with a 502 Bad Gateway so the handler layer can
// make retry/degradation decisions based on a status code.
func WrapEinoError(err error) error {
	if err == nil {
		return nil
	}
	var se StatusError
	if errors.As(err, &se) {
		return err
	}
	return NewStatusError(http.StatusBadGateway, err.Error())
}

// RetryGenerate retries fn up to NetworkConfig.MaxRetries times on retryable
// errors (429, 5xx). Non-retryable 4xx errors are returned immediately.
// Do NOT use this for streaming — retry semantics are undefined once a stream starts.
func RetryGenerate[T any](config NetworkConfig, fn func() (T, error)) (T, error) {
	maxRetries := config.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	delay := time.Duration(config.RetryDelaySeconds) * time.Second
	if delay <= 0 {
		delay = time.Second
	}
	var zero T
	var last error
	for i := 0; i <= maxRetries; i++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		last = WrapEinoError(err)
		if !isRetryable(last) {
			return zero, last
		}
		if i < maxRetries {
			time.Sleep(delay * time.Duration(i+1))
		}
	}
	return zero, last
}

func isRetryable(err error) bool {
	var se StatusError
	if errors.As(err, &se) {
		code := se.StatusCode()
		return code == http.StatusTooManyRequests || (code >= 500 && code < 600)
	}
	return true // network errors without status code are retryable
}

func ResolveCredential(ctx context.Context, config ProviderConfig) (apiKey string, baseURL string) {
	config.Defaults()
	apiKey = config.APIKey
	baseURL = config.BaseURL

	if c, ok := CredentialFromContext(ctx); ok {
		if token, _ := c.Metadata["access_token"].(string); token != "" {
			apiKey = token
		} else if key := strings.TrimSpace(c.APIKey()); key != "" {
			apiKey = key
		}
		if u := strings.TrimSpace(c.BaseURL()); u != "" {
			baseURL = u
		}
	}

	return apiKey, baseURL
}
