package routecore

import (
	"encoding/json"
	"fmt"
	"github.com/agent-guide/agent-gateway/internal/strictjson"
	"strings"
)

// ValidateLLMTargetFields rejects removed fields at raw-envelope boundaries,
// before permissive JSON decoding can silently discard their provenance.
// It is not a target decoder or an existing-data converter.
func ValidateLLMTargetFields(routeID string, data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("route %q target_policy: %w", routeID, err)
	}
	for key := range fields {
		if strings.EqualFold(key, "provider_id") {
			return fmt.Errorf("route %q target_policy.provider_id was removed; use an explicit ProviderModel reference in target_policy.model_targets; inspect and explicitly convert existing data with scripts/repair-architecture-config (docs/guides/architecture-config-recovery.md)", routeID)
		}
	}
	var kind string
	_ = json.Unmarshal(fields["type"], &kind)
	if kind == "logical-model" {
		return fmt.Errorf("route %q inline logical-model policy was removed; use models with explicit model references; run scripts/repair-architecture-config", routeID)
	}
	if _, ok := fields["provider_target"]; ok || kind == "direct-provider" {
		return fmt.Errorf("route %q direct-provider was removed; register ProviderModels and expose explicit model_targets; use scripts/repair-architecture-config", routeID)
	}
	allowed := map[string]bool{"type": true, "model_targets": true, "default_model": true, "credential_selector": true, "credential_scope_order": true, "credential_type_order": true}
	for k := range fields {
		if !allowed[k] {
			return fmt.Errorf("route %q unsupported target_policy field %q; use independent LogicalModel scheduling", routeID, k)
		}
	}
	if raw, ok := fields["model_targets"]; ok {
		var targets []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &targets); err != nil {
			return err
		}
		for _, t := range targets {
			for k := range t {
				switch k {
				case "name", "kind", "provider_id", "upstream_model", "logical_model_id":
				default:
					return fmt.Errorf("route %q unsupported model target field %q; use an independent LogicalModel", routeID, k)
				}
			}
		}
	}

	return nil
}

// ValidateMCPTargetFields rejects removed configuration names at every raw
// loading boundary, including empty or null aliases.
func ValidateMCPTargetFields(routeID string, data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	v, err := strictjson.Parse(data, 4<<20, 64)
	if err != nil {
		return err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("route %q MCP target must be object", routeID)
	}
	for k := range obj {
		if k != "kind" && k != "connector_id" {
			return fmt.Errorf("route %q unsupported MCP target field; use connector_id and offline conversion", routeID)
		}
	}
	if kind, ok := obj["kind"]; ok && kind != "connector" && kind != "" {
		return fmt.Errorf("route %q unsupported MCP target kind; use connector", routeID)
	}
	return nil
}
