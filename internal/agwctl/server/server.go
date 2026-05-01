package server

import (
	"log"
	"net/http"

	"github.com/agent-guide/caddy-agent-gateway/internal/agwctl/caddyadmin"
	"github.com/agent-guide/caddy-agent-gateway/internal/agwctl/gatewayadmin"
)

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (lw *loggingResponseWriter) WriteHeader(code int) {
	lw.status = code
	lw.ResponseWriter.WriteHeader(code)
}

func (lw *loggingResponseWriter) Write(b []byte) (int, error) {
	if lw.status == 0 {
		lw.status = http.StatusOK
	}
	return lw.ResponseWriter.Write(b)
}

type Server struct {
	caddy             *caddyadmin.Manager
	gateway           *gatewayadmin.Proxy
	mux               *http.ServeMux
	sessions          *sessionStore
	adminUsername     string
	adminPasswordHash string
}

func New(caddyAdminAddr, adminUsername, adminPasswordHash string, readOnlyServerIDs []string, gw *gatewayadmin.Proxy) *Server {
	caddyMgr := caddyadmin.NewManager(caddyAdminAddr)
	caddyMgr.SetReadOnlyServerIDs(readOnlyServerIDs)
	s := &Server{
		caddy:             caddyMgr,
		gateway:           gw,
		sessions:          newSessionStore(),
		adminUsername:     adminUsername,
		adminPasswordHash: adminPasswordHash,
	}
	s.mux = s.buildMux()
	return s
}

func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/health", s.handleHealth)
	mux.HandleFunc("POST /admin/auth/login", s.handleLogin)
	mux.HandleFunc("POST /admin/auth/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET /admin/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("GET /admin/caddy/servers", s.requireAuth(s.handleListCaddyServers))
	mux.HandleFunc("POST /admin/caddy/servers", s.requireAuth(s.handleCreateCaddyServer))
	mux.HandleFunc("GET /admin/caddy/servers/{id}", s.requireAuth(s.handleGetCaddyServer))
	mux.HandleFunc("PUT /admin/caddy/servers/{id}", s.requireAuth(s.handleUpdateCaddyServer))
	mux.HandleFunc("DELETE /admin/caddy/servers/{id}", s.requireAuth(s.handleDeleteCaddyServer))
	mux.HandleFunc("GET /admin/caddy/servers/{id}/routes", s.requireAuth(s.handleListCaddyRoutes))
	mux.HandleFunc("POST /admin/caddy/servers/{id}/routes", s.requireAuth(s.handleAddCaddyRoute))
	mux.HandleFunc("PUT /admin/caddy/servers/{id}/routes/{routeId}", s.requireAuth(s.handleUpdateCaddyRoute))
	mux.HandleFunc("DELETE /admin/caddy/servers/{id}/routes/{routeId}", s.requireAuth(s.handleDeleteCaddyRoute))
	if s.gateway != nil {
		mux.HandleFunc("/admin/", s.requireAuth(s.handleGatewayProxy))
	}
	return mux
}

func (s *Server) handleGatewayProxy(w http.ResponseWriter, r *http.Request) {
	s.gateway.ServeProxy(w, r)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	lw := &loggingResponseWriter{ResponseWriter: w}
	s.mux.ServeHTTP(lw, r)
	if lw.status >= 400 {
		log.Printf("http error: %s %s -> %d", r.Method, r.URL.Path, lw.status)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	_ = writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
