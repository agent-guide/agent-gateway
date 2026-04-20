package route

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
)

// ValidateDefinition checks static route definition correctness without external dependencies.
func (r AgentRoute) ValidateDefinition() error {
	if r.ID == "" {
		return fmt.Errorf("route_id is required")
	}
	if r.LLMAPI == "" {
		return fmt.Errorf("route %q llm_api is required", r.ID)
	}

	hasEligibleTarget := false
	for _, target := range r.Targets {
		if target.Disabled {
			continue
		}
		hasEligibleTarget = true
		if target.ProviderID == "" {
			return fmt.Errorf("route %q has target with empty provider_id", r.ID)
		}
	}
	if !hasEligibleTarget {
		return fmt.Errorf("route %q has no enabled targets", r.ID)
	}

	return nil
}

// ProviderIDs returns unique enabled provider IDs declared by the route.
func (r AgentRoute) ProviderIDs() []string {
	ids := make([]string, 0, len(r.Targets))
	seen := make(map[string]struct{}, len(r.Targets))
	for _, target := range r.Targets {
		if target.Disabled || target.ProviderID == "" {
			continue
		}
		if _, ok := seen[target.ProviderID]; ok {
			continue
		}
		seen[target.ProviderID] = struct{}{}
		ids = append(ids, target.ProviderID)
	}
	return ids
}

// ValidateRequestPolicy validates the request against route-level policy.
func (r AgentRoute) ValidateRequestPolicy(req RouteResolveRequest) error {
	if req.Model != "" {
		if len(r.Policy.AllowedModels) > 0 && !slices.Contains(r.Policy.AllowedModels, req.Model) {
			return statuserr.New(http.StatusForbidden, fmt.Sprintf("model %q is not allowed on route %q", req.Model, r.ID))
		}
	}

	if req.Stream {
		if r.Policy.AllowStreaming != nil && !*r.Policy.AllowStreaming {
			return statuserr.New(http.StatusForbidden, "streaming is disabled on this route")
		}
	}

	return nil
}

// matchesConditions checks whether a target's conditions are satisfied by the request.
func matchesConditions(conditions TargetConditions, req RouteResolveRequest) bool {
	if len(conditions.Models) > 0 && req.Model != "" && !slices.Contains(conditions.Models, req.Model) {
		return false
	}
	if conditions.RequireStreaming != nil && *conditions.RequireStreaming != req.Stream {
		return false
	}
	return true
}
