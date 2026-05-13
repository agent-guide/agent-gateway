package admin

import (
	"net/http"
	"sync"

	"github.com/agent-guide/agent-gateway/internal/httpcapture"
	"github.com/agent-guide/agent-gateway/internal/httplog"
	"github.com/agent-guide/agent-gateway/pkg/cliauth"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"go.uber.org/zap"
)

// Handler handles Admin API requests under /admin/.
type Handler struct {
	cliauthManager     *cliauth.Manager
	cliauthRefresher   *cliauth.AutoRefresher
	credentialManager  *credentialmgr.Manager
	configStoreBackend configstore.ConfigStoreBackend
	routeManager       *routepkg.AgentRouteManager
	virtualKeyManager  *virtualkeypkg.VirtualKeyManager
	providerManager    *gateway.ProviderManager
	modelCatalog       modelcatalog.Service
	mux                *http.ServeMux
	logger             *zap.Logger
	cliAuthMu          sync.RWMutex
	cliAuthSessions    map[string]cliAuthStatus // login_id -> cliAuthStatus
	cliAuthActive      map[string]string        // cliname -> login_id
	sessions           *sessionStore
	adminUsername      string
	adminPasswordHash  string
}

// NewHandler constructs an admin Handler.
// logger may be nil (a no-op logger is used in that case).
func NewHandler(agentGateway *gateway.AgentGateway, logger *zap.Logger, adminUser, adminPasswordHash string) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}

	var cliauthMgr *cliauth.Manager
	var cliauthRefresher *cliauth.AutoRefresher
	var credentialMgr *credentialmgr.Manager
	var configStoreBackend configstore.ConfigStoreBackend
	var routeManager *routepkg.AgentRouteManager
	var virtualKeyManager *virtualkeypkg.VirtualKeyManager
	var providerManager *gateway.ProviderManager
	var modelCatalogSvc modelcatalog.Service
	if agentGateway != nil {
		cliauthMgr = agentGateway.CLIAuthManager()
		cliauthRefresher = agentGateway.CLIAuthRefresher()
		credentialMgr = agentGateway.CredentialManager()
		configStoreBackend = agentGateway.ConfigStoreBackend()
		routeManager = agentGateway.AgentRouteManager()
		virtualKeyManager = agentGateway.VirtualKeyManager()
		providerManager = agentGateway.ProviderManager()
		modelCatalogSvc = agentGateway.ModelCatalog()
	}

	h := &Handler{
		cliauthManager:     cliauthMgr,
		cliauthRefresher:   cliauthRefresher,
		credentialManager:  credentialMgr,
		configStoreBackend: configStoreBackend,
		routeManager:       routeManager,
		virtualKeyManager:  virtualKeyManager,
		providerManager:    providerManager,
		modelCatalog:       modelCatalogSvc,
		logger:             logger,
		cliAuthSessions:    map[string]cliAuthStatus{},
		cliAuthActive:      map[string]string{},
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
