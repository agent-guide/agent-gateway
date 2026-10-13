package extension

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

var envExpansion = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandConfig classifies every declared reference before expanding ordinary
// ${NAME} values. Reference objects survive unchanged, set or unset. This is
// for local declarative files, not server secret resolution.
func (r *Registry) ExpandConfig(input []Envelope) ([]Envelope, error) {
	if r == nil {
		return nil, failure("unknown_module", "", "")
	}
	if len(input) > 64 {
		return nil, failure("invalid_config", "", "extensions")
	}
	out := make([]Envelope, 0, len(input))
	for _, e := range input {
		reg, ok := r.entries[e.ModuleID]
		if !ok || reg.Factory == nil {
			return nil, failure("unknown_module", safeID(e.ModuleID), "")
		}
		if _, err := secretRefs(e.Config, reg.Factory.SecretFields); err != nil {
			return nil, err
		}
		value, err := DecodeObject(e.Config)
		if err != nil {
			return nil, err
		}
		var walk func(any) (any, error)
		walk = func(v any) (any, error) {
			switch x := v.(type) {
			case map[string]any:
				if _, ok := x["$secret_ref"]; ok {
					return x, nil
				}
				for k, c := range x {
					next, err := walk(c)
					if err != nil {
						return nil, err
					}
					x[k] = next
				}
			case []any:
				for i, c := range x {
					next, err := walk(c)
					if err != nil {
						return nil, err
					}
					x[i] = next
				}
			case string:
				for _, match := range envExpansion.FindAllStringSubmatch(x, -1) {
					if _, ok := os.LookupEnv(match[1]); !ok {
						return nil, failure("environment_unavailable", e.ModuleID, "config")
					}
				}
				return envExpansion.ReplaceAllStringFunc(x, func(token string) string {
					name := strings.TrimSuffix(strings.TrimPrefix(token, "${"), "}")
					return os.Getenv(name)
				}), nil
			}
			return v, nil
		}
		expanded, err := walk(value)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(expanded)
		if err != nil {
			return nil, failure("invalid_config", e.ModuleID, "config")
		}
		e.Config = raw
		out = append(out, e)
	}
	return out, nil
}
