package route

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/agent-guide/caddy-agent-gateway/internal/utils"
)

// RouteLoader resolves the latest persisted route definition for a route ID.
type RouteLoader func(ctx context.Context, routeID string) (*Route, error)

// ResolveRequest captures the request attributes required for route resolution.
type ResolveRequest struct {
	HTTPRequest *http.Request
	Model       string
	Stream      bool
}

// ResolveTarget validates route policy and selects an eligible target for the request.
func (r Route) ResolveTarget(req ResolveRequest, selector RouteTargetSelector) (*RouteTarget, error) {
	r.Normalize()

	if err := r.ValidateRequestPolicy(req); err != nil {
		return nil, err
	}
	if selector == nil {
		selector = DefaultRouteSelector{}
	}
	return selector.SelectTarget(r, req)
}

// ValidateRequestPolicy validates the request against route-level policy.
func (r Route) ValidateRequestPolicy(req ResolveRequest) error {
	if req.Model != "" {
		if len(r.Policy.AllowedModels) > 0 && !slices.Contains(r.Policy.AllowedModels, req.Model) {
			return utils.NewHTTPError(http.StatusForbidden, fmt.Sprintf("model %q is not allowed on route %q", req.Model, r.ID))
		}
	}

	if req.Stream {
		if r.Policy.AllowStreaming != nil && !*r.Policy.AllowStreaming {
			return utils.NewHTTPError(http.StatusForbidden, "streaming is disabled on this route")
		}
	}

	return nil
}

// matchesConditions checks whether a target's conditions are satisfied by the request.
func matchesConditions(conditions TargetConditions, req ResolveRequest) bool {
	if len(conditions.Models) > 0 && req.Model != "" && !slices.Contains(conditions.Models, req.Model) {
		return false
	}
	if conditions.RequireStreaming != nil && *conditions.RequireStreaming != req.Stream {
		return false
	}
	return true
}
