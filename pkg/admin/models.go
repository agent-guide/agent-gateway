package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

type ManagedConcreteModelView struct {
	modelcatalog.ManagedModel
	ProviderType  string                      `json:"provider_type,omitempty"`
	DisplayName   string                      `json:"display_name,omitempty"`
	Description   string                      `json:"description,omitempty"`
	Capabilities  provider.ModelCapabilities  `json:"capabilities,omitempty"`
	SnapshotState modelcatalog.SnapshotStatus `json:"snapshot_status,omitempty"`
	FetchedAt     time.Time                   `json:"fetched_at,omitempty"`
	LastError     string                      `json:"last_error,omitempty"`
}

func (h *Handler) handleListDiscoveredModels(w http.ResponseWriter, r *http.Request) {
	if h.modelCatalog == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "model catalog is not configured")
		return
	}
	items, err := h.modelCatalog.ListProviderSnapshots(r.Context(), strings.TrimSpace(r.PathValue("provider_id")))
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleRefreshProviderModels(w http.ResponseWriter, r *http.Request) {
	if h.modelCatalog == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "model catalog is not configured")
		return
	}
	providerID := strings.TrimSpace(r.PathValue("provider_id"))
	if err := h.modelCatalog.RefreshProvider(r.Context(), providerID); err != nil {
		_ = httpjson.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	items, err := h.modelCatalog.ListProviderSnapshots(r.Context(), providerID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"provider_id": providerID, "items": items})
}

func (h *Handler) handleListManagedModels(w http.ResponseWriter, r *http.Request) {
	if h.modelCatalog == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "model catalog is not configured")
		return
	}
	items, err := h.modelCatalog.ListManagedModels(r.Context(), modelcatalog.ManagedModelFilter{
		ProviderID: strings.TrimSpace(r.URL.Query().Get("provider_id")),
	})
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]ManagedConcreteModelView, 0, len(items))
	for _, item := range items {
		view, err := h.managedModelView(r, item)
		if err != nil {
			_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
		views = append(views, view)
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": views})
}

func (h *Handler) handleGetManagedModel(w http.ResponseWriter, r *http.Request) {
	if h.modelCatalog == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "model catalog is not configured")
		return
	}
	item, ok, err := h.modelCatalog.GetManagedModel(r.Context(), r.PathValue("provider_id"), r.PathValue("upstream_model"))
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		_ = httpjson.Error(w, http.StatusNotFound, "managed model not found")
		return
	}
	view, err := h.managedModelView(r, *item)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, view)
}

func (h *Handler) handleCreateManagedModel(w http.ResponseWriter, r *http.Request) {
	if h.modelCatalog == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "model catalog is not configured")
		return
	}
	var item modelcatalog.ManagedModel
	if err := httpjson.Decode(r, &item); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	item.Normalize()
	if err := h.modelCatalog.CreateManagedModel(r.Context(), item); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	viewModel, ok, err := h.modelCatalog.GetManagedModel(r.Context(), item.ProviderID, item.UpstreamModel)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		_ = httpjson.Error(w, http.StatusNotFound, "managed model not found")
		return
	}
	view, err := h.managedModelView(r, *viewModel)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, view)
}

func (h *Handler) handleUpdateManagedModel(w http.ResponseWriter, r *http.Request) {
	if h.modelCatalog == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "model catalog is not configured")
		return
	}
	var item modelcatalog.ManagedModel
	if err := httpjson.Decode(r, &item); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	item.ProviderID = r.PathValue("provider_id")
	item.UpstreamModel = r.PathValue("upstream_model")
	item.Normalize()
	if err := h.modelCatalog.UpdateManagedModel(r.Context(), item); err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "managed model not found")
			return
		}
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	viewModel, ok, err := h.modelCatalog.GetManagedModel(r.Context(), item.ProviderID, item.UpstreamModel)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		_ = httpjson.Error(w, http.StatusNotFound, "managed model not found")
		return
	}
	view, err := h.managedModelView(r, *viewModel)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, view)
}

func (h *Handler) handleDeleteManagedModel(w http.ResponseWriter, r *http.Request) {
	if h.modelCatalog == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "model catalog is not configured")
		return
	}
	if err := h.modelCatalog.DeleteManagedModel(r.Context(), r.PathValue("provider_id"), r.PathValue("upstream_model")); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) managedModelView(r *http.Request, item modelcatalog.ManagedModel) (ManagedConcreteModelView, error) {
	view := ManagedConcreteModelView{ManagedModel: item}

	resolved, ok, err := h.modelCatalog.GetResolvedManagedModel(r.Context(), item.ProviderID, item.UpstreamModel)
	if err != nil {
		return ManagedConcreteModelView{}, err
	}
	if ok {
		view.Capabilities = resolved.Capabilities
		if resolved.Snapshot != nil {
			view.ProviderType = resolved.Snapshot.ProviderType
			view.DisplayName = resolved.Snapshot.DisplayName
			view.Description = resolved.Snapshot.Description
			view.SnapshotState = resolved.Snapshot.Status
			view.FetchedAt = resolved.Snapshot.FetchedAt
			view.LastError = resolved.Snapshot.LastError
		}
	}
	if view.ProviderType == "" && h.providerManager != nil {
		cfg, err := h.providerManager.GetConfig(r.Context(), item.ProviderID)
		if err == nil {
			view.ProviderType = cfg.ProviderType
		}
	}
	return view, nil
}
