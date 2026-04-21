package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
)

func (h *Handler) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	if h.credentialManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "credential manager not configured")
		return
	}

	filter := credentialmgr.Filter{
		Provider: r.URL.Query().Get("provider"),
		Source:   r.URL.Query().Get("source"),
	}
	items := h.credentialManager.ListCredentials(filter)
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGetCredential(w http.ResponseWriter, r *http.Request) {
	if h.credentialManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "credential manager not configured")
		return
	}

	id := strings.TrimSpace(r.PathValue("credential_id"))
	if id == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "credential_id is required")
		return
	}

	item := h.credentialManager.GetCredential(id)
	if item == nil {
		_ = httpjson.Error(w, http.StatusNotFound, "credential not found")
		return
	}
	_ = httpjson.Write(w, http.StatusOK, item)
}

// credentialCreateRequest is the request body for POST /admin/credentials.
type credentialCreateRequest struct {
	Provider   string            `json:"provider"`
	Label      string            `json:"label,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// credentialUpdateRequest is the request body for PUT /admin/credentials/{credential_id}.
type credentialUpdateRequest struct {
	Label      string            `json:"label,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Disabled   bool              `json:"disabled,omitempty"`
}

func (h *Handler) handleCreateCredential(w http.ResponseWriter, r *http.Request) {
	if h.credentialManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "credential manager not configured")
		return
	}

	var req credentialCreateRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if strings.TrimSpace(req.Provider) == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "provider is required")
		return
	}

	cred := &credentialmgr.Credential{
		Provider:   strings.TrimSpace(req.Provider),
		Source:     credentialmgr.SourceAPIKey,
		Label:      req.Label,
		Attributes: req.Attributes,
	}
	if err := h.credentialManager.RegisterCredential(r.Context(), cred); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, cred)
}

func (h *Handler) handleUpdateCredential(w http.ResponseWriter, r *http.Request) {
	if h.credentialManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "credential manager not configured")
		return
	}

	id := strings.TrimSpace(r.PathValue("credential_id"))
	if id == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "credential_id is required")
		return
	}

	existing := h.credentialManager.GetCredential(id)
	if existing == nil {
		_ = httpjson.Error(w, http.StatusNotFound, "credential not found")
		return
	}
	if existing.Source != credentialmgr.SourceAPIKey {
		_ = httpjson.Error(w, http.StatusForbidden, "only api_key credentials can be updated via the admin API")
		return
	}

	var req credentialUpdateRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	existing.Label = req.Label
	existing.Disabled = req.Disabled
	if req.Attributes != nil {
		existing.Attributes = req.Attributes
	}

	if err := h.credentialManager.UpdateCredential(r.Context(), existing); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, existing)
}

func (h *Handler) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	if h.credentialManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "credential manager not configured")
		return
	}

	id := strings.TrimSpace(r.PathValue("credential_id"))
	if id == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "credential_id is required")
		return
	}
	if err := h.credentialManager.DeregisterCredential(r.Context(), id); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "credential_id": id})
}
