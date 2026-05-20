package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	llmroutepkg "github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"gorm.io/gorm"
)

const defaultLLMRouteTag = ""

type LLMRouteView struct {
	ID           string                       `json:"id"`
	Kind         llmroutepkg.RouteKind        `json:"kind,omitempty"`
	Protocol     llmroutepkg.RouteProtocol    `json:"protocol,omitempty"`
	Description  string                       `json:"description,omitempty"`
	Disabled     bool                         `json:"disabled"`
	MatchPolicy  llmroutepkg.RouteMatchPolicy `json:"match_policy"`
	TargetPolicy json.RawMessage              `json:"target_policy"`
	AuthPolicy   llmroutepkg.RouteAuthPolicy  `json:"auth_policy"`
	CreatedAt    time.Time                    `json:"created_at"`
	UpdatedAt    time.Time                    `json:"updated_at"`
	Source       string                       `json:"source"`
	ReadOnly     bool                         `json:"read_only"`
}

func (v LLMRouteView) LLMRouteConfig() (llmroutepkg.LLMRouteConfig, error) {
	return llmroutepkg.NewLLMRouteConfigFromConfig(llmroutepkg.AgentRouteConfig{
		ID:           v.ID,
		Kind:         v.Kind,
		Protocol:     v.Protocol,
		Description:  v.Description,
		Disabled:     v.Disabled,
		MatchPolicy:  v.MatchPolicy,
		TargetPolicy: append(json.RawMessage(nil), v.TargetPolicy...),
		AuthPolicy:   v.AuthPolicy,
		CreatedAt:    v.CreatedAt,
		UpdatedAt:    v.UpdatedAt,
	})
}

func (h *Handler) handleListLLMRoutes(w http.ResponseWriter, r *http.Request) {
	manager := h.routeConfigManagerForRoutes()
	resolver := h.llmRouteResolver()
	if manager == nil || resolver == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "llm route resolver is not configured")
		return
	}

	tagPrefix := r.URL.Query().Get("tag_prefix")
	tag := r.URL.Query().Get("tag")
	if tag == "" && tagPrefix == "" {
		tag = defaultLLMRouteTag
	}

	items, err := resolver.ListConfigs(r.Context(), routecore.RouteListOptions{Tag: tag, TagPrefix: tagPrefix})
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	items = filterRouteConfigsByKind(items, routecore.RouteKindLLM)
	views := make([]LLMRouteView, 0, len(items))
	for _, item := range items {
		views = append(views, llmRouteViewFromConfig(manager, item))
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]any{"items": views})
}

func (h *Handler) handleCreateLLMRoute(w http.ResponseWriter, r *http.Request) {
	manager := h.routeConfigManagerForRoutes()
	resolver := h.llmRouteResolver()
	if manager == nil || resolver == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "llm route resolver is not configured")
		return
	}

	var llmRoute llmroutepkg.LLMRouteConfig
	if err := httpjson.Decode(r, &llmRoute); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if llmRoute.ID == "" {
		_ = httpjson.Error(w, http.StatusBadRequest, "id is required")
		return
	}
	if !llmRoute.CreatedAt.IsZero() || !llmRoute.UpdatedAt.IsZero() {
		_ = httpjson.Error(w, http.StatusBadRequest, "created_at and updated_at are managed by the server and must be omitted")
		return
	}
	llmRoute.Normalize()
	if err := llmRoute.ValidateDefinition(); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	tag := r.URL.Query().Get("tag")
	if tag == "" {
		tag = defaultLLMRouteTag
	}

	cfg, err := llmRoute.ToConfig()
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := resolver.CreateConfig(r.Context(), cfg, tag); err != nil {
		if errors.Is(err, routecore.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, err := resolver.GetConfig(r.Context(), llmRoute.ID)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusCreated, llmRouteViewFromConfig(manager, created))
}

func (h *Handler) handleGetLLMRoute(w http.ResponseWriter, r *http.Request) {
	manager := h.routeConfigManagerForRoutes()
	resolver := h.llmRouteResolver()
	if manager == nil || resolver == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "llm route resolver is not configured")
		return
	}

	item, err := resolver.GetConfig(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, routecore.ErrRouteNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "llm route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item.Kind != routecore.RouteKindLLM {
		_ = httpjson.Error(w, http.StatusNotFound, "llm route not found")
		return
	}
	_ = httpjson.Write(w, http.StatusOK, llmRouteViewFromConfig(manager, item))
}

func (h *Handler) handleUpdateLLMRoute(w http.ResponseWriter, r *http.Request) {
	manager := h.routeConfigManagerForRoutes()
	resolver := h.llmRouteResolver()
	if manager == nil || resolver == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "llm route resolver is not configured")
		return
	}

	var llmRoute llmroutepkg.LLMRouteConfig
	if err := httpjson.Decode(r, &llmRoute); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	id := r.PathValue("id")
	current, err := resolver.GetConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, routecore.ErrRouteNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "llm route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if current.Kind != routecore.RouteKindLLM {
		_ = httpjson.Error(w, http.StatusNotFound, "llm route not found")
		return
	}
	if llmRoute.ID == "" {
		llmRoute.ID = id
	}
	if llmRoute.ID != id {
		_ = httpjson.Error(w, http.StatusBadRequest, "llm route id in body must match path")
		return
	}
	if !llmRoute.CreatedAt.IsZero() || !llmRoute.UpdatedAt.IsZero() {
		_ = httpjson.Error(w, http.StatusBadRequest, "created_at and updated_at are managed by the server and must be omitted")
		return
	}
	llmRoute.Normalize()
	if err := llmRoute.ValidateDefinition(); err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	cfg, err := llmRoute.ToConfig()
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := resolver.UpdateConfig(r.Context(), id, cfg); err != nil {
		if errors.Is(err, routecore.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "llm route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	item, err := resolver.GetConfig(r.Context(), id)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, llmRouteViewFromConfig(manager, item))
}

func (h *Handler) handleDeleteLLMRoute(w http.ResponseWriter, r *http.Request) {
	manager := h.routeConfigManagerForRoutes()
	resolver := h.llmRouteResolver()
	if manager == nil || resolver == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "llm route config manager is not configured")
		return
	}

	id := r.PathValue("id")
	item, err := resolver.GetConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, routecore.ErrRouteNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "llm route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item.Kind != routecore.RouteKindLLM {
		_ = httpjson.Error(w, http.StatusNotFound, "llm route not found")
		return
	}
	if err := resolver.DeleteConfig(r.Context(), id); err != nil {
		if errors.Is(err, routecore.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

func (h *Handler) handleEnableLLMRoute(w http.ResponseWriter, r *http.Request) {
	h.handleSetLLMRouteDisabled(w, r, false)
}

func (h *Handler) handleDisableLLMRoute(w http.ResponseWriter, r *http.Request) {
	h.handleSetLLMRouteDisabled(w, r, true)
}

func (h *Handler) handleSetLLMRouteDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	manager := h.routeConfigManagerForRoutes()
	resolver := h.llmRouteResolver()
	if manager == nil || resolver == nil {
		_ = httpjson.Error(w, http.StatusServiceUnavailable, "llm route resolver is not configured")
		return
	}

	id := r.PathValue("id")
	item, err := resolver.GetConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, routecore.ErrRouteNotConfigured) {
			_ = httpjson.Error(w, http.StatusNotFound, "llm route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item.Kind != routecore.RouteKindLLM {
		_ = httpjson.Error(w, http.StatusNotFound, "llm route not found")
		return
	}
	item.Disabled = disabled
	cfg := item
	if err := resolver.UpdateConfig(r.Context(), id, cfg); err != nil {
		if errors.Is(err, routecore.ErrStaticRouteReadOnly) {
			_ = httpjson.Error(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = httpjson.Error(w, http.StatusNotFound, "llm route not found")
			return
		}
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated, err := resolver.GetConfig(r.Context(), id)
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, llmRouteViewFromConfig(manager, updated))
}

func (h *Handler) llmRouteResolver() *llmroutepkg.LLMRouteResolver {
	if h.sharedLLMRouteResolver != nil {
		return h.sharedLLMRouteResolver
	}
	manager := h.routeConfigManagerForRoutes()
	if manager == nil {
		return nil
	}
	return llmroutepkg.NewLLMRouteResolver(manager)
}

func llmRouteViewFromRoute(manager *routecore.AgentRouteConfigManager, llmRoute *llmroutepkg.LLMRoute) LLMRouteView {
	var item llmroutepkg.LLMRouteConfig
	if llmRoute != nil {
		item = llmRoute.Config()
	}
	view := newLLMRouteView(item, "store", false)
	if llmRoute != nil && manager != nil && manager.IsStatic(llmRoute.ID) {
		view.Source = "caddyfile"
		view.ReadOnly = true
	}
	return view
}

func llmRouteViewFromConfig(manager *routecore.AgentRouteConfigManager, cfg routecore.AgentRouteConfig) LLMRouteView {
	item, _ := llmroutepkg.NewLLMRouteConfigFromConfig(cfg)
	view := newLLMRouteView(item, "store", false)
	if manager != nil && manager.IsStatic(cfg.ID) {
		view.Source = "caddyfile"
		view.ReadOnly = true
	}
	return view
}

func newLLMRouteView(item llmroutepkg.LLMRouteConfig, source string, readOnly bool) LLMRouteView {
	cfg, _ := item.ToConfig()
	return LLMRouteView{
		ID:           cfg.ID,
		Kind:         cfg.Kind,
		Protocol:     cfg.Protocol,
		Description:  cfg.Description,
		Disabled:     cfg.Disabled,
		MatchPolicy:  cfg.MatchPolicy,
		TargetPolicy: append(json.RawMessage(nil), cfg.TargetPolicy...),
		AuthPolicy:   cfg.AuthPolicy,
		CreatedAt:    cfg.CreatedAt,
		UpdatedAt:    cfg.UpdatedAt,
		Source:       source,
		ReadOnly:     readOnly,
	}
}
