package admin

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	dispatcherpkg "github.com/agent-guide/agent-gateway/pkg/dispatcher"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"gorm.io/gorm"
)

type ProviderView struct {
	provider.ProviderConfig
	PendingMutation *configmgr.PendingMutation `json:"pending_mutation,omitempty"`
	Source          string                     `json:"source"`
	ReadOnly        bool                       `json:"read_only"`
}

type ProviderTypeView struct {
	ProviderType string `json:"provider_type"`
	Enabled      bool   `json:"enabled"`
}

type LLMApiHandlerTypeView struct {
	LLMApiHandlerType string `json:"llm_api_handler_type"`
}

func (h *Handler) handleListProviders(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	items, err := manager.ListConfigs(r.Context(), gateway.ProviderListOptions{
		ProviderType: r.URL.Query().Get("provider_type"),
	})
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	providers := make([]ProviderView, 0, len(items))
	for _, cfg := range items {
		providers = append(providers, providerViewFromConfig(manager, cfg))
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": providers})
}

func (h *Handler) handleListProviderTypes(w http.ResponseWriter, r *http.Request) {
	names := provider.ListProviderTypes()
	items := make([]ProviderTypeView, 0, len(names))
	for _, name := range names {
		enabled, ok := provider.IsProviderTypeEnabled(name)
		if !ok {
			continue
		}
		items = append(items, ProviderTypeView{
			ProviderType: name,
			Enabled:      enabled,
		})
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleListLLMApiHandlerTypes(w http.ResponseWriter, r *http.Request) {
	types := dispatcherpkg.ListLLMApiHandlerTypes()
	items := make([]LLMApiHandlerTypeView, 0, len(types))
	for _, handlerType := range types {
		items = append(items, LLMApiHandlerTypeView{
			LLMApiHandlerType: handlerType,
		})
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	var cfg provider.ProviderConfig
	if err := httpjson.Decode(r, &cfg); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if cfg.Id == "" || cfg.ProviderType == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "id and provider_type are required")
		return
	}
	if err := manager.CreateConfig(r.Context(), cfg); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, err := manager.GetConfig(r.Context(), cfg.Id)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, providerViewFromConfig(manager, created))
}

func (h *Handler) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	id := r.PathValue("id")
	cfg, err := manager.GetConfig(r.Context(), id)
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, gateway.ErrProviderNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, providerViewFromConfig(manager, cfg))
}

func (h *Handler) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	var cfg provider.ProviderConfig
	if err := httpjson.Decode(r, &cfg); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if cfg.ProviderType == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "provider_type is required")
		return
	}

	id := r.PathValue("id")
	if cfg.Id != "" && cfg.Id != id {
		_ = httpjson.Error(w, http.StatusBadRequest, "body id must match path id")
		return
	}
	cfg.Id = id
	if err := manager.UpdateConfig(r.Context(), id, cfg); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, gateway.ErrProviderNotConfigured) || errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		if errors.Is(err, gateway.ErrStaticProviderReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	updatedCfg, err := manager.GetConfig(r.Context(), id)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, providerViewFromConfig(manager, updatedCfg))
}

func (h *Handler) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	id := r.PathValue("id")
	if err := manager.DeleteConfig(r.Context(), id); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, gateway.ErrStaticProviderReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, gateway.ErrProviderNotConfigured) || errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

func (h *Handler) handleEnableProvider(w http.ResponseWriter, r *http.Request) {
	h.handleSetProviderDisabled(w, r, false)
}

func (h *Handler) handleDisableProvider(w http.ResponseWriter, r *http.Request) {
	h.handleSetProviderDisabled(w, r, true)
}

func (h *Handler) handleSetProviderDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}

	id := r.PathValue("id")
	cfg, err := manager.GetConfig(r.Context(), id)
	if err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, gateway.ErrProviderNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		if errors.Is(err, gateway.ErrProviderDisabled) && !disabled {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.Disabled = disabled

	if err := manager.UpdateConfig(r.Context(), id, cfg); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		if errors.Is(err, gateway.ErrProviderNotConfigured) || errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "provider not found")
			return
		}
		if errors.Is(err, gateway.ErrStaticProviderReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	updatedCfg, err := manager.GetConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, gateway.ErrProviderDisabled) && disabled {
			_ = httpjson.Write(w, http.StatusOK, providerViewFromConfig(manager, cfg))
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, providerViewFromConfig(manager, updatedCfg))
}

func (h *Handler) providerStore() configstore.ConfigStore {
	if h.configStoreBackend == nil {
		return nil
	}
	providerConfigStore, err := h.configStoreBackend.Get(schema.StoreProviders)
	if err != nil {
		return nil
	}
	return providerConfigStore
}

func (h *Handler) providerManagerForRoutes() *gateway.ProviderManager {
	if h.providerManager != nil {
		return h.providerManager
	}

	store := h.providerStore()
	if store == nil {
		return nil
	}
	return gateway.NewProviderManager(store)
}

func providerViewFromConfig(manager *gateway.ProviderManager, cfg provider.ProviderConfig) ProviderView {
	view := ProviderView{
		ProviderConfig: cfg,
		Source:         "store",
		ReadOnly:       false,
	}
	if manager != nil {
		view.PendingMutation = manager.PendingMutation(cfg.Id)
	}
	if manager != nil && manager.IsStatic(cfg.Id) {
		view.Source = "caddyfile"
		view.ReadOnly = true
	}
	return view
}
