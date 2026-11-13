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
		RouteMetadata: RouteMetadata{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
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
		RouteMetadata: RouteMetadata{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
		TargetPolicy: &RouteModelPolicy{
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
		RouteMetadata: RouteMetadata{ID: "chat-prod"},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai", UpstreamModel: "gpt-4.1"}},
		},
	}).ValidateDefinition()
	if err == nil {
		t.Fatal("ValidateDefinition returned nil error, want protocol rejection")
	}
}

func TestValidateDefinitionAllowsConcreteModelReference(t *testing.T) {
	err := (LLMRoute{
		RouteMetadata: RouteMetadata{ID: "chat-prod", Protocol: RouteProtocolOpenAI},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai-main", UpstreamModel: "gpt-4.1"}},
		},
	}).ValidateDefinition()
	if err != nil {
		t.Fatalf("ValidateDefinition returned error: %v", err)
	}
}

func TestProviderIDsReturnsConcreteBindingProvider(t *testing.T) {
	ids := (LLMRoute{
		RouteMetadata: RouteMetadata{ID: "chat-prod"},
		TargetPolicy: &RouteModelPolicy{
			DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: "openai", UpstreamModel: "gpt-4.1"}},
		},
	}).ProviderIDs()
	if len(ids) != 1 {
		t.Fatalf("len(ProviderIDs) = %d, want 1", len(ids))
	}
	if ids[0] != "openai" {
		t.Fatalf("ProviderIDs = %#v, want [openai]", ids)
	}
}

func TestModelReferencesValidation(t *testing.T) {
	cases := []struct {
		name         string
		targets      []RouteModelTarget
		defaultModel string
		valid        bool
	}{
		{"both kinds", []RouteModelTarget{{Name: "m", Kind: "provider-model", ProviderID: "p", UpstreamModel: "m"}, {Name: "logical", Kind: "logical-model", LogicalModelID: "l"}}, "logical", false},
		{"one provider multiple models", []RouteModelTarget{{Kind: "provider-model", ProviderID: "p", UpstreamModel: "a"}, {Kind: "provider-model", ProviderID: "p", UpstreamModel: "b"}}, "a", true},
		{"multiple providers", []RouteModelTarget{{Name: "a", Kind: "provider-model", ProviderID: "p", UpstreamModel: "a"}, {Name: "b", Kind: "provider-model", ProviderID: "q", UpstreamModel: "b"}}, "a", false},
		{"provider alias", []RouteModelTarget{{Name: "chat", Kind: "provider-model", ProviderID: "p", UpstreamModel: "m"}}, "chat", false},
		{"logical references", []RouteModelTarget{{Name: "chat", Kind: "logical-model", LogicalModelID: "l"}, {Name: "fast", Kind: "logical-model", LogicalModelID: "l2"}}, "chat", true},
		{"empty", nil, "", false},
		{"missing provider model", []RouteModelTarget{{Name: "m", Kind: "provider-model", ProviderID: "p"}}, "", false},
		{"missing logical model", []RouteModelTarget{{Name: "m", Kind: "logical-model"}}, "", false},
		{"mixed reference", []RouteModelTarget{{Name: "m", Kind: "logical-model", LogicalModelID: "l", ProviderID: "p"}}, "", false},
		{"duplicate name", []RouteModelTarget{{Name: "m", Kind: "logical-model", LogicalModelID: "l"}, {Name: "m", Kind: "logical-model", LogicalModelID: "l2"}}, "", false},
		{"unexposed default", []RouteModelTarget{{Name: "m", Kind: "logical-model", LogicalModelID: "l"}}, "other", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := RouteModelPolicy{DefaultModel: tc.defaultModel, ModelTargets: tc.targets}
			if err := p.ValidateDefinition("r"); (err == nil) != tc.valid {
				t.Fatalf("ValidateDefinition()=%v valid=%v", err, tc.valid)
			}
		})
	}
}
