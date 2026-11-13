package llmroute

import (
	"fmt"
	"slices"
)

func (r LLMRoute) ValidateDefinition() error {
	r.Normalize()
	if r.ID == "" {
		return fmt.Errorf("route_id is required")
	}
	if r.Protocol == "" {
		return fmt.Errorf("route %q protocol is required", r.ID)
	}
	if r.Protocol == RouteProtocolMCP {
		return fmt.Errorf("route %q protocol %q is invalid for llm routes", r.ID, r.Protocol)
	}
	if r.TargetPolicy == nil {
		return fmt.Errorf("route %q targets are required in model-target mode", r.ID)
	}
	return r.TargetPolicy.ValidateDefinition(r.ID)
}

func (r LLMRoute) ValidateStaticDefinition() error { return r.ValidateDefinition() }

func (c RouteTargetPolicyCommon) validateCredentialPolicy(routeID string) error {
	switch c.CredentialSelector() {
	case "", RouteCredentialSelectRoundRobin, RouteCredentialSelectFillFirst:
	default:
		return fmt.Errorf("route %q invalid credential_selector %q", routeID, c.CredentialSelector())
	}
	seenScopes := map[RouteCredentialScope]struct{}{}
	for _, scope := range c.CredentialScopeOrder() {
		switch scope {
		case RouteCredentialScopeModelCustom, RouteCredentialScopeProviderID:
		default:
			return fmt.Errorf("route %q invalid credential_scope_order %q", routeID, scope)
		}
		if _, ok := seenScopes[scope]; ok {
			return fmt.Errorf("route %q duplicate credential_scope_order %q", routeID, scope)
		}
		seenScopes[scope] = struct{}{}
	}
	if len(c.CredentialTypeOrder()) == 0 {
		return fmt.Errorf("route %q credential_type_order must not be empty", routeID)
	}
	seenTypes := map[RouteCredentialType]struct{}{}
	for _, credentialType := range c.CredentialTypeOrder() {
		switch credentialType {
		case RouteCredentialTypeAPIKey, RouteCredentialTypeOAuthToken:
		default:
			return fmt.Errorf("route %q invalid credential_type_order %q", routeID, credentialType)
		}
		if _, ok := seenTypes[credentialType]; ok {
			return fmt.Errorf("route %q duplicate credential_type_order %q", routeID, credentialType)
		}
		seenTypes[credentialType] = struct{}{}
	}
	return nil
}

func (p *RouteModelPolicy) ValidateDefinition(routeID string) error {
	p.Normalize()
	if err := p.RouteTargetPolicyCommon.validateCredentialPolicy(routeID); err != nil {
		return err
	}
	if len(p.ModelTargets) == 0 {
		return fmt.Errorf("route %q target_policy.model_targets are required in models mode", routeID)
	}
	seen := map[string]bool{}
	kind := p.ModelTargets[0].Kind
	providerID := p.ModelTargets[0].ProviderID
	for _, t := range p.ModelTargets {
		if t.Kind != kind {
			return fmt.Errorf("route %q cannot mix ProviderModel and LogicalModel targets; split them into separate routes", routeID)
		}
		if t.Name == "" || seen[t.Name] {
			return fmt.Errorf("route %q has empty or duplicate model name", routeID)
		}
		seen[t.Name] = true
		switch t.Kind {
		case "provider-model":
			if t.ProviderID != providerID {
				return fmt.Errorf("route %q ProviderModel targets must use a single provider; use a LogicalModel for cross-provider selection", routeID)
			}
			if t.Name != t.UpstreamModel {
				return fmt.Errorf("route %q ProviderModel name must equal upstream_model; aliases require a LogicalModel", routeID)
			}
			if t.ProviderID == "" || t.UpstreamModel == "" || t.LogicalModelID != "" {
				return fmt.Errorf("route %q invalid provider-model reference", routeID)
			}
		case "logical-model":
			if t.LogicalModelID == "" || t.ProviderID != "" || t.UpstreamModel != "" {
				return fmt.Errorf("route %q invalid logical-model reference", routeID)
			}
		default:
			return fmt.Errorf("route %q invalid model target kind %q", routeID, t.Kind)
		}
	}
	if p.DefaultModel != "" && !seen[p.DefaultModel] {
		return fmt.Errorf("route %q default_model %q must appear in targets", routeID, p.DefaultModel)
	}
	return nil
}

func (r LLMRoute) ProviderIDs() []string {
	r.Normalize()
	if r.TargetPolicy == nil {
		return nil
	}
	ids := r.TargetPolicy.ProviderIDs()
	slices.Sort(ids)
	return ids
}
