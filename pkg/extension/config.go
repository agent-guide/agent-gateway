package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Envelope retains ConfigStore-owned module JSON and unresolved secret references.
type Envelope struct {
	ModuleID      string          `json:"module_id"`
	SchemaVersion int             `json:"schema_version"`
	Config        json.RawMessage `json:"config"`
}

// PreparedConfig carries tokens separately from resolved secrets. Only Prepare
// receives Secrets; they must never be copied into envelopes or diagnostics.
type PreparedConfig struct {
	Envelope Envelope
	Secrets  map[string]string
}

func cloneEnvelope(e Envelope) Envelope { e.Config = slices.Clone(e.Config); return e }
func validPointer(p string) bool {
	if len(p) > 256 || !strings.HasPrefix(p, "/") {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '~' {
			if i+1 == len(p) || p[i+1] != '0' && p[i+1] != '1' {
				return false
			}
			i++
		}
	}
	return true
}

var envName = regexp.MustCompile(`^env:[A-Za-z_][A-Za-z0-9_]*$`)

// DecodeObject enforces the common size/depth boundary before module decoding.
func DecodeObject(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || len(raw) > 256*1024 {
		return nil, failure("invalid_config", "", "config")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decode func(int) (any, error)
	decode = func(depth int) (any, error) {
		if depth > 32 {
			return nil, failure("invalid_config", "", "config")
		}
		token, err := dec.Token()
		if err != nil {
			return nil, failure("invalid_config", "", "config")
		}
		switch token {
		case json.Delim('{'):
			object := map[string]any{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, failure("invalid_config", "", "config")
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, failure("invalid_config", "", "config")
				}
				if _, exists := object[key]; exists {
					return nil, failure("invalid_config", "", "config")
				}
				child, err := decode(depth + 1)
				if err != nil {
					return nil, err
				}
				object[key] = child
			}
			if end, err := dec.Token(); err != nil || end != json.Delim('}') {
				return nil, failure("invalid_config", "", "config")
			}
			return object, nil
		case json.Delim('['):
			array := []any{}
			for dec.More() {
				child, err := decode(depth + 1)
				if err != nil {
					return nil, err
				}
				array = append(array, child)
			}
			if end, err := dec.Token(); err != nil || end != json.Delim(']') {
				return nil, failure("invalid_config", "", "config")
			}
			return array, nil
		default:
			return token, nil
		}
	}
	value, err := decode(0)
	if err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, failure("invalid_config", "", "config")
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return nil, failure("invalid_config", "", "config")
	}
	return value, nil
}
func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func secretRefs(raw json.RawMessage, fields []string) (map[string]string, error) {
	value, err := DecodeObject(raw)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	var walk func(any, string) error
	walk = func(v any, path string) error {
		if slices.Contains(fields, path) {
			obj, ok := v.(map[string]any)
			if !ok || len(obj) != 1 {
				return failure("invalid_secret_ref", "", path)
			}
			token, ok := obj["$secret_ref"].(string)
			if !ok || !envName.MatchString(token) {
				return failure("invalid_secret_ref", "", path)
			}
			refs[path] = token
			return nil
		}
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["$secret_ref"]; ok {
				return failure("invalid_secret_ref", "", boundedPath(path))
			}
			for k, c := range x {
				if err := walk(c, path+"/"+escapePointer(k)); err != nil {
					return err
				}
			}
		case []any:
			for i, c := range x {
				if err := walk(c, path+"/"+jsonIndex(i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(value, ""); err != nil {
		return nil, err
	}
	return refs, nil
}
func boundedPath(s string) string {
	if len(s) > 256 {
		return "config"
	}
	return s
}
func jsonIndex(i int) string { b, _ := json.Marshal(i); return string(b) }

// Validate validates locally without resolving environment references, applies
// defaults, and returns dependency order for configured envelopes.
func (r *Registry) Validate(ctx context.Context, input []Envelope) ([]Envelope, error) {
	return r.validate(ctx, input, nil)
}
func (r *Registry) validate(ctx context.Context, input []Envelope, graph map[string][]string) ([]Envelope, error) {
	if r == nil {
		return nil, failure("unknown_module", "", "")
	}
	if len(input) > 64 {
		return nil, failure("invalid_config", "", "extensions")
	}
	configs := map[string]Envelope{}
	extra := map[string][]string{}
	for _, e := range input {
		if !ValidID(e.ModuleID) {
			return nil, failure("invalid_config", "", "module_id")
		}
		reg, ok := r.entries[e.ModuleID]
		if !ok || reg.Factory == nil {
			return nil, failure("unknown_module", e.ModuleID, "")
		}
		if _, ok := configs[e.ModuleID]; ok {
			return nil, failure("duplicate_module", e.ModuleID, "")
		}
		if e.SchemaVersion != reg.Descriptor.SchemaVersion {
			return nil, failure("schema_mismatch", e.ModuleID, "")
		}
		refs, err := secretRefs(e.Config, reg.Factory.SecretFields)
		if err != nil {
			return nil, err
		}
		normalized, err := callback(func() (json.RawMessage, error) { return reg.Factory.Validate(ctx, slices.Clone(e.Config)) })
		if err != nil {
			return nil, failure("invalid_config", e.ModuleID, "config")
		}
		after, err := secretRefs(normalized, reg.Factory.SecretFields)
		if err != nil {
			return nil, err
		}
		if len(refs) != len(after) {
			return nil, failure("invalid_secret_ref", e.ModuleID, "config")
		}
		for p, ref := range refs {
			if after[p] != ref {
				return nil, failure("invalid_secret_ref", e.ModuleID, p)
			}
		}
		e.Config = slices.Clone(normalized)
		configs[e.ModuleID] = e
		if reg.Factory.Dependencies != nil {
			deps, err := callback(func() ([]string, error) { return reg.Factory.Dependencies(ctx, slices.Clone(e.Config)) })
			if err != nil || len(deps) > 64 {
				return nil, failure("invalid_config", e.ModuleID, "dependencies")
			}
			for _, dep := range deps {
				if !ValidID(dep) {
					return nil, failure("invalid_config", e.ModuleID, "dependencies")
				}
			}
			extra[e.ModuleID] = deps
		}
	}
	order, err := r.topological(extra)
	if err != nil {
		return nil, err
	}
	for id, e := range configs {
		deps := append(slices.Clone(r.entries[id].Descriptor.Dependencies), extra[id]...)
		if graph != nil {
			graph[id] = slices.Clone(deps)
		}
		for _, dep := range deps {
			if r.entries[dep].Factory != nil {
				if _, ok := configs[dep]; !ok {
					return nil, failure("missing_dependency", e.ModuleID, "extensions")
				}
			}
		}
	}
	out := make([]Envelope, 0, len(input))
	for _, id := range order {
		if e, ok := configs[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}
func resolve(e Envelope, f *Factory) (PreparedConfig, error) {
	refs, err := secretRefs(e.Config, f.SecretFields)
	if err != nil {
		return PreparedConfig{}, err
	}
	cfg := PreparedConfig{Envelope: cloneEnvelope(e), Secrets: map[string]string{}}
	for p, token := range refs {
		value, ok := os.LookupEnv(strings.TrimPrefix(token, "env:"))
		if !ok {
			return PreparedConfig{}, failure("secret_unavailable", e.ModuleID, p)
		}
		cfg.Secrets[p] = value
	}
	return cfg, nil
}
