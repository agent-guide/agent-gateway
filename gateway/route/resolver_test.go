package route

import "testing"

type fixedTestSelector struct {
	target RouteTarget
}

func (s fixedTestSelector) SelectTarget(Route, ResolveRequest) (*RouteTarget, error) {
	target := s.target
	return &target, nil
}

func TestValidateRequestPolicyRejectsDisallowedModelOnRoute(t *testing.T) {
	r := Route{
		ID: "chat-prod",
		Policy: RoutePolicy{
			AllowedModels: []string{"gpt-4.1"},
		},
	}
	err := r.ValidateRequestPolicy(ResolveRequest{
		Model: "gpt-4o-mini",
	})
	if err == nil {
		t.Fatal("ValidateRequestPolicy returned nil error, want model rejection")
	}
}

func TestValidateRequestPolicyRejectsStreamingDisabledOnRoute(t *testing.T) {
	disabled := false
	r := Route{
		ID: "chat-prod",
		Policy: RoutePolicy{
			AllowStreaming: &disabled,
		},
	}
	err := r.ValidateRequestPolicy(ResolveRequest{
		Stream: true,
	})
	if err == nil {
		t.Fatal("ValidateRequestPolicy returned nil error, want streaming rejection")
	}
}

func TestResolveTargetValidatesPolicyBeforeSelecting(t *testing.T) {
	_, err := (Route{
		ID: "chat-prod",
		Policy: RoutePolicy{
			AllowedModels: []string{"gpt-4.1"},
		},
	}).ResolveTarget(ResolveRequest{
		Model: "gpt-4o-mini",
	}, fixedTestSelector{target: RouteTarget{ProviderRef: "openai"}})
	if err == nil {
		t.Fatal("ResolveTarget returned nil error, want model rejection")
	}
}

func TestResolveTargetUsesDefaultSelectorWhenNil(t *testing.T) {
	target, err := (Route{
		ID: "chat-prod",
		Targets: []RouteTarget{
			{ProviderRef: "openai"},
		},
	}).ResolveTarget(ResolveRequest{}, nil)
	if err != nil {
		t.Fatalf("ResolveTarget returned error: %v", err)
	}
	if target == nil || target.ProviderRef != "openai" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestValidateDefinitionRejectsEmptyRouteID(t *testing.T) {
	err := (Route{}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want route id rejection")
	}
}

func TestValidateDefinitionRejectsEnabledTargetWithoutProviderRef(t *testing.T) {
	err := (Route{
		ID: "chat-prod",
		Targets: []RouteTarget{
			{},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want provider_ref rejection")
	}
}

func TestProviderRefsReturnsUniqueEnabledRefs(t *testing.T) {
	refs := (Route{
		ID: "chat-prod",
		Targets: []RouteTarget{
			{ProviderRef: "openai"},
			{ProviderRef: "openai"},
			{ProviderRef: "openrouter", Disabled: true},
			{ProviderRef: "anthropic"},
		},
	}).ProviderRefs()
	if len(refs) != 2 {
		t.Fatalf("len(ProviderRefs) = %d, want 2", len(refs))
	}
	if refs[0] != "openai" || refs[1] != "anthropic" {
		t.Fatalf("ProviderRefs = %#v, want [openai anthropic]", refs)
	}
}
