package llmroute

import "fmt"

// Clone keeps the compiled target cache private; validation and execution may
// normalize their own copy without mutating another request's route generation.
func (r LLMRoute) Clone() (LLMRoute, error) {
	r.RouteMetadata = r.RouteMetadata.Clone()
	switch p := r.TargetPolicy.(type) {
	case nil:
	case *RouteDirectProviderPolicy:
		if p != nil {
			copy := *p
			copy.RouteTargetPolicyCommon = clonePolicyCommon(p.RouteTargetPolicyCommon)
			r.TargetPolicy = &copy
		}
	case *RouteLogicalModelTargetPolicy:
		if p != nil {
			copy := *p
			copy.RouteTargetPolicyCommon = clonePolicyCommon(p.RouteTargetPolicyCommon)
			copy.ModelTargets = append([]RouteModelTarget(nil), p.ModelTargets...)
			for i := range copy.ModelTargets {
				copy.ModelTargets[i].Candidates = append([]RouteModelCandidate(nil), copy.ModelTargets[i].Candidates...)
			}
			r.TargetPolicy = &copy
		}
	default:
		return LLMRoute{}, fmt.Errorf("unsupported route target policy %T", p)
	}
	return r, nil
}

func clonePolicyCommon(p RouteTargetPolicyCommon) RouteTargetPolicyCommon {
	p.CredentialScopeOrderValue = append([]RouteCredentialScope(nil), p.CredentialScopeOrderValue...)
	p.CredentialTypeOrderValue = append([]RouteCredentialType(nil), p.CredentialTypeOrderValue...)
	return p
}
