package admin

import (
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

func (h *Handler) routeStore() configstore.ConfigStore {
	if h.configStoreBackend == nil {
		return nil
	}
	store, err := h.configStoreBackend.Get(schema.StoreRoutes)
	if err != nil {
		return nil
	}
	return store
}

func (h *Handler) routeConfigManagerForRoutes() *routecore.RouteConfigManager {
	if h.routeConfigManager != nil {
		return h.routeConfigManager
	}

	store := h.routeStore()
	if store == nil {
		return nil
	}
	return routecore.NewRouteConfigManager(store)
}

func filterRouteConfigsByKind(items []routecore.RouteConfig, kind routecore.RouteKind) []routecore.RouteConfig {
	filtered := make([]routecore.RouteConfig, 0, len(items))
	for _, item := range items {
		if item.Kind == kind {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
