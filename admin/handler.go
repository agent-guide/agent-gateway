package admin

import (
	"net/http"
	"sync"

	"github.com/agent-guide/caddy-agent-gateway/admin/caddymgr"
	"github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/gateway"
	"github.com/agent-guide/caddy-agent-gateway/internal/utils"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/manager"
	"go.uber.org/zap"
)

// Handler handles Admin API requests under /admin/.
type Handler struct {
	cliauthManager     *manager.Manager
	configStore        intf.ConfigStorer
	routeManager       *gateway.RouteManager
	localAPIKeyManager *gateway.LocalAPIKeyManager
	providerManager    *gateway.ProviderManager
	caddyManager       *caddymgr.CaddyManager
	mux                *http.ServeMux
	logger             *zap.Logger
	cliAuthSessions    sync.Map // cliname -> cliAuthStatus
	sessions           *sessionStore
	adminUsername      string
	adminPasswordHash  string
}

// NewHandler constructs an admin Handler.
// caddyMgr may be nil; caddy server management endpoints will return 503 in that case.
// logger may be nil (a no-op logger is used in that case).
func NewHandler(agentGateway *gateway.AgentGateway, logger *zap.Logger, adminUser, adminPasswordHash string, caddyMgr *caddymgr.CaddyManager) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}

	var cliauthMgr *manager.Manager
	var configStore intf.ConfigStorer
	var routeManager *gateway.RouteManager
	var localAPIKeyManager *gateway.LocalAPIKeyManager
	var providerManager *gateway.ProviderManager
	if agentGateway != nil {
		cliauthMgr = agentGateway.CLIAuthManager()
		configStore = agentGateway.ConfigStore()
		routeManager = agentGateway.RouteManager()
		localAPIKeyManager = agentGateway.LocalAPIKeyManager()
		providerManager = agentGateway.ProviderManager()
	}

	h := &Handler{
		cliauthManager:     cliauthMgr,
		configStore:        configStore,
		routeManager:       routeManager,
		localAPIKeyManager: localAPIKeyManager,
		providerManager:    providerManager,
		caddyManager:       caddyMgr,
		logger:             logger,
		sessions:           newSessionStore(),
		adminUsername:      adminUser,
		adminPasswordHash:  adminPasswordHash,
	}
	h.mux = http.NewServeMux()
	for _, route := range h.Routes() {
		handler := route.Handler
		if route.RequireAuth {
			handler = h.requireAuth(handler)
		}
		h.mux.HandleFunc(route.Method+" "+route.Path, handler)
	}
	return h
}

// ServeHTTP dispatches admin API requests, including CORS preflight handling.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	lrw := utils.NewLoggingResponseWriter(w)
	defer func() {
		if recovered := recover(); recovered != nil {
			utils.LogHTTPError(h.logger, "admin request panicked", r, http.StatusInternalServerError, nil, zap.Any("panic", recovered))
			http.Error(lrw, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		utils.LogHTTPResponseError(h.logger, "admin request failed", r, lrw)
	}()

	if origin := r.Header.Get("Origin"); origin != "" {
		lrw.Header().Set("Access-Control-Allow-Origin", origin)
		lrw.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		lrw.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		lrw.Header().Set("Access-Control-Max-Age", "86400")
	}
	if r.Method == "OPTIONS" {
		lrw.WriteHeader(http.StatusNoContent)
		return
	}
	h.mux.ServeHTTP(lrw, r)
}
