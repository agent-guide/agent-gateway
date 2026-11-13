package configrepair

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

// ExtractModels converts only explicitly selected legacy route records. New IDs
// are route-scoped to preserve independent definitions with equal display names.
func ExtractModels(r Record) (Record, []Record, error) {
	obj, err := object(r.Data)
	if err != nil {
		return r, nil, err
	}
	if r.Family != "routes" || r.Static || r.StaticInput {
		return r, nil, fmt.Errorf("extract-models requires a dynamic LLM route")
	}
	var kind string
	_ = json.Unmarshal(obj["kind"], &kind)
	if kind != "llm" {
		return r, nil, fmt.Errorf("extract-models requires an LLM route")
	}
	policy, err := object(obj["target_policy"])
	if err != nil {
		return r, nil, err
	}
	var old struct {
		Type     string                         `json:"type"`
		Strategy modelcatalog.SelectionStrategy `json:"model_selector_strategy"`
		Fallback modelcatalog.FallbackPolicy    `json:"fallback"`
		Targets  []struct {
			Name       string `json:"name"`
			Candidates []struct {
				ProviderID    string `json:"provider_id"`
				UpstreamModel string `json:"upstream_model"`
				Weight        int    `json:"weight"`
				Priority      int    `json:"priority"`
			} `json:"candidates"`
		} `json:"model_targets"`
	}
	if err := json.Unmarshal(obj["target_policy"], &old); err != nil {
		return r, nil, err
	}
	if old.Type != "" && old.Type != "logical-model" {
		return r, nil, fmt.Errorf("route is not a legacy logical-model policy")
	}
	if len(old.Targets) == 0 {
		return r, nil, fmt.Errorf("route has no inline logical models")
	}
	var models []Record
	var targets []map[string]string
	for _, t := range old.Targets {
		digest := sha256.Sum256([]byte(r.ID + "\x00" + t.Name))
		id := fmt.Sprintf("model-%x", digest[:16])
		m := modelcatalog.LogicalModel{ID: id, Strategy: old.Strategy, Fallback: old.Fallback}
		for _, c := range t.Candidates {
			m.Candidates = append(m.Candidates, modelcatalog.ModelCandidate{ProviderID: c.ProviderID, UpstreamModel: c.UpstreamModel, Weight: c.Weight, Priority: c.Priority})
		}
		m.Normalize()
		if err := m.Validate(); err != nil {
			return r, nil, err
		}
		data, err := json.Marshal(m)
		if err != nil {
			return r, nil, err
		}
		models = append(models, Record{Family: "logical_models", ID: id, Data: data})
		targets = append(targets, map[string]string{"name": t.Name, "kind": "logical-model", "logical_model_id": id})
	}
	policy["type"], _ = json.Marshal("models")
	policy["model_targets"], _ = json.Marshal(targets)
	delete(policy, "model_selector_strategy")
	delete(policy, "fallback")
	obj["target_policy"], _ = json.Marshal(policy)
	r.Data, err = json.Marshal(obj)
	return r, models, err
}

// ConvertProviderRoute requires an explicit allowlist: an open provider route
// cannot be converted losslessly by guessing from its upstream default model.
func ConvertProviderRoute(r Record, data json.RawMessage) (Record, []Record, error) {
	canonical, err := CanonicalTarget(r.Data)
	if err != nil {
		return r, nil, err
	}
	obj, err := object(canonical)
	if err != nil {
		return r, nil, err
	}
	policy, err := object(obj["target_policy"])
	if err != nil {
		return r, nil, err
	}
	var target struct {
		ProviderID string `json:"provider_id"`
	}
	if err := json.Unmarshal(policy["provider_target"], &target); err != nil {
		return r, nil, err
	}
	var selection struct {
		DefaultModel string `json:"default_model"`
		Models       []struct {
			Name                string                      `json:"name"`
			UpstreamModel       string                      `json:"upstream_model"`
			CapabilityOverrides *provider.ModelCapabilities `json:"capability_overrides,omitempty"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &selection); err != nil || len(selection.Models) == 0 {
		return r, nil, fmt.Errorf("convert-target requires data.models with explicit name and upstream_model allowlist")
	}
	var refs []map[string]string
	var records []Record
	seen := map[string]bool{}
	for _, m := range selection.Models {
		if m.Name == "" || m.UpstreamModel == "" || m.Name != m.UpstreamModel || seen[m.Name] {
			return r, nil, fmt.Errorf("explicit model name must equal upstream_model and be unique")
		}
		seen[m.Name] = true
		model := modelcatalog.ProviderModel{ProviderID: target.ProviderID, UpstreamModel: m.UpstreamModel, Enabled: true, CapabilityOverrides: m.CapabilityOverrides}
		encoded, err := json.Marshal(model)
		if err != nil {
			return r, nil, err
		}
		records = append(records, Record{Family: "provider_models", ID: recordKey([]string{target.ProviderID, m.UpstreamModel}), Data: encoded, StaticInput: r.StaticInput})
		refs = append(refs, map[string]string{"name": m.Name, "kind": "provider-model", "provider_id": target.ProviderID, "upstream_model": m.UpstreamModel})
	}
	if selection.DefaultModel == "" && len(refs) == 1 {
		selection.DefaultModel = refs[0]["name"]
	}
	if selection.DefaultModel != "" && !seen[selection.DefaultModel] {
		return r, nil, fmt.Errorf("default_model must be exposed")
	}
	delete(policy, "provider_target")
	delete(policy, "provider_id")
	policy["type"], _ = json.Marshal("models")
	policy["default_model"], _ = json.Marshal(selection.DefaultModel)
	policy["model_targets"], _ = json.Marshal(refs)
	obj["target_policy"], _ = json.Marshal(policy)
	r.Data, err = json.Marshal(obj)
	return r, records, err
}
