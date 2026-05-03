package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/caddy-agent-gateway/internal/httplog"
	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"
)

type LLMApiHandler interface {
	caddy.Module
	Name() string
	MatchLLMApi(*http.Request) bool
	PrepareLLMApiRequest(*http.Request) (*PreparedLLMApiRequest, error)
	ServeLLMApi(http.ResponseWriter, *http.Request, provider.Provider, *PreparedLLMApiRequest) error
}

type PreparedLLMApiRequest struct {
	Type             provider.LLMApiRequestType
	ChatRequest      *provider.ChatRequest
	EmbeddingRequest *provider.EmbeddingRequest
	ResponsesRequest *provider.ResponsesRequest
	StreamRequested  bool
	RawRequest       any
}

func (r *PreparedLLMApiRequest) Model() string {
	if r == nil {
		return ""
	}
	switch r.Type {
	case provider.LLMApiRequestTypeEmbedding:
		if r.EmbeddingRequest != nil {
			return r.EmbeddingRequest.Model
		}
	case provider.LLMApiRequestTypeResponses:
		if r.ResponsesRequest != nil {
			return r.ResponsesRequest.Model
		}
	default:
		if r.ChatRequest != nil {
			return r.ChatRequest.Model
		}
	}
	return ""
}

func (r *PreparedLLMApiRequest) Stream() bool {
	if r == nil {
		return false
	}
	return r.StreamRequested
}

func (r *PreparedLLMApiRequest) IsValid() bool {
	if r == nil {
		return false
	}
	switch r.Type {
	case provider.LLMApiRequestTypeEmbedding:
		return r.EmbeddingRequest != nil
	case provider.LLMApiRequestTypeResponses:
		return r.ResponsesRequest != nil
	case provider.LLMApiRequestTypeChat:
		return r.ChatRequest != nil
	default:
		return r.ChatRequest != nil || r.EmbeddingRequest != nil || r.ResponsesRequest != nil
	}
}

func (r *PreparedLLMApiRequest) SetModel(model string) {
	if r == nil {
		return
	}
	switch r.Type {
	case provider.LLMApiRequestTypeEmbedding:
		if r.EmbeddingRequest != nil {
			r.EmbeddingRequest.Model = model
		}
	case provider.LLMApiRequestTypeResponses:
		if r.ResponsesRequest != nil {
			r.ResponsesRequest.Model = model
		}
	default:
		if r.ChatRequest != nil {
			r.ChatRequest.Model = model
		}
	}
}

type ErrorContext struct {
	Protocol string
	RouteID  string
	Model    string
}

const StatusClientClosedRequest = 499

func WriteError(logger *zap.Logger, apiName, routeID, model string, w http.ResponseWriter, r *http.Request, err error, message string) error {
	status := statuserr.StatusCode(err, http.StatusBadGateway)
	return WriteLoggedError(logger, ErrorContext{
		Protocol: apiName,
		RouteID:  routeID,
		Model:    model,
	}, w, r, status, err.Error(), fmt.Errorf("%s: %w", message, err))
}

func WriteProviderError(logger *zap.Logger, ctx ErrorContext, w http.ResponseWriter, r *http.Request, err error, phase string) error {
	status := statuserr.StatusCode(err, http.StatusBadGateway)
	clientMessage := err.Error()
	if IsClientCanceled(err) {
		status = StatusClientClosedRequest
		clientMessage = "client canceled request"
	}
	return WriteLoggedError(logger, ctx, w, r, status, clientMessage, fmt.Errorf("%s: %w", phase, err))
}

func IsClientCanceled(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "context canceled")
}

func WriteLoggedError(logger *zap.Logger, ctx ErrorContext, w http.ResponseWriter, r *http.Request, status int, clientMessage string, cause error, fields ...zap.Field) error {
	logFields := []zap.Field{
		zap.String("protocol", ctx.Protocol),
		zap.String("route_id", ctx.RouteID),
		zap.String("model", ctx.Model),
	}
	logFields = append(logFields, fields...)
	return httplog.WriteError(logger, w, r, status, clientMessage, cause, logFields...)
}
