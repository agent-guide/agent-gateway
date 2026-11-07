package admin

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
)

func writeControlPlaneError(w http.ResponseWriter, err error) bool {
	status := 0
	switch {
	case errors.Is(err, configmgr.ErrMutationPending):
		status = http.StatusServiceUnavailable
	case errors.Is(err, gateway.ErrResourceReferenced):
		status = http.StatusConflict
	case errors.Is(err, gateway.ErrDependencyNotConfigured):
		status = http.StatusBadRequest
	case errors.Is(err, configstore.ErrTransactionsUnsupported):
		status = http.StatusServiceUnavailable
	}
	if status == 0 {
		return false
	}
	var pending *configmgr.PendingMutationError
	if errors.As(err, &pending) && !pending.Mutation.RetryAfter.IsZero() {
		seconds := max(1, int(time.Until(pending.Mutation.RetryAfter).Seconds()+0.999))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	_ = httpjson.Error(w, status, err.Error())
	return true
}

func (h *Handler) handleProviderMutationStatus(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}
	id := r.PathValue("id")
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"id": id, "pending_mutation": manager.PendingMutation(id)})
}

func (h *Handler) handleRecoverProvider(w http.ResponseWriter, r *http.Request) {
	manager := h.providerManagerForRoutes()
	if manager == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "provider manager is not configured")
		return
	}
	id := r.PathValue("id")
	if err := manager.Recover(r.Context(), id); err != nil {
		if writeControlPlaneError(w, err) {
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"id": id, "status": "recovered"})
}
