package admin

import (
	"context"
	"net/http"

	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"go.uber.org/zap"
)

func (h *Handler) contributedHandler(route extension.OwnedAdminRoute) http.HandlerFunc {
	bindPublisher := h.extensions.AdminPublisher(route.ModuleID)
	return func(w http.ResponseWriter, r *http.Request) {
		lease, err := h.extensions.Acquire()
		if err != nil {
			extensionError(w, err)
			return
		}
		defer lease.Release()
		resource, ok := lease.Snapshot.Resource(route.ModuleID)
		if !ok {
			extension.WriteAdminError(w, 503, "extension_unavailable")
			return
		}
		contributor, ok := resource.(extension.AdminContributor)
		if !ok {
			extension.WriteAdminError(w, 503, "extension_unavailable")
			return
		}
		config, _ := lease.Snapshot.Config(route.ModuleID)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stop := context.AfterFunc(lease.Snapshot.Context(), cancel)
		defer stop()
		deps := extension.AdminDependencies{
			Config: config, Logger: h.logger.With(zap.String("module_id", route.ModuleID)),
			Attribution: attribution.FromContext, Events: bindPublisher(ctx),
		}
		contributor.ServeAdminHTTP(w, r.WithContext(ctx), deps)
	}
}
