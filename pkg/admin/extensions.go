package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"io"
	"net/http"
	"regexp"
)

func extensionJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

var extensionDiagnostic = regexp.MustCompile(`^[A-Za-z0-9_./~-]{0,128}$`)

func extensionError(w http.ResponseWriter, err error) {
	var e *extension.Error
	status := http.StatusBadRequest
	if !errors.As(err, &e) {
		e = &extension.Error{Code: "extension_unavailable"}
		status = http.StatusServiceUnavailable
	}
	owned := *e
	owned.ModuleID = safeExtensionID(owned.ModuleID)
	if !extensionDiagnostic.MatchString(owned.Field) {
		owned.Field = "config"
	}
	if !extensionDiagnostic.MatchString(owned.Code) {
		owned.Code = "extension_unavailable"
	}
	e = &owned
	switch e.Code {
	case "not_found":
		status = http.StatusNotFound
	case "static_config", "missing_dependency", "dependency_cycle":
		status = http.StatusConflict
	case "prepare_failed", "unhealthy", "secret_unavailable", "persistence_failed", "closed":
		status = http.StatusServiceUnavailable
	}
	extensionJSON(w, status, map[string]any{"error": e.Code, "code": e.Code, "module_id": e.ModuleID, "field": e.Field})
}
func (h *Handler) handleManifest(w http.ResponseWriter, r *http.Request) {
	if h.gateway == nil {
		extensionError(w, errors.New("unavailable"))
		return
	}
	m, err := h.gateway.DistributionManifest()
	if err != nil {
		extensionError(w, err)
		return
	}
	extensionJSON(w, http.StatusOK, m)
}
func (h *Handler) handleListExtensions(w http.ResponseWriter, r *http.Request) {
	if h.extensions == nil {
		extensionError(w, errors.New("unavailable"))
		return
	}
	extensionJSON(w, http.StatusOK, map[string]any{"items": h.extensions.List()})
}
func (h *Handler) handleGetExtension(w http.ResponseWriter, r *http.Request) {
	if h.extensions == nil {
		extensionError(w, errors.New("unavailable"))
		return
	}
	id := r.PathValue("module_id")
	for _, e := range h.extensions.List() {
		if e.ModuleID == id {
			extensionJSON(w, http.StatusOK, e)
			return
		}
	}
	extensionError(w, &extension.Error{Code: "not_found", ModuleID: safeExtensionID(id)})
}
func safeExtensionID(id string) string {
	if extension.ValidID(id) {
		return id
	}
	return ""
}
func (h *Handler) handlePutExtension(w http.ResponseWriter, r *http.Request) {
	if h.extensions == nil {
		extensionError(w, errors.New("unavailable"))
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		extensionError(w, &extension.Error{Code: "invalid_config"})
		return
	}
	var e extension.Envelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&e); err != nil {
		extensionError(w, &extension.Error{Code: "invalid_config"})
		return
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		extensionError(w, &extension.Error{Code: "invalid_config"})
		return
	}
	if e.ModuleID != r.PathValue("module_id") {
		extensionError(w, &extension.Error{Code: "invalid_config", Field: "module_id"})
		return
	}
	if err = h.extensions.Put(r.Context(), e); err != nil {
		extensionError(w, err)
		return
	}
	h.handleGetExtension(w, r)
}
func (h *Handler) handleDeleteExtension(w http.ResponseWriter, r *http.Request) {
	if h.extensions == nil {
		extensionError(w, errors.New("unavailable"))
		return
	}
	id := r.PathValue("module_id")
	if err := h.extensions.Delete(r.Context(), id); err != nil {
		extensionError(w, err)
		return
	}
	extensionJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}
