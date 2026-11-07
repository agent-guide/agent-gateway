package admin

import (
	"net/http"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
)

func (h *Handler) handleGetMemoryConfig(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}

func (h *Handler) handleSetMemoryConfig(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}

func (h *Handler) handleSearchMemory(w http.ResponseWriter, r *http.Request) {
	_ = httpjson.Error(w, http.StatusNotImplemented, "not implemented")
}
