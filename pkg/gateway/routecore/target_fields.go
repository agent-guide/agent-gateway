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
			return fmt.Errorf("route %q target_policy.provider_id was removed; use target_policy.provider_target.provider_id; inspect and explicitly convert existing data with scripts/repair-architecture-config (docs/guides/architecture-config-recovery.md)", routeID)
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
