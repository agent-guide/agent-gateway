package execution_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	agentexecution "github.com/agent-guide/agent-gateway/pkg/agent/execution"
)

func TestNormalizedErrorSupportsErrorsIsAndCodeExtraction(t *testing.T) {
	t.Parallel()

	native := errors.New("native detail")
	err := agentexecution.WrapError(agentexecution.ErrorBackendUnavailable, "backend unavailable", native)
	wrapped := fmt.Errorf("serve turn: %w", err)

	if !errors.Is(wrapped, agentexecution.ErrBackendUnavailable) {
		t.Fatalf("errors.Is(%v, ErrBackendUnavailable) = false", wrapped)
	}
	if !errors.Is(wrapped, native) {
		t.Fatalf("errors.Is(%v, native) = false", wrapped)
	}
	if code, ok := agentexecution.ErrorCodeOf(wrapped); !ok || code != agentexecution.ErrorBackendUnavailable {
		t.Fatalf("ErrorCodeOf() = %q, %v", code, ok)
	}
	if got := err.Error(); got != "backend unavailable" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestNormalizeErrorClosesOverNativeFailures(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		err  error
		code agentexecution.ErrorCode
	}{
		{context.Canceled, agentexecution.ErrorTurnCancelled},
		{context.DeadlineExceeded, agentexecution.ErrorBackendTimeout},
		{errors.New("exec /private/tool --token secret"), agentexecution.ErrorTurnFailed},
	} {
		normalized := agentexecution.NormalizeError(tt.err)
		if code, ok := agentexecution.ErrorCodeOf(normalized); !ok || code != tt.code {
			t.Errorf("NormalizeError(%v) = %v (%q), want %q", tt.err, normalized, code, tt.code)
		}
		if !errors.Is(normalized, tt.err) {
			t.Errorf("NormalizeError(%v) did not retain cause", tt.err)
		}
	}
}

func TestHTTPStatusAndPublicErrorRedaction(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		code   agentexecution.ErrorCode
		status int
	}{
		{agentexecution.ErrorAgentNotFound, 404},
		{agentexecution.ErrorInvalidRequest, 400},
		{agentexecution.ErrorRuntimeNotExecutable, 501},
		{agentexecution.ErrorCapabilityNotSupported, 501},
		{agentexecution.ErrorSessionBusy, 429},
		{agentexecution.ErrorSessionLimitExceeded, 429},
		{agentexecution.ErrorPermissionExpired, 410},
		{agentexecution.ErrorTurnLimitExceeded, 429},
		{agentexecution.ErrorBackendUnavailable, 503},
		{agentexecution.ErrorBackendTimeout, 504},
		{agentexecution.ErrorTurnFailed, 502},
		{agentexecution.ErrorTurnCancelled, 499},
	} {
		err := agentexecution.WrapError(tt.code, "token=secret /private/workspace", errors.New("command --password hunter2"))
		if got := agentexecution.HTTPStatus(err); got != tt.status {
			t.Errorf("HTTPStatus(%q) = %d, want %d", tt.code, got, tt.status)
		}
		public := agentexecution.PublicError(err)
		if public.ErrorType != tt.code {
			t.Errorf("PublicError(%q).ErrorType = %q", tt.code, public.ErrorType)
		}
		if strings.Contains(public.Message, "secret") || strings.Contains(public.Message, "/private") || strings.Contains(public.Message, "hunter2") {
			t.Errorf("PublicError(%q) leaked sensitive detail: %+v", tt.code, public)
		}
	}
}
