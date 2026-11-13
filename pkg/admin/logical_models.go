package admin

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
)

func (h *Handler) handleLogicalModels(w http.ResponseWriter, r *http.Request) {
	if h.modelCatalog == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "model catalog is not configured")
		return
	}
	id := r.PathValue("id")
	var value any
	var err error
	status := http.StatusOK
	switch r.Method {
	case http.MethodGet:
		if id == "" {
			items, e := h.modelCatalog.ListLogicalModels(r.Context())
			err = e
			value = map[string]any{"items": items}
		} else {
			var ok bool
			value, ok, err = h.modelCatalog.GetLogicalModel(r.Context(), id)
			if err == nil && !ok {
				err = configstore.ErrNotFound
			}
		}
	case http.MethodPost, http.MethodPut:
		var m modelcatalog.LogicalModel
		if err = httpjson.Decode(r, &m); err == nil {
			if r.Method == http.MethodPost {
				m.Normalize()
			}
			if r.Method == http.MethodPut {
				if m.ID != "" && m.ID != id {
					err = fmt.Errorf("body id must match path id")
				} else {
					m.ID = id
					err = h.modelCatalog.UpdateLogicalModel(r.Context(), m)
				}
			} else {
				err = h.modelCatalog.CreateLogicalModel(r.Context(), m)
				status = http.StatusCreated
			}
			if err == nil {
				value, _, err = h.modelCatalog.GetLogicalModel(r.Context(), m.ID)
			}
		}
	case http.MethodDelete:
		err = h.modelCatalog.DeleteLogicalModel(r.Context(), id)
		value = map[string]string{"status": "deleted"}
	}
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		status = http.StatusBadRequest
		if errors.Is(err, configstore.ErrNotFound) {
			status = http.StatusNotFound
		}
		_ = httpjson.Error(w, status, err.Error())
		return
	}
	_ = httpjson.Write(w, status, value)
}
