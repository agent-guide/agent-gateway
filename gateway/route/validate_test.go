package route

import (
	"strings"
	"testing"
)

func TestValidateDefinitionRejectsEmptyRouteID(t *testing.T) {
	err := (AgentRoute{}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want route id rejection")
	}
}

func TestValidateDefinitionRejectsRouteWithoutAnyTargetMode(t *testing.T) {
	err := (AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		TargetPolicy: RouteTargetPolicy{
			ProviderTarget: DirectProviderTarget{},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want missing target rejection")
	}
	if !strings.Contains(err.Error(), "targets are required in model-target mode") {
		t.Fatalf("ValidateDefinition error = %q, want model-target mode rejection", err)
	}
}

func TestValidateDefinitionRejectsTargetWithoutCandidates(t *testing.T) {
	err := (AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		TargetPolicy: RouteTargetPolicy{
			ModelTargets: []RouteModelTarget{{
				Name: "chat-fast",
			}},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want target candidate rejection")
	}
}

func TestValidateDefinitionRejectsMissingLLMAPI(t *testing.T) {
	err := (AgentRoute{
		ID: "chat-prod",
		TargetPolicy: RouteTargetPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "openai"},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want llm_api rejection")
	}
}

func TestValidateDefinitionAllowsDirectProviderWithoutModelTargets(t *testing.T) {
	err := (AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		TargetPolicy: RouteTargetPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "openai-main"},
		},
	}).ValidateDefinition()
	if err != nil {
		t.Fatalf("ValidateDefinition returned error: %v", err)
	}
}

func TestValidateDefinitionPrefersDirectProviderWhenMixedTargetsConfigured(t *testing.T) {
	err := (AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		TargetPolicy: RouteTargetPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "openai-main"},
			DefaultModel:   "chat-fast",
			ModelTargets: []RouteModelTarget{{
				Name: "chat-fast",
			}},
		},
	}).ValidateDefinition()
	if err != nil {
		t.Fatalf("ValidateDefinition returned error: %v", err)
	}
}

func TestProviderIDsReturnsDirectProviderID(t *testing.T) {
	ids := (AgentRoute{
		ID: "chat-prod",
		TargetPolicy: RouteTargetPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "openai"},
		},
	}).ProviderIDs()
	if len(ids) != 1 {
		t.Fatalf("len(ProviderIDs) = %d, want 1", len(ids))
	}
	if ids[0] != "openai" {
		t.Fatalf("ProviderIDs = %#v, want [openai]", ids)
	}
}

func TestProviderIDsIncludesModelTargetProviders(t *testing.T) {
	ids := (AgentRoute{
		ID:     "chat-prod",
		LLMAPI: "openai",
		TargetPolicy: RouteTargetPolicy{
			ModelTargets: []RouteModelTarget{{
				Name: "chat-fast",
				Candidates: []RouteModelCandidate{
					{ProviderID: "openai", UpstreamModel: "gpt-4.1-mini"},
					{ProviderID: "zhipu", UpstreamModel: "glm-4.5-air"},
				},
			}},
		},
	}).ProviderIDs()
	if len(ids) != 2 {
		t.Fatalf("len(ProviderIDs) = %d, want 2", len(ids))
	}
	if ids[0] != "openai" || ids[1] != "zhipu" {
		t.Fatalf("ProviderIDs = %#v, want [openai zhipu]", ids)
	}
}
