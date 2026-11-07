package admin

import (
	"errors"
	"net/http"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
)

func writeControlPlaneError(w http.ResponseWriter, err error) bool {
	status := 0
	switch {
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
	_ = httpjson.Error(w, status, err.Error())
	return true
}
