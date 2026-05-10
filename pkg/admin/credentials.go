package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
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
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		readOnly := h.getProviderStaticCredential(r.Context(), item.ID) != nil
		views = append(views, credentialView(item, readOnly))
		seen[item.ID] = struct{}{}
	}
	for _, item := range h.listProviderStaticCredentials(r.Context(), filter) {
		if item == nil {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
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

	if spec := h.getProviderStaticCredential(r.Context(), id); spec != nil {
		_ = httpjson.Write(w, http.StatusOK, credentialViewFromSpec(spec, true))
		return
	}

	item := h.credentialManager.GetCredential(id)
	if item != nil {
		_ = httpjson.Write(w, http.StatusOK, credentialView(item, false))
		return
	}
	_ = httpjson.Error(w, http.StatusNotFound, "credential not found")
}

// credentialCreateRequest is the request body for POST /admin/credentials.
type credentialCreateRequest struct {
	ID           string            `json:"id,omitempty"`
	Source       string            `json:"source,omitempty"`
	ProviderType string            `json:"provider_type,omitempty"`
	ProviderID   string            `json:"provider_id"`
	Label        string            `json:"label,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
	Metadata     map[string]any    `json:"metadata,omitempty"`
	Disabled     bool              `json:"disabled,omitempty"`
	CreatedAt    any               `json:"created_at,omitempty"`
	UpdatedAt    any               `json:"updated_at,omitempty"`
}

// credentialUpdateRequest is the request body for PUT /admin/credentials/{credential_id}.
type credentialUpdateRequest struct {
	Source       string            `json:"source,omitempty"`
	ProviderType string            `json:"provider_type,omitempty"`
	ProviderID   string            `json:"provider_id,omitempty"`
	Label        string            `json:"label,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
	Metadata     map[string]any    `json:"metadata,omitempty"`
	Disabled     bool              `json:"disabled,omitempty"`
	CreatedAt    any               `json:"created_at,omitempty"`
	UpdatedAt    any               `json:"updated_at,omitempty"`
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
	cred, err := h.buildCredentialForCreate(r.Context(), req)
	if err != nil {
		h.writeCredentialBuildError(w, err)
		return
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
	if h.getProviderStaticCredential(r.Context(), id) != nil {
		_ = httpjson.Error(w, http.StatusForbidden, "provider config api_key credentials are read-only")
		return
	}

	existing := h.credentialManager.GetCredential(id)
	if existing == nil {
		_ = httpjson.Error(w, http.StatusNotFound, "credential not found")
		return
	}

	var req credentialUpdateRequest
	if err := httpjson.Decode(r, &req); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	updated, err := h.buildCredentialForUpdate(existing, req)
	if err != nil {
		h.writeCredentialBuildError(w, err)
		return
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
		cred := provider.ProviderConfigAPIKeyCredential(cfg, cfg.Id)
		if cred == nil || !matchesCredentialFilter(cred, filter) {
			continue
		}
		out = append(out, cred)
	}
	return out
}

func (h *Handler) getProviderStaticCredential(ctx context.Context, id string) *credentialmgr.Credential {
	providerID, ok := provider.ProviderConfigAPIKeyCredentialProviderID(id)
	if !ok {
		return nil
	}
	manager := h.providerManagerForRoutes()
	if manager == nil {
		return nil
	}
	cfg, err := manager.GetConfig(ctx, providerID)
	if err != nil {
		return nil
	}
	return provider.ProviderConfigAPIKeyCredential(cfg, cfg.Id)
}

func (h *Handler) buildCredentialForCreate(ctx context.Context, req credentialCreateRequest) (*credentialmgr.Credential, error) {
	source := strings.ToLower(strings.TrimSpace(req.Source))
	if source == "" {
		source = credentialmgr.SourceAPIKey
	}

	switch source {
	case credentialmgr.SourceAPIKey:
		manager := h.providerManagerForRoutes()
		if manager == nil {
			return nil, fmt.Errorf("provider manager is not configured")
		}
		providerID := strings.TrimSpace(req.ProviderID)
		if providerID == "" {
			return nil, fmt.Errorf("provider_id is required")
		}
		cfg, err := manager.GetConfig(ctx, providerID)
		if err != nil {
			if errors.Is(err, gateway.ErrProviderNotConfigured) || errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			return nil, fmt.Errorf("get provider config: %w", err)
		}
		return &credentialmgr.Credential{
			ID:           strings.TrimSpace(req.ID),
			ProviderType: cfg.ProviderType,
			ProviderID:   cfg.Id,
			Source:       credentialmgr.SourceAPIKey,
			Label:        req.Label,
			Attributes:   req.Attributes,
			Disabled:     req.Disabled,
		}, nil
	case credentialmgr.SourceCLIAuthToken:
		cred := &credentialmgr.Credential{
			ID:           strings.TrimSpace(req.ID),
			ProviderType: strings.TrimSpace(req.ProviderType),
			ProviderID:   strings.TrimSpace(req.ProviderID),
			Source:       credentialmgr.SourceCLIAuthToken,
			Label:        req.Label,
			Attributes:   req.Attributes,
			Metadata:     req.Metadata,
			Disabled:     req.Disabled,
		}
		if cred.ProviderType == "" {
			return nil, fmt.Errorf("provider_type is required")
		}
		if cred.ProviderID == "" {
			cred.ProviderID = cred.ProviderType
		}
		return cred, nil
	default:
		return nil, fmt.Errorf("unsupported credential source %q", source)
	}
}

func (h *Handler) buildCredentialForUpdate(existing *credentialmgr.ManagedCredential, req credentialUpdateRequest) (*credentialmgr.Credential, error) {
	if existing == nil {
		return nil, fmt.Errorf("credential not found")
	}
	switch existing.Source {
	case credentialmgr.SourceAPIKey:
		updated := existing.Credential.Clone()
		updated.Label = req.Label
		updated.Disabled = req.Disabled
		if req.Attributes != nil {
			updated.Attributes = req.Attributes
		}
		return updated, nil
	case credentialmgr.SourceCLIAuthToken:
		updated := existing.Credential.Clone()
		updated.Label = req.Label
		updated.Disabled = req.Disabled
		if req.Attributes != nil {
			updated.Attributes = req.Attributes
		}
		if req.Metadata != nil {
			updated.Metadata = req.Metadata
		}
		if providerType := strings.TrimSpace(req.ProviderType); providerType != "" {
			updated.ProviderType = providerType
		}
		if providerID := strings.TrimSpace(req.ProviderID); providerID != "" {
			updated.ProviderID = providerID
		} else if strings.TrimSpace(updated.ProviderID) == "" {
			updated.ProviderID = updated.ProviderType
		}
		return updated, nil
	default:
		return nil, fmt.Errorf("credential source %q cannot be updated via the admin API", existing.Source)
	}
}

func (h *Handler) writeCredentialBuildError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, gateway.ErrProviderNotConfigured), errors.Is(err, gorm.ErrRecordNotFound):
		status = http.StatusNotFound
		msg = "provider not found"
	case strings.Contains(msg, "provider manager is not configured"):
		status = http.StatusServiceUnavailable
	case strings.Contains(msg, "cannot be updated via the admin API"):
		status = http.StatusForbidden
	}
	_ = httpjson.Error(w, status, msg)
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
