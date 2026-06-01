package provider

import (
	"fmt"
	"strings"
)

type CompactMode string

const (
	CompactModeNone  CompactMode = "none"
	CompactModeCC    CompactMode = "cc"
	CompactModeCodex CompactMode = "codex"
)

// CompactModeFromOptions reads options.compact. Supported values are "cc",
// "codex", and "none"; missing or empty values resolve to "none".
func CompactModeFromOptions(opts map[string]any) (CompactMode, error) {
	v, ok := opts["compact"]
	if !ok {
		return CompactModeNone, nil
	}
	switch typed := v.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "", string(CompactModeNone):
			return CompactModeNone, nil
		case string(CompactModeCC):
			return CompactModeCC, nil
		case string(CompactModeCodex):
			return CompactModeCodex, nil
		default:
			return CompactModeNone, fmt.Errorf("provider: option compact must be one of cc, codex, none")
		}
	default:
		return CompactModeNone, fmt.Errorf("provider: option compact must be a string")
	}
}

// CompactMode reads options.compact and treats invalid values as "none". Use
// CompactModeFromOptions in provider constructors that should reject bad config.
func (c ProviderConfig) CompactMode() CompactMode {
	mode, err := CompactModeFromOptions(c.Options)
	if err != nil {
		return CompactModeNone
	}
	return mode
}
