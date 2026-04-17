package route

import "testing"

func TestValidateRequestPolicyRejectsDisallowedModelOnRoute(t *testing.T) {
	r := AgentRoute{
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
	r := AgentRoute{
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

func TestValidateDefinitionRejectsEmptyRouteID(t *testing.T) {
	err := (AgentRoute{}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want route id rejection")
	}
}

func TestValidateDefinitionRejectsEnabledTargetWithoutProviderRef(t *testing.T) {
	err := (AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		Targets: []RouteTarget{
			{},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want provider_ref rejection")
	}
}

func TestValidateDefinitionRejectsMissingLLMAPI(t *testing.T) {
	err := (AgentRoute{
		ID:      "chat-prod",
		Targets: []RouteTarget{{ProviderRef: "openai"}},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want llm_api rejection")
	}
}

func TestProviderRefsReturnsUniqueEnabledRefs(t *testing.T) {
	refs := (AgentRoute{
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
