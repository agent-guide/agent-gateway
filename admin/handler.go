package admin

import (
	"net/http"
	"sync"

	"github.com/agent-guide/caddy-agent-gateway/admin/caddymgr"
	"github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/gateway"
	localapikeypkg "github.com/agent-guide/caddy-agent-gateway/gateway/localapikey"
	"github.com/agent-guide/caddy-agent-gateway/internal/httpcapture"
	"github.com/agent-guide/caddy-agent-gateway/internal/httplog"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"go.uber.org/zap"
)

// Handler handles Admin API requests under /admin/.
type Handler struct {
	cliauthManager     *cliauth.Manager
	credentialManager  *credentialmgr.Manager
	configStore        intf.ConfigStorer
	routeManager       *gateway.RouteManager
	localAPIKeyManager *localapikeypkg.LocalAPIKeyManager
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

	var cliauthMgr *cliauth.Manager
	var credentialMgr *credentialmgr.Manager
	var configStore intf.ConfigStorer
	var routeManager *gateway.RouteManager
	var localAPIKeyManager *localapikeypkg.LocalAPIKeyManager
	var providerManager *gateway.ProviderManager
	if agentGateway != nil {
		cliauthMgr = agentGateway.CLIAuthManager()
		credentialMgr = agentGateway.CredentialManager()
		configStore = agentGateway.ConfigStore()
		routeManager = agentGateway.RouteManager()
		localAPIKeyManager = agentGateway.LocalAPIKeyManager()
		providerManager = agentGateway.ProviderManager()
	}

	h := &Handler{
		cliauthManager:     cliauthMgr,
		credentialManager:  credentialMgr,
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
	rr := httpcapture.NewResponseRecorder(w)
	defer func() {
		if recovered := recover(); recovered != nil {
			httplog.Error(h.logger, "admin request panicked", r, http.StatusInternalServerError, nil, zap.Any("panic", recovered))
			http.Error(rr, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		httplog.ResponseError(h.logger, "admin request failed", r, rr)
	}()

	if origin := r.Header.Get("Origin"); origin != "" {
		rr.Header().Set("Access-Control-Allow-Origin", origin)
		rr.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		rr.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		rr.Header().Set("Access-Control-Max-Age", "86400")
	}
	if r.Method == "OPTIONS" {
		rr.WriteHeader(http.StatusNoContent)
		return
	}
	h.mux.ServeHTTP(rr, r)
}
