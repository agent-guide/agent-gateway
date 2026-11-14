package dispatcher

import (
	"context"
	"errors"
	"net/http"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/agent"
	agentexecution "github.com/agent-guide/agent-gateway/pkg/agent/execution"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

func bindRuntimeRequestContext(ctx context.Context, agentID, runtimeType, runID, sessionID, requestID string) context.Context {
	dims, _ := usage.DimensionsFromContext(ctx)
	dims.AgentID = agentID
	dims.RuntimeType = runtimeType
	dims.RunID = runID
	ctx = usage.ContextWithDimensions(ctx, dims)
	return agentexecution.WithIdentities(ctx, agentexecution.Identities{
		AgentID: agentID, RuntimeType: runtimeType, RunID: runID,
		SessionID: sessionID, RequestID: requestID,
		TraceID: dims.TraceID, SpanID: dims.SpanID, ParentSpanID: dims.ParentSpanID,
	})
}

func normalizeAgentLookupError(err error) error {
	if errors.Is(err, agent.ErrAgentNotConfigured) || errors.Is(err, configstore.ErrNotFound) {
		return agentexecution.WrapError(agentexecution.ErrorAgentNotFound, "agent not found", err)
	}
	return agentexecution.WrapError(agentexecution.ErrorBackendUnavailable, "agent store is unavailable", err)
}

func permissionRequestID(decision *agentexecution.PermissionDecision) string {
	if decision == nil {
		return ""
	}
	return decision.RequestID
}

func writeRuntimePreStreamError(w http.ResponseWriter, request *http.Request, err error) error {
	public := agentexecution.PublicError(err)
	// The caller's span remains bound to the request context even when the
	// failure happens before a backend stream starts.
	usage.SpanFromContext(request.Context()).AddAnnotation("error_type", string(public.ErrorType))
	return httpjson.Error(w, agentexecution.HTTPStatus(err), public.Message)
}
