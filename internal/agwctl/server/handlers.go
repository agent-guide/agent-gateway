package server

import (
	"errors"
	"net/http"

	"github.com/agent-guide/caddy-agent-gateway/internal/agwctl/model"
)

func (s *Server) handleListCaddyServers(w http.ResponseWriter, r *http.Request) {
	servers, err := s.caddy.ListServers(r.Context())
	if err != nil {
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{"items": servers})
}

func (s *Server) handleGetCaddyServer(w http.ResponseWriter, r *http.Request) {
	srv, err := s.caddy.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			_ = writeError(w, http.StatusNotFound, "server not found")
			return
		}
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = writeJSON(w, http.StatusOK, srv)
}

func (s *Server) handleCreateCaddyServer(w http.ResponseWriter, r *http.Request) {
	var req model.ServerRequest
	if err := decodeJSON(r, &req); err != nil {
		_ = writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.caddy.CreateServer(r.Context(), &req); err != nil {
		if errors.Is(err, model.ErrReadOnly) {
			_ = writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, model.ErrConflict) {
			_ = writeError(w, http.StatusConflict, err.Error())
			return
		}
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	srv, err := s.caddy.GetServer(r.Context(), req.ID)
	if err != nil {
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = writeJSON(w, http.StatusCreated, srv)
}

func (s *Server) handleUpdateCaddyServer(w http.ResponseWriter, r *http.Request) {
	var req model.ServerRequest
	if err := decodeJSON(r, &req); err != nil {
		_ = writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ID = r.PathValue("id")
	if err := s.caddy.UpdateServer(r.Context(), &req); err != nil {
		if errors.Is(err, model.ErrReadOnly) {
			_ = writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, model.ErrNotFound) {
			_ = writeError(w, http.StatusNotFound, "server not found")
			return
		}
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	srv, err := s.caddy.GetServer(r.Context(), req.ID)
	if err != nil {
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = writeJSON(w, http.StatusOK, srv)
}

func (s *Server) handleDeleteCaddyServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.caddy.DeleteServer(r.Context(), id); err != nil {
		if errors.Is(err, model.ErrReadOnly) {
			_ = writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, model.ErrNotFound) {
			_ = writeError(w, http.StatusNotFound, "server not found")
			return
		}
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

func (s *Server) handleListCaddyRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := s.caddy.ListRoutes(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			_ = writeError(w, http.StatusNotFound, "server not found")
			return
		}
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]any{"items": routes})
}

func (s *Server) handleAddCaddyRoute(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	var req model.RouteRequest
	if err := decodeJSON(r, &req); err != nil {
		_ = writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.caddy.AddRoute(r.Context(), serverID, &req); err != nil {
		if errors.Is(err, model.ErrReadOnly) {
			_ = writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, model.ErrNotFound) {
			_ = writeError(w, http.StatusNotFound, "server not found")
			return
		}
		if errors.Is(err, model.ErrConflict) {
			_ = writeError(w, http.StatusConflict, err.Error())
			return
		}
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	routes, err := s.caddy.ListRoutes(r.Context(), serverID)
	if err != nil {
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, route := range routes {
		if route.ID == req.ID {
			_ = writeJSON(w, http.StatusCreated, route)
			return
		}
	}
	_ = writeJSON(w, http.StatusCreated, map[string]string{"id": req.ID})
}

func (s *Server) handleUpdateCaddyRoute(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	routeID := r.PathValue("routeId")
	var req model.RouteRequest
	if err := decodeJSON(r, &req); err != nil {
		_ = writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ID = routeID
	if err := s.caddy.UpdateRoute(r.Context(), serverID, routeID, &req); err != nil {
		if errors.Is(err, model.ErrReadOnly) {
			_ = writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, model.ErrNotFound) {
			_ = writeError(w, http.StatusNotFound, "route not found")
			return
		}
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	routes, err := s.caddy.ListRoutes(r.Context(), serverID)
	if err != nil {
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, route := range routes {
		if route.ID == routeID {
			_ = writeJSON(w, http.StatusOK, route)
			return
		}
	}
	_ = writeJSON(w, http.StatusOK, map[string]string{"id": routeID})
}

func (s *Server) handleDeleteCaddyRoute(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	routeID := r.PathValue("routeId")
	if err := s.caddy.DeleteRoute(r.Context(), serverID, routeID); err != nil {
		if errors.Is(err, model.ErrReadOnly) {
			_ = writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, model.ErrNotFound) {
			_ = writeError(w, http.StatusNotFound, "route not found")
			return
		}
		_ = writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": routeID})
}
