package llmroute

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRouteModelPolicyMarshalJSONUsesModelTargets(t *testing.T) {
	t.Parallel()

	policy := &RouteModelPolicy{
		ModelTargets: []RouteModelTarget{{
			Name: "chat-default",
			Kind: "provider-model", ProviderID: "deepseek-main", UpstreamModel: "deepseek-v4-pro",
		}},
	}

	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("json.Marshal(policy): %v", err)
	}

	if strings.Contains(string(data), `"candidates"`) {
		t.Fatalf("marshaled JSON unexpectedly contains legacy models field: %s", data)
	}
	if !strings.Contains(string(data), `"model_targets"`) {
		t.Fatalf("marshaled JSON missing model_targets field: %s", data)
	}
}

func TestDirectTargetHasOneProviderRepresentation(t *testing.T) {
	p := RouteModelPolicy{DefaultModel: "gpt-4.1", ModelTargets: []RouteModelTarget{{Name: "gpt-4.1", Kind: "provider-model", ProviderID: " p ", UpstreamModel: "gpt-4.1"}}}
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
	var decoded RouteModelPolicy
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
		var direct RouteModelPolicy
		if err := json.Unmarshal(data, &direct); err == nil || !strings.Contains(err.Error(), "provider_id was removed") {
			t.Fatalf("direct policy accepted %s: %v", data, err)
		}
		if _, err := NewLLMRouteFromConfig(RouteConfig{TargetPolicy: data}); err == nil {
			t.Fatalf("route accepted %s", data)
		}
	}
	if _, err := NewLLMRouteFromConfig(RouteConfig{TargetPolicy: []byte(`{"type":"logical-model","provider_id":"p","model_targets":[]}`)}); err == nil {
		t.Fatal("logical policy silently ignored removed field")
	}
}

func TestProviderOnlyTargetsAreRejected(t *testing.T) {
	for _, raw := range []string{`{"type":"direct-provider"}`, `{"provider_target":null}`, `{"provider_target":{}}`, `{"provider_target":{"provider_id":"p"}}`, `{"type":"models","provider_target":null,"model_targets":[{"name":"m","kind":"provider-model","provider_id":"p","upstream_model":"m"}]}`} {
		if _, err := NewLLMRouteFromConfig(RouteConfig{TargetPolicy: []byte(raw)}); err == nil {
			t.Fatalf("accepted removed Provider-only target: %s", raw)
		}
	}
}

func TestLLMRouteJSONRejectsUnknownFields(t *testing.T) {
	for _, body := range []string{`{"match":{}}`, `{"match_policy":{"path_prefx":"/"}}`, `{"auth_policy":{"require_virtual_kee":true}}`, `{"protocol":"openai","protocol":"cc"}`} {
		var route LLMRoute
		if err := json.Unmarshal([]byte(body), &route); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	var route LLMRoute
	if err := json.Unmarshal([]byte(`{"id":"catch-all","protocol":"openai","match_policy":{},"target_policy":{"type":"models","model_targets":[{"kind":"provider-model","provider_id":"p","upstream_model":"m"}]}}`), &route); err != nil {
		t.Fatal(err)
	}
	if err := route.ValidateDefinition(); err != nil {
		t.Fatalf("explicit catch-all must remain valid: %v", err)
	}
}

// Every entry path must validate the concrete policy, without an interface decoder.
func TestRouteModelPolicyStrictDecodeAcrossBoundaries(t *testing.T) {
	for _, raw := range []string{
		`{"type":"unknown","model_targets":[]}`,
		`{"type":"direct-provider"}`,
		`{"type":"logical-model"}`,
		`{"type":42}`,
		`{"type":"models","unknown":true}`,
		`{"type":"models","model_targets":[{"kind":"provider-model","upstream_modle":"m"}]}`,
		`{"type":"models","type":"models"}`,
		`{"model_targets":[{"provider_id":"p","provider_id":"q"}]}`,
		`[]`,
	} {
		t.Run(raw, func(t *testing.T) {
			var policy RouteModelPolicy
			if err := json.Unmarshal([]byte(raw), &policy); err == nil {
				t.Fatal("direct policy decode accepted invalid JSON")
			}
			var route LLMRoute
			if err := json.Unmarshal([]byte(`{"target_policy":`+raw+`}`), &route); err == nil {
				t.Fatal("route decode accepted invalid policy")
			}
			if _, err := NewLLMRouteFromConfig(RouteConfig{TargetPolicy: []byte(raw)}); err == nil {
				t.Fatal("stored config decode accepted invalid policy")
			}
		})
	}
}

func TestLLMRouteConcretePolicyRoundTrip(t *testing.T) {
	for _, kind := range []string{`"type":"models",`, ""} {
		raw := []byte(`{"id":"r","protocol":"openai","target_policy":{` + kind + `"model_targets":[{"kind":"provider-model","provider_id":" p ","upstream_model":" m "}]}}`)
		var route LLMRoute
		if err := json.Unmarshal(raw, &route); err != nil {
			t.Fatal(err)
		}
		if err := route.ValidateDefinition(); err != nil {
			t.Fatal(err)
		}
		cfg, err := route.ToConfig()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := NewLLMRouteFromConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		var result LLMRoute
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result.TargetPolicy == nil || len(result.TargetPolicy.ModelTargets) != 1 ||
			result.TargetPolicy.ModelTargets[0].Name != "m" || result.TargetPolicy.ModelTargets[0].ProviderID != "p" {
			t.Fatalf("policy not preserved: %s", data)
		}
		if !strings.Contains(string(data), `"type":"models"`) {
			t.Fatalf("missing models discriminator: %s", data)
		}
	}
}

func TestLLMRouteMissingPolicyRemainsNil(t *testing.T) {
	for _, raw := range []string{`{"id":"r","protocol":"openai"}`, `{"id":"r","protocol":"openai","target_policy":null}`} {
		var route LLMRoute
		if err := json.Unmarshal([]byte(raw), &route); err != nil {
			t.Fatal(err)
		}
		if route.TargetPolicy != nil || route.ValidateDefinition() == nil {
			t.Fatal("missing policy must remain nil and fail validation")
		}
		var cfg RouteConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatal(err)
		}
		stored, err := NewLLMRouteFromConfig(cfg)
		if err != nil || stored.TargetPolicy != nil {
			t.Fatalf("stored missing policy: %#v, %v", stored, err)
		}
	}
}

func TestLLMRouteMarshalProtocolOmission(t *testing.T) {
	for _, protocol := range []RouteProtocol{"", RouteProtocolOpenAI} {
		route := LLMRoute{RouteMetadata: RouteMetadata{Protocol: protocol}}
		data, err := json.Marshal(route)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if _, present := fields["protocol"]; present != (protocol != "") {
			t.Fatalf("protocol %q marshaled as %s", protocol, data)
		}
		if protocol != "" && string(fields["protocol"]) != `"openai"` {
			t.Fatalf("protocol not preserved: %s", data)
		}
		if string(fields["kind"]) != `"llm"` {
			t.Fatalf("kind must always be llm: %s", data)
		}
	}
}
