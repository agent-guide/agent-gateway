package utils

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

var expireKeys = [...]string{"expired", "expire", "expires_at", "expiresAt", "expiry", "expires"}

// ExpirationFromMap extracts an expiration timestamp from a metadata map.
// It checks common expiry key names and nested "token"/"Token" objects for
// OAuth-style token representations.
func ExpirationFromMap(meta map[string]any) (time.Time, bool) {
	if meta == nil {
		return time.Time{}, false
	}
	for _, key := range expireKeys {
		if v, ok := meta[key]; ok {
			if ts, ok1 := ParseTimeValue(v); ok1 {
				return ts, true
			}
		}
	}
	// Check nested "token" object for OAuth-style tokens.
	for _, nestedKey := range []string{"token", "Token"} {
		if nested, ok := meta[nestedKey]; ok {
			switch val := nested.(type) {
			case map[string]any:
				if ts, ok1 := ExpirationFromMap(val); ok1 {
					return ts, true
				}
			case map[string]string:
				temp := make(map[string]any, len(val))
				for k, v := range val {
					temp[k] = v
				}
				if ts, ok1 := ExpirationFromMap(temp); ok1 {
					return ts, true
				}
			}
		}
	}
	return time.Time{}, false
}

// ParseTimeValue converts an arbitrary value to time.Time.
// Accepts strings (RFC3339, Unix timestamp), float64, int64, and json.Number.
func ParseTimeValue(v any) (time.Time, bool) {
	switch value := v.(type) {
	case string:
		s := strings.TrimSpace(value)
		if s == "" {
			return time.Time{}, false
		}
		for _, layout := range []string{
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02 15:04:05",
			"2006-01-02 15:04",
		} {
			if ts, err := time.Parse(layout, s); err == nil {
				return ts, true
			}
		}
		if unix, err := strconv.ParseInt(s, 10, 64); err == nil {
			return normaliseUnix(unix), true
		}
	case float64:
		return normaliseUnix(int64(value)), true
	case int64:
		return normaliseUnix(value), true
	case json.Number:
		if i, err := value.Int64(); err == nil {
			return normaliseUnix(i), true
		}
	}
	return time.Time{}, false
}

func normaliseUnix(raw int64) time.Time {
	if raw <= 0 {
		return time.Time{}
	}
	if raw > 1_000_000_000_000 {
		return time.UnixMilli(raw)
	}
	return time.Unix(raw, 0)
}

// ParseBoolAny converts an arbitrary value to bool.
// Accepts bool, string ("true"/"false"/etc.), float64, and json.Number.
func ParseBoolAny(val any) (bool, bool) {
	switch typed := val.(type) {
	case bool:
		return typed, true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return false, false
		}
		parsed, err := strconv.ParseBool(trimmed)
		if err != nil {
			return false, false
		}
		return parsed, true
	case float64:
		return typed != 0, true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return false, false
		}
		return parsed != 0, true
	default:
		return false, false
	}
}

// ParseIntAny converts an arbitrary value to int.
// Accepts int, int32, int64, float64, json.Number, and string.
func ParseIntAny(val any) (int, bool) {
	switch typed := val.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// ParseDurationAny converts an arbitrary value to time.Duration.
// Accepts time.Duration, numeric nanoseconds, and duration strings like "10s".
func ParseDurationAny(val any) (time.Duration, bool) {
	switch typed := val.(type) {
	case time.Duration:
		return typed, true
	case int:
		return time.Duration(typed), true
	case int32:
		return time.Duration(typed), true
	case int64:
		return time.Duration(typed), true
	case float64:
		return time.Duration(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return time.Duration(parsed), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		if parsed, err := time.ParseDuration(trimmed); err == nil {
			return parsed, true
		}
		if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return time.Duration(parsed), true
		}
		return 0, false
	default:
		return 0, false
	}
}
