package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/caddy-agent-gateway/gateway"
	"github.com/agent-guide/caddy-agent-gateway/internal/httpjson"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"gorm.io/gorm"
)

type CredentialView struct {
	credentialmgr.ManagedCredential
	ReadOnly bool `json:"read_only"`
}

func (h *Handler) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	if h.credentialManager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "credential manager not configured")
		return
	}

	filter := credentialmgr.Filter{
		ProviderType: r.URL.Query().Get("provider_type"),
		ProviderID:   r.URL.Query().Get("provider_id"),
		Source:       r.URL.Query().Get("source"),
	}
	items := h.credentialManager.ListCredentials(filter)
	views := make([]CredentialView, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		views = append(views, credentialView(item, false))
	}
	for _, item := range h.listProviderStaticCredentials(r.Context(), filter) {
		views = append(views, credentialViewFromSpec(item, true))
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": views})
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
	if item != nil {
		_ = httpjson.Write(w, http.StatusOK, credentialView(item, false))
		return
	}
	spec := h.getProviderStaticCredential(r.Context(), id)
	if spec == nil {
		_ = httpjson.Error(w, http.StatusNotFound, "credential not found")
		return
	}
	_ = httpjson.Write(w, http.StatusOK, credentialViewFromSpec(spec, true))
}

// credentialCreateRequest is the request body for POST /admin/credentials.
type credentialCreateRequest struct {
	ProviderID string            `json:"provider_id"`
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
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	var req credentialCreateRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	providerID := strings.TrimSpace(req.ProviderID)
	if providerID == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "provider_id is required")
		return
	}

	cfg, err := manager.GetConfig(r.Context(), providerID)
	if err != nil {
		if errors.Is(err, gateway.ErrProviderNotConfigured) || errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	cred := &credentialmgr.Credential{
		ProviderType: cfg.ProviderType,
		ProviderID:   cfg.Id,
		Source:       credentialmgr.SourceAPIKey,
		Label:        req.Label,
		Attributes:   req.Attributes,
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
		if h.getProviderStaticCredential(r.Context(), id) != nil {
			_ = httpjson.Error(w, http.StatusForbidden, "provider config api_key credentials are read-only")
			return
		}
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

	updated := existing.Credential.Clone()
	updated.Label = req.Label
	updated.Disabled = req.Disabled
	if req.Attributes != nil {
		updated.Attributes = req.Attributes
	}

	if err := h.credentialManager.UpdateCredential(r.Context(), updated); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, h.credentialManager.GetCredential(id))
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
	if h.getProviderStaticCredential(r.Context(), id) != nil {
		_ = httpjson.Error(w, http.StatusForbidden, "provider config api_key credentials are read-only")
		return
	}
	if err := h.credentialManager.DeregisterCredential(r.Context(), id); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "credential_id": id})
}

func credentialView(cred *credentialmgr.ManagedCredential, readOnly bool) CredentialView {
	if cred == nil {
		return CredentialView{ReadOnly: readOnly}
	}
	return CredentialView{
		ManagedCredential: *cred.Clone(),
		ReadOnly:          readOnly,
	}
}

func credentialViewFromSpec(cred *credentialmgr.Credential, readOnly bool) CredentialView {
	if cred == nil {
		return CredentialView{ReadOnly: readOnly}
	}
	return CredentialView{
		ManagedCredential: credentialmgr.ManagedCredential{Credential: *cred.Clone()},
		ReadOnly:          readOnly,
	}
}

func (h *Handler) listProviderStaticCredentials(ctx context.Context, filter credentialmgr.Filter) []*credentialmgr.Credential {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		return nil
	}
	items, err := manager.ListConfigs(ctx, gateway.ProviderListOptions{
		ProviderType: filter.ProviderType,
	})
	if err != nil {
		return nil
	}
	out := make([]*credentialmgr.Credential, 0, len(items))
	for _, cfg := range items {
		cred := provider.StaticAPIKeyCredential(cfg, cfg.Id)
		if cred == nil || !matchesCredentialFilter(cred, filter) {
			continue
		}
		out = append(out, cred)
	}
	return out
}

func (h *Handler) getProviderStaticCredential(ctx context.Context, id string) *credentialmgr.Credential {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	for _, item := range h.listProviderStaticCredentials(ctx, credentialmgr.Filter{}) {
		if item != nil && item.ID == id {
			return item
		}
	}
	return nil
}

func matchesCredentialFilter(cred *credentialmgr.Credential, filter credentialmgr.Filter) bool {
	if cred == nil {
		return false
	}
	if providerType := strings.ToLower(strings.TrimSpace(filter.ProviderType)); providerType != "" && strings.ToLower(cred.ProviderType) != providerType {
		return false
	}
	if providerID := strings.ToLower(strings.TrimSpace(filter.ProviderID)); providerID != "" && strings.ToLower(cred.ProviderID) != providerID {
		return false
	}
	if source := strings.ToLower(strings.TrimSpace(filter.Source)); source != "" && strings.ToLower(cred.Source) != source {
		return false
	}
	return true
}
