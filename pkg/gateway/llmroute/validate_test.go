package llmroute

import (
	"strings"
	"testing"
)

func TestValidateDefinitionRejectsEmptyRouteID(t *testing.T) {
	err := (LLMRoute{}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want route id rejection")
	}
}

func TestValidateDefinitionRejectsRouteWithoutAnyTargetMode(t *testing.T) {
	err := (LLMRoute{
		AgentRouteConfig: AgentRouteConfig{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want missing target rejection")
	}
	if !strings.Contains(err.Error(), "targets are required in model-target mode") {
		t.Fatalf("ValidateDefinition error = %q, want model-target mode rejection", err)
	}
}

func TestValidateDefinitionRejectsTargetWithoutCandidates(t *testing.T) {
	err := (LLMRoute{
		AgentRouteConfig: AgentRouteConfig{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
		TargetPolicy: &RouteLogicalModelTargetPolicy{
			ModelTargets: []RouteModelTarget{{
				Name: "chat-fast",
			}},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want target candidate rejection")
	}
}

func TestValidateDefinitionRejectsMissingProtocol(t *testing.T) {
	err := (LLMRoute{
		AgentRouteConfig: AgentRouteConfig{ID: "chat-prod"},
		TargetPolicy: &RouteDirectProviderPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "openai"},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want protocol rejection")
	}
}

func TestValidateDefinitionAllowsDirectProviderWithoutModelTargets(t *testing.T) {
	err := (LLMRoute{
		AgentRouteConfig: AgentRouteConfig{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
		TargetPolicy: &RouteDirectProviderPolicy{
			ProviderTarget: DirectProviderTarget{ProviderID: "openai-main"},
		},
	}).ValidateDefinition()
	if err != nil {
		t.Fatalf("ValidateDefinition returned error: %v", err)
	}
}

func TestProviderIDsReturnsDirectProviderID(t *testing.T) {
	ids := (LLMRoute{
		AgentRouteConfig: AgentRouteConfig{ID: "chat-prod"},
		TargetPolicy: &RouteDirectProviderPolicy{
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
	ids := (LLMRoute{
		AgentRouteConfig: AgentRouteConfig{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
		TargetPolicy: &RouteLogicalModelTargetPolicy{
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

func TestValidateDefinitionRejectsMultipleDefaultCandidateTargets(t *testing.T) {
	err := (LLMRoute{
		AgentRouteConfig: AgentRouteConfig{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
		TargetPolicy: &RouteLogicalModelTargetPolicy{
			DefaultModel: "chat-default",
			ModelTargets: []RouteModelTarget{
				{
					Name: "code-default",
					Candidates: []RouteModelCandidate{{
						ProviderID:    "zhipu-main",
						UpstreamModel: "glm-4.7",
						Default:       true,
					}},
				},
				{
					Name: "chat-default",
					Candidates: []RouteModelCandidate{{
						ProviderID:    "deepseek-main",
						UpstreamModel: "deepseek-v4-pro",
						Default:       true,
					}},
				},
			},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want conflicting default candidate rejection")
	}
	if !strings.Contains(err.Error(), "default candidates must belong to a single target model") {
		t.Fatalf("ValidateDefinition error = %q, want conflicting default candidate rejection", err)
	}
}

func TestValidateDefinitionRejectsDefaultCandidateTargetMismatch(t *testing.T) {
	err := (LLMRoute{
		AgentRouteConfig: AgentRouteConfig{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
		TargetPolicy: &RouteLogicalModelTargetPolicy{
			DefaultModel: "chat-default",
			ModelTargets: []RouteModelTarget{
				{
					Name: "code-default",
					Candidates: []RouteModelCandidate{{
						ProviderID:    "zhipu-main",
						UpstreamModel: "glm-4.7",
						Default:       true,
					}},
				},
				{
					Name: "chat-default",
					Candidates: []RouteModelCandidate{{
						ProviderID:    "deepseek-main",
						UpstreamModel: "deepseek-v4-pro",
					}},
				},
			},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want default_model mismatch rejection")
	}
	if !strings.Contains(err.Error(), `default candidate target "code-default" must match default_model "chat-default"`) {
		t.Fatalf("ValidateDefinition error = %q, want default_model mismatch rejection", err)
	}
}
