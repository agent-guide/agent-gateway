package route

import "testing"

func TestValidateRequestPolicyRejectsDisallowedModelOnRoute(t *testing.T) {
	r := AgentRoute{
		ID: "chat-prod",
		Policy: RoutePolicy{
			AllowedModels: []string{"gpt-4.1"},
		},
	}
	err := r.ValidateRequestPolicy(RouteResolveRequest{
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
	err := r.ValidateRequestPolicy(RouteResolveRequest{
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

func TestValidateDefinitionRejectsEnabledTargetWithoutProviderID(t *testing.T) {
	err := (AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		Targets: []RouteTarget{
			{},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want provider_id rejection")
	}
}

func TestValidateDefinitionRejectsMissingLLMAPI(t *testing.T) {
	err := (AgentRoute{
		ID:      "chat-prod",
		Targets: []RouteTarget{{ProviderID: "openai"}},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want llm_api rejection")
	}
}

func TestProviderIDsReturnsUniqueEnabledIDs(t *testing.T) {
	ids := (AgentRoute{
		ID: "chat-prod",
		Targets: []RouteTarget{
			{ProviderID: "openai"},
			{ProviderID: "openai"},
			{ProviderID: "openrouter", Disabled: true},
			{ProviderID: "anthropic"},
		},
	}).ProviderIDs()
	if len(ids) != 2 {
		t.Fatalf("len(ProviderIDs) = %d, want 2", len(ids))
	}
	if ids[0] != "openai" || ids[1] != "anthropic" {
		t.Fatalf("ProviderIDs = %#v, want [openai anthropic]", ids)
	}
}
