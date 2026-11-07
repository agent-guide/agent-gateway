package llmroute

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRouteTargetPolicyMarshalJSONUsesModelTargets(t *testing.T) {
	t.Parallel()

	policy := &RouteLogicalModelTargetPolicy{
		ModelTargets: []RouteModelTarget{{
			Name: "chat-default",
			Candidates: []RouteModelCandidate{{
				ProviderID:    "deepseek-main",
				UpstreamModel: "deepseek-v4-pro",
				Default:       true,
			}},
		}},
	}

	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("json.Marshal(policy): %v", err)
	}

	if strings.Contains(string(data), `"models"`) {
		t.Fatalf("marshaled JSON unexpectedly contains legacy models field: %s", data)
	}
	if !strings.Contains(string(data), `"model_targets"`) {
		t.Fatalf("marshaled JSON missing model_targets field: %s", data)
	}
}

func TestDirectTargetHasOneProviderRepresentation(t *testing.T) {
	p := RouteDirectProviderPolicy{ProviderTarget: DirectProviderTarget{ProviderID: " p "}}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]json.RawMessage
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	if _, exists := policy["provider_id"]; exists {
		t.Fatalf("export retained outer field: %s", data)
	}
	if strings.Count(string(data), `"provider_id":`) != 1 {
		t.Fatalf("export did not have one provider ID: %s", data)
	}
	var decoded RouteDirectProviderPolicy
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.ProviderIDs(); len(got) != 1 || got[0] != "p" {
		t.Fatalf("provider = %v", got)
	}
}

func TestRemovedDirectTargetFieldIsExplicitlyRejected(t *testing.T) {
	for _, value := range []string{`"p"`, `""`, `null`, `42`} {
		data := []byte(`{"type":"direct-provider","provider_id":` + value + `,"provider_target":{"provider_id":"p"}}`)
		var direct RouteDirectProviderPolicy
		if err := json.Unmarshal(data, &direct); err == nil || !strings.Contains(err.Error(), "provider_id was removed") {
			t.Fatalf("direct policy accepted %s: %v", data, err)
		}
		if _, err := unmarshalRouteTargetPolicy(data); err == nil {
			t.Fatalf("route accepted %s", data)
		}
	}
	if _, err := unmarshalRouteTargetPolicy([]byte(`{"type":"logical-model","provider_id":"p","model_targets":[]}`)); err == nil {
		t.Fatal("logical policy silently ignored removed field")
	}
}
