package api

import (
	"fmt"
	"net/http"

	"github.com/agent-guide/caddy-agent-gateway/internal/utils"
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

func WriteError(logger *zap.Logger, apiName, routeID, model string, w http.ResponseWriter, r *http.Request, err error, message string) error {
	status := utils.StatusCode(err)
	return utils.WriteLoggedError(logger, w, r, status, err.Error(), fmt.Errorf("%s: %w", message, err),
		zap.String("protocol", apiName),
		zap.String("route_id", routeID),
		zap.String("model", model),
	)
}
