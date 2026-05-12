package route

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
