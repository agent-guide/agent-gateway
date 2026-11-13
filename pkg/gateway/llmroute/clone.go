package llmroute

// Clone keeps target slices private; validation and execution may
// normalize their own copy without mutating another request's route generation.
func (r LLMRoute) Clone() LLMRoute {
	r.RouteMetadata = r.RouteMetadata.Clone()
	if p := r.TargetPolicy; p != nil {
		copy := *p
		copy.RouteTargetPolicyCommon = clonePolicyCommon(p.RouteTargetPolicyCommon)
		copy.ModelTargets = append([]RouteModelTarget(nil), p.ModelTargets...)
		r.TargetPolicy = &copy
	}
	return r
}

func clonePolicyCommon(p RouteTargetPolicyCommon) RouteTargetPolicyCommon {
	p.CredentialScopeOrderValue = append([]RouteCredentialScope(nil), p.CredentialScopeOrderValue...)
	p.CredentialTypeOrderValue = append([]RouteCredentialType(nil), p.CredentialTypeOrderValue...)
	return p
}
