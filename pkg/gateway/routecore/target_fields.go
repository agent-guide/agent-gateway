package routecore

import (
	"encoding/json"
	"fmt"
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
