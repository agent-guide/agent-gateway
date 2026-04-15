package api

import (
	"fmt"
	"net/http"

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
	GenerateRequest *provider.GenerateRequest
	Stream          bool
	RawRequest      any
}

type ErrorContext struct {
	Protocol string
	RouteID  string
	Model    string
}

func WriteError(logger *zap.Logger, apiName, routeID, model string, w http.ResponseWriter, r *http.Request, err error, message string) error {
	status := statuserr.StatusCode(err, http.StatusBadGateway)
	return WriteLoggedError(logger, ErrorContext{
		Protocol: apiName,
		RouteID:  routeID,
		Model:    model,
	}, w, r, status, err.Error(), fmt.Errorf("%s: %w", message, err))
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
