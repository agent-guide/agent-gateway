package admin

import (
	"errors"
	"net/http"

	"github.com/agent-guide/caddy-agent-gateway/admin/caddymgr"
	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
)

// ── Server handlers ───────────────────────────────────────────────────────────

// handleListCaddyServers lists all HTTP servers registered in Caddy.
// GET /admin/caddy/servers
func (h *Handler) handleListCaddyServers(w http.ResponseWriter, r *http.Request) {
	if h.caddyManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "caddy manager not configured")
		return
	}
	servers, err := h.caddyManager.ListServers(r.Context())
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": servers})
}

// handleGetCaddyServer returns a single HTTP server by ID.
// GET /admin/caddy/servers/{id}
func (h *Handler) handleGetCaddyServer(w http.ResponseWriter, r *http.Request) {
	if h.caddyManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "caddy manager not configured")
		return
	}
	srv, err := h.caddyManager.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, caddymgr.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "server not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, srv)
}

// handleCreateCaddyServer creates a new HTTP server in Caddy.
// POST /admin/caddy/servers
func (h *Handler) handleCreateCaddyServer(w http.ResponseWriter, r *http.Request) {
	if h.caddyManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "caddy manager not configured")
		return
	}
	var req caddymgr.ServerRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.caddyManager.CreateServer(r.Context(), &req); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	srv, err := h.caddyManager.GetServer(r.Context(), req.ID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, srv)
}

// handleUpdateCaddyServer updates listen addresses and TLS config of an existing server.
// PUT /admin/caddy/servers/{id}
func (h *Handler) handleUpdateCaddyServer(w http.ResponseWriter, r *http.Request) {
	if h.caddyManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "caddy manager not configured")
		return
	}
	var req caddymgr.ServerRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ID = r.PathValue("id")

	if err := h.caddyManager.UpdateServer(r.Context(), &req); err != nil {
		if errors.Is(err, caddymgr.ErrReadOnly) {
			_ = httpjson.Error(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, caddymgr.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "server not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	srv, err := h.caddyManager.GetServer(r.Context(), req.ID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, srv)
}

// handleDeleteCaddyServer removes an HTTP server from Caddy.
// DELETE /admin/caddy/servers/{id}
func (h *Handler) handleDeleteCaddyServer(w http.ResponseWriter, r *http.Request) {
	if h.caddyManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "caddy manager not configured")
		return
	}
	id := r.PathValue("id")
	if err := h.caddyManager.DeleteServer(r.Context(), id); err != nil {
		if errors.Is(err, caddymgr.ErrReadOnly) {
			_ = httpjson.Error(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, caddymgr.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "server not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// ── Route handlers ────────────────────────────────────────────────────────────

// handleListCaddyRoutes lists all routes for a server.
// Routes defined in the Caddyfile are included but have an empty ID.
// GET /admin/caddy/servers/{id}/routes
func (h *Handler) handleListCaddyRoutes(w http.ResponseWriter, r *http.Request) {
	if h.caddyManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "caddy manager not configured")
		return
	}
	routes, err := h.caddyManager.ListRoutes(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, caddymgr.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "server not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": routes})
}

// handleAddCaddyRoute adds a route to a server.
// POST /admin/caddy/servers/{id}/routes
func (h *Handler) handleAddCaddyRoute(w http.ResponseWriter, r *http.Request) {
	if h.caddyManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "caddy manager not configured")
		return
	}
	serverID := r.PathValue("id")
	var req caddymgr.RouteRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.caddyManager.AddRoute(r.Context(), serverID, &req); err != nil {
		if errors.Is(err, caddymgr.ErrReadOnly) {
			_ = httpjson.Error(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, caddymgr.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "server not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	routes, err := h.caddyManager.ListRoutes(r.Context(), serverID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Return only the newly added route.
	for _, route := range routes {
		if route.ID == req.ID {
			_ = httpjson.Write(w, http.StatusCreated, route)
			return
		}
	}
	_ = httpjson.Write(w, http.StatusCreated, map[string]string{"id": req.ID})
}

// handleUpdateCaddyRoute updates an existing route's handler and match config.
// PUT /admin/caddy/servers/{id}/routes/{routeId}
func (h *Handler) handleUpdateCaddyRoute(w http.ResponseWriter, r *http.Request) {
	if h.caddyManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "caddy manager not configured")
		return
	}
	serverID := r.PathValue("id")
	routeID := r.PathValue("routeId")
	var req caddymgr.RouteRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ID = routeID

	if err := h.caddyManager.UpdateRoute(r.Context(), serverID, routeID, &req); err != nil {
		if errors.Is(err, caddymgr.ErrReadOnly) {
			_ = httpjson.Error(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, caddymgr.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	routes, err := h.caddyManager.ListRoutes(r.Context(), serverID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, route := range routes {
		if route.ID == routeID {
			_ = httpjson.Write(w, http.StatusOK, route)
			return
		}
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"id": routeID})
}

// handleDeleteCaddyRoute removes a route from a server.
// DELETE /admin/caddy/servers/{id}/routes/{routeId}
func (h *Handler) handleDeleteCaddyRoute(w http.ResponseWriter, r *http.Request) {
	if h.caddyManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "caddy manager not configured")
		return
	}
	serverID := r.PathValue("id")
	routeID := r.PathValue("routeId")
	if err := h.caddyManager.DeleteRoute(r.Context(), serverID, routeID); err != nil {
		if errors.Is(err, caddymgr.ErrReadOnly) {
			_ = httpjson.Error(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, caddymgr.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "id": routeID})
}
