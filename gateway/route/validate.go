package route

import "fmt"

// ValidateDefinition checks static route definition correctness without external dependencies.
func (r Route) ValidateDefinition() error {
	if r.ID == "" {
		return fmt.Errorf("route_id is required")
	}

	hasEligibleTarget := false
	for _, target := range r.Targets {
		if target.Disabled {
			continue
		}
		hasEligibleTarget = true
		if target.ProviderRef == "" {
			return fmt.Errorf("route %q has target with empty provider_ref", r.ID)
		}
	}
	if !hasEligibleTarget {
		return fmt.Errorf("route %q has no enabled targets", r.ID)
	}

	return nil
}

// ProviderRefs returns unique enabled provider references declared by the route.
func (r Route) ProviderRefs() []string {
	refs := make([]string, 0, len(r.Targets))
	seen := make(map[string]struct{}, len(r.Targets))
	for _, target := range r.Targets {
		if target.Disabled || target.ProviderRef == "" {
			continue
		}
		if _, ok := seen[target.ProviderRef]; ok {
			continue
		}
		seen[target.ProviderRef] = struct{}{}
		refs = append(refs, target.ProviderRef)
	}
	return refs
}
