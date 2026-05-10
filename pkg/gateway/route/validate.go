package route

import (
	"fmt"
	"slices"
)

func (r AgentRoute) ValidateDefinition() error {
	r.Normalize()
	if r.ID == "" {
		return fmt.Errorf("route_id is required")
	}
	if r.LLMAPI == "" {
		return fmt.Errorf("route %q llm_api is required", r.ID)
	}

	switch r.TargetPolicy.PolicyKind() {
	case RouteTargetPolicyKindDirectProvider:
		if r.TargetPolicy.ProviderID == "" {
			return fmt.Errorf("route %q target_policy.provider_id is required", r.ID)
		}
		if err := validateCredentialPolicy(r); err != nil {
			return err
		}
		for _, scope := range r.TargetPolicy.CredentialScopeOrder {
			if scope == RouteCredentialScopeModelCustom {
				return fmt.Errorf("route %q direct-provider target_policy cannot use credential_scope_order %q", r.ID, scope)
			}
		}
		return nil
	case RouteTargetPolicyKindLogicalModel:
		if err := validateCredentialPolicy(r); err != nil {
			return err
		}
	default:
		return fmt.Errorf("route %q targets are required in model-target mode", r.ID)
	}

	if len(r.TargetPolicy.ModelTargets) == 0 {
		return fmt.Errorf("route %q target_policy.model_targets are required in logical-model mode", r.ID)
	}

	switch r.TargetPolicy.ModelSelectorStrategy {
	case "", RouteSelectionStrategyAuto, RouteSelectionStrategyWeighted, RouteSelectionStrategyPriority:
	default:
		return fmt.Errorf("route %q invalid model_selector_strategy %q", r.ID, r.TargetPolicy.ModelSelectorStrategy)
	}
	if r.TargetPolicy.Fallback.MaxNum < 0 || r.TargetPolicy.Fallback.MaxNum > 5 {
		return fmt.Errorf("route %q fallback.max_num must be between 0 and 5", r.ID)
	}

	targetNames := make([]string, 0, len(r.TargetPolicy.ModelTargets))
	seenNames := map[string]struct{}{}
	defaultCandidateTargets := map[string]struct{}{}
	for _, target := range r.TargetPolicy.ModelTargets {
		if target.Name == "" {
			return fmt.Errorf("route %q target name is required", r.ID)
		}
		if _, exists := seenNames[target.Name]; exists {
			return fmt.Errorf("route %q target %q is duplicated", r.ID, target.Name)
		}
		seenNames[target.Name] = struct{}{}
		targetNames = append(targetNames, target.Name)
		if len(target.Candidates) == 0 {
			return fmt.Errorf("route %q target %q must define at least one candidate", r.ID, target.Name)
		}
		candidateIDs := map[string]struct{}{}
		defaultCandidateCount := 0
		for _, candidate := range target.Candidates {
			if candidate.ProviderID == "" || candidate.UpstreamModel == "" {
				return fmt.Errorf("route %q target %q candidates require provider_id and upstream_model", r.ID, target.Name)
			}
			key := candidate.ProviderID + "/" + candidate.UpstreamModel
			if _, exists := candidateIDs[key]; exists {
				return fmt.Errorf("route %q target %q duplicates candidate %q", r.ID, target.Name, key)
			}
			candidateIDs[key] = struct{}{}
			if candidate.Default {
				defaultCandidateCount++
			}
		}
		if defaultCandidateCount > 1 {
			return fmt.Errorf("route %q target %q may define at most one default candidate", r.ID, target.Name)
		}
		if defaultCandidateCount == 1 {
			defaultCandidateTargets[target.Name] = struct{}{}
		}
	}
	if r.TargetPolicy.DefaultModel != "" && !slices.Contains(targetNames, r.TargetPolicy.DefaultModel) {
		return fmt.Errorf("route %q default_model %q must appear in targets", r.ID, r.TargetPolicy.DefaultModel)
	}
	if len(defaultCandidateTargets) > 1 {
		return fmt.Errorf("route %q default candidates must belong to a single target model", r.ID)
	}
	if r.TargetPolicy.DefaultModel != "" {
		for targetName := range defaultCandidateTargets {
			if targetName != r.TargetPolicy.DefaultModel {
				return fmt.Errorf("route %q default candidate target %q must match default_model %q", r.ID, targetName, r.TargetPolicy.DefaultModel)
			}
		}
	}

	return nil
}

func validateCredentialPolicy(r AgentRoute) error {
	switch r.TargetPolicy.CredentialSelector {
	case "", RouteCredentialSelectRoundRobin, RouteCredentialSelectFillFirst:
	default:
		return fmt.Errorf("route %q invalid credential_selector %q", r.ID, r.TargetPolicy.CredentialSelector)
	}
	seenScopes := map[RouteCredentialScope]struct{}{}
	for _, scope := range r.TargetPolicy.CredentialScopeOrder {
		switch scope {
		case RouteCredentialScopeModelCustom, RouteCredentialScopeProviderID:
		default:
			return fmt.Errorf("route %q invalid credential_scope_order %q", r.ID, scope)
		}
		if _, ok := seenScopes[scope]; ok {
			return fmt.Errorf("route %q duplicate credential_scope_order %q", r.ID, scope)
		}
		seenScopes[scope] = struct{}{}
	}
	if len(r.TargetPolicy.CredentialSourceOrder) == 0 {
		return fmt.Errorf("route %q credential_source_order must not be empty", r.ID)
	}
	seenSources := map[RouteCredentialSource]struct{}{}
	for _, source := range r.TargetPolicy.CredentialSourceOrder {
		switch source {
		case RouteCredentialSourceAPIKey, RouteCredentialSourceCLIAuthToken:
		default:
			return fmt.Errorf("route %q invalid credential_source_order %q", r.ID, source)
		}
		if _, ok := seenSources[source]; ok {
			return fmt.Errorf("route %q duplicate credential_source_order %q", r.ID, source)
		}
		seenSources[source] = struct{}{}
	}
	return nil
}

func (r AgentRoute) ProviderIDs() []string {
	r.Normalize()
	ids := map[string]struct{}{}
	if r.TargetPolicy.ProviderID != "" {
		ids[r.TargetPolicy.ProviderID] = struct{}{}
	}
	for _, target := range r.TargetPolicy.ModelTargets {
		for _, candidate := range target.Candidates {
			if candidate.ProviderID == "" {
				continue
			}
			ids[candidate.ProviderID] = struct{}{}
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
