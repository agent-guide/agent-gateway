package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"gorm.io/gorm"
)

type VirtualKeyView struct {
	virtualkeypkg.VirtualKey
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}

func (h *Handler) handleListVirtualKeys(w http.ResponseWriter, r *http.Request) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	tag := r.URL.Query().Get("tag")
	items, err := manager.List(r.Context(), virtualkeypkg.VirtualKeyListOptions{Tag: tag})
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]VirtualKeyView, 0, len(items))
	for _, item := range items {
		views = append(views, virtualKeyViewFromKey(item))
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": views})
}

func (h *Handler) handleCreateVirtualKey(w http.ResponseWriter, r *http.Request) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	var key virtualkeypkg.VirtualKey
	if err := httpjson.Decode(r, &key); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	generatedKey, err := virtualkeypkg.GenerateKey()
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, "failed to generate virtual key")
		return
	}
	key.Key = generatedKey
	if strings.TrimSpace(key.ID) == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "virtual key id is required")
		return
	}
	if !key.CreatedAt.IsZero() || !key.UpdatedAt.IsZero() {
		_ = httpjson.Error(w, http.StatusBadRequest, "created_at and updated_at are managed by the server and must be omitted")
		return
	}
	if err := key.ValidateConfiguration(); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := manager.Create(r.Context(), key); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, err := manager.GetByID(r.Context(), key.ID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, created)
}

func (h *Handler) handleGetVirtualKey(w http.ResponseWriter, r *http.Request) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	item, err := manager.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, virtualkeypkg.ErrVirtualKeyNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "virtual key not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, virtualKeyViewFromKey(item))
}

func (h *Handler) handleUpdateVirtualKey(w http.ResponseWriter, r *http.Request) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	var key virtualkeypkg.VirtualKey
	if err := httpjson.Decode(r, &key); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	pathID := r.PathValue("id")
	if key.ID == "" {
		key.ID = pathID
	}
	if key.ID != pathID {
		_ = httpjson.Error(w, http.StatusBadRequest, "virtual key id in body must match path")
		return
	}
	if !key.CreatedAt.IsZero() || !key.UpdatedAt.IsZero() {
		_ = httpjson.Error(w, http.StatusBadRequest, "created_at and updated_at are managed by the server and must be omitted")
		return
	}
	if key.Key != "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "virtual key key is generated and must be omitted")
		return
	}
	if err := key.ValidateConfiguration(); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if _, err := manager.GetByID(r.Context(), pathID); err != nil {
		if errors.Is(err, virtualkeypkg.ErrVirtualKeyNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "virtual key not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := manager.Update(r.Context(), key.ID, key); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := manager.GetByID(r.Context(), key.ID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, updated)
}

func (h *Handler) handleDeleteVirtualKey(w http.ResponseWriter, r *http.Request) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	id := r.PathValue("id")
	if err := manager.Delete(r.Context(), id); err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

func (h *Handler) handleEnableVirtualKey(w http.ResponseWriter, r *http.Request) {
	h.handleSetVirtualKeyDisabled(w, r, false)
}

func (h *Handler) handleDisableVirtualKey(w http.ResponseWriter, r *http.Request) {
	h.handleSetVirtualKeyDisabled(w, r, true)
}

func (h *Handler) handleSetVirtualKeyDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	manager := h.virtualKeyManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "virtual key manager not configured")
		return
	}

	keyID := r.PathValue("id")
	key, err := manager.GetByID(r.Context(), keyID)
	if err != nil {
		if errors.Is(err, virtualkeypkg.ErrVirtualKeyNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "virtual key not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	key.Disabled = disabled

	if err := manager.Update(r.Context(), keyID, key); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "virtual key not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated, err := manager.GetByID(r.Context(), keyID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, virtualKeyViewFromKey(updated))
}

func (h *Handler) virtualKeyStore() configstore.ConfigStore {
	if h.configStoreBackend == nil {
		return nil
	}
	store, err := h.configStoreBackend.Get(schema.StoreVirtualKeys)
	if err != nil {
		return nil
	}
	return store
}

func (h *Handler) virtualKeyManagerForRoutes() *virtualkeypkg.VirtualKeyManager {
	if h.virtualKeyManager != nil {
		return h.virtualKeyManager
	}

	store := h.virtualKeyStore()
	if store == nil {
		return nil
	}
	return virtualkeypkg.NewVirtualKeyManager(store)
}

func virtualKeyViewFromKey(key virtualkeypkg.VirtualKey) VirtualKeyView {
	return VirtualKeyView{
		VirtualKey: key,
		Source:     "store",
		ReadOnly:   false,
	}
}
