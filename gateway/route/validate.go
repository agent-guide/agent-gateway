package route

import (
	"fmt"
	"slices"
)

func (r AgentRoute) ValidateDefinition() error {
	if r.ID == "" {
		return fmt.Errorf("route_id is required")
	}
	if r.LLMAPI == "" {
		return fmt.Errorf("route %q llm_api is required", r.ID)
	}

	usesDirect := r.usesDirectProvider()
	if usesDirect {
		return nil
	}

	if len(r.TargetPolicy.ModelTargets) == 0 {
		return fmt.Errorf("route %q targets are required in model-target mode", r.ID)
	}

	targetNames := make([]string, 0, len(r.TargetPolicy.ModelTargets))
	seenNames := map[string]struct{}{}
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
		for _, candidate := range target.Candidates {
			if candidate.ProviderID == "" || candidate.UpstreamModel == "" {
				return fmt.Errorf("route %q target %q candidates require provider_id and upstream_model", r.ID, target.Name)
			}
			key := candidate.ProviderID + "/" + candidate.UpstreamModel
			if _, exists := candidateIDs[key]; exists {
				return fmt.Errorf("route %q target %q duplicates candidate %q", r.ID, target.Name, key)
			}
			candidateIDs[key] = struct{}{}
		}
	}
	if r.TargetPolicy.DefaultModel != "" && !slices.Contains(targetNames, r.TargetPolicy.DefaultModel) {
		return fmt.Errorf("route %q default_model %q must appear in targets", r.ID, r.TargetPolicy.DefaultModel)
	}

	return nil
}

func (r AgentRoute) ProviderIDs() []string {
	ids := map[string]struct{}{}
	if r.TargetPolicy.ProviderTarget.ProviderID != "" {
		ids[r.TargetPolicy.ProviderTarget.ProviderID] = struct{}{}
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
