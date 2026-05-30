package provider

import (
	"strconv"
	"strings"
)

// BoolOption reads a boolean provider option. It accepts native bool values and
// strconv-parseable strings ("true", "1", ...). Any missing, unparseable, or
// otherwise-typed value yields false.
func (c ProviderConfig) BoolOption(key string) bool {
	switch typed := c.Options[key].(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && parsed
	default:
		return false
	}
}
