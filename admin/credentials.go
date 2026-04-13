package admin

import (
	"net/http"
	"strings"

	"github.com/agent-guide/caddy-agent-gateway/internal/utils"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
)

func (h *Handler) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	if h.credentialManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "credential manager not configured")
		return
	}

	filter := credentialmgr.Filter{
		Provider: r.URL.Query().Get("provider"),
		Source:   r.URL.Query().Get("source"),
	}
	items := h.credentialManager.ListCredentials(filter)
	_ = utils.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGetCredential(w http.ResponseWriter, r *http.Request) {
	if h.credentialManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "credential manager not configured")
		return
	}

	id := strings.TrimSpace(r.PathValue("credential_id"))
	if id == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "credential_id is required")
		return
	}

	item := h.credentialManager.GetCredential(id)
	if item == nil {
		_ = utils.WriteError(w, http.StatusNotFound, "credential not found")
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	if h.credentialManager == nil {
		_ = utils.WriteError(w, http.StatusServiceUnavailable, "credential manager not configured")
		return
	}

	id := strings.TrimSpace(r.PathValue("credential_id"))
	if id == "" {
		_ = utils.WriteError(w, http.StatusBadRequest, "credential_id is required")
		return
	}
	if err := h.credentialManager.DeregisterCredential(r.Context(), id); err != nil {
		_ = utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted", "credential_id": id})
}
