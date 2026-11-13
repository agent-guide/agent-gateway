// Package configrepair inspects raw configuration without starting a gateway or
// decoding the entire store through startup validation. It is an offline tool.
package configrepair

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
)

type Record struct {
	Family string          `json:"family"`
	ID     string          `json:"id"`
	Data   json.RawMessage `json:"data"`
	Static bool            `json:"-"`
	// StaticInput retains source rules when Static is flipped to select which
	// source an offline repair may mutate.
	StaticInput bool `json:"-"`
}

type Issue struct {
	Family string `json:"family"`
	ID     string `json:"id"`
	Field  string `json:"field"`
	Action string `json:"action"`
}

// Composite keys use a JSON tuple so component separators cannot collide.
func recordKey(values []string) string {
	if len(values) == 1 {
		return values[0]
	}
	data, _ := json.Marshal(values)
	return string(data)
}

func object(data []byte) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	return obj, nil
}

// CanonicalTarget classifies the original JSON before normalization can erase
// field provenance. Conflicting values always require an explicit replacement.
func CanonicalTarget(data []byte) ([]byte, error) {
	route, err := object(data)
	if err != nil {
		return nil, err
	}
	policy, err := object(route["target_policy"])
	if err != nil {
		return nil, fmt.Errorf("target_policy: %w", err)
	}
	var kind string
	if err := json.Unmarshal(policy["type"], &kind); err != nil && len(policy["type"]) > 0 {
		return nil, err
	}
	if kind != "" && kind != "direct-provider" {
		return nil, fmt.Errorf("target_policy is not direct-provider")
	}
	inner := map[string]json.RawMessage{}
	if value, ok := policy["provider_target"]; ok {
		inner, err = object(value)
		if err != nil {
			return nil, fmt.Errorf("provider_target: %w", err)
		}
	}
	read := func(obj map[string]json.RawMessage) (string, error) {
		value, exists := obj["provider_id"]
		if !exists {
			return "", nil
		}
		var text string
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return "", fmt.Errorf("provider_id must be a string")
		}
		if err := json.Unmarshal(value, &text); err != nil {
			return "", fmt.Errorf("provider_id must be a string")
		}
		return strings.TrimSpace(text), nil
	}
	outerID, err := read(policy)
	if err != nil {
		return nil, err
	}
	innerID, err := read(inner)
	if err != nil {
		return nil, err
	}
	if outerID != "" && innerID != "" && outerID != innerID {
		return nil, fmt.Errorf("conflicting provider_id values; replace the route with an explicit target")
	}
	if innerID == "" {
		innerID = outerID
	}
	if innerID == "" {
		return nil, fmt.Errorf("both provider_id values are empty")
	}
	inner["provider_id"], _ = json.Marshal(innerID)
	delete(policy, "provider_id")
	policy["provider_target"], _ = json.Marshal(inner)
	route["target_policy"], _ = json.Marshal(policy)
	return json.Marshal(route)
}

// Inspect reports migration hazards even when unrelated records cannot decode.
// Reports contain identifiers and field names, never raw configuration/secrets.
func Inspect(records []Record) []Issue {
	var issues []Issue
	var routes []routecore.RouteConfig
	seen := map[string]uint8{}
	for _, r := range records {
		add := func(field, action string) { issues = append(issues, Issue{r.Family, r.ID, field, action}) }
		key := r.Family + "/" + r.ID
		source := uint8(1)
		if r.StaticInput {
			source = 2
		}
		// Providers use static precedence at runtime. Repeated records within
		// either source and duplicate routes remain configuration hazards.
		if seen[key] != 0 && (r.Family != "providers" || seen[key]&source != 0) {
			add("id", "duplicate ID across static/dynamic configuration; explicitly rewrite or delete")
		}
		seen[key] |= source
		obj, err := object(r.Data)
		if err != nil {
			add("record", "invalid JSON object; replace or delete by ID")
			continue
		}
		fields := []string{"id"}
		switch r.Family {
		case "provider_models":
			fields = []string{"provider_id", "upstream_model"}
		case "extensions":
			fields = []string{"module_id"}
		}
		values := make([]string, len(fields))
		validID := true
		for i, field := range fields {
			if json.Unmarshal(obj[field], &values[i]) != nil || values[i] == "" {
				validID = false
			}
		}
		if !validID || recordKey(values) != r.ID {
			add("id", "record ID must match its storage ID")
		}
		switch r.Family {
		case "provider_models":
			var enabled bool
			value, exists := obj["enabled"]
			if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &enabled) != nil {
				add("enabled", "replace with an explicit enabled boolean; legacy omitted values must be chosen explicitly")
			}
		case "routes":
			var route routecore.RouteConfig
			if err := json.Unmarshal(r.Data, &route); err != nil {
				add("record", "invalid route shape; replace or delete by ID")
				continue
			}
			routes = append(routes, route)
			if route.Kind != routecore.RouteKindLLM {
				continue
			}
			policy, err := object(obj["target_policy"])
			if err != nil {
				add("target_policy", "replace invalid target policy")
				continue
			}
			var kind string
			_ = json.Unmarshal(policy["type"], &kind)
			_, outer := policy["provider_id"]
			_, inner := policy["provider_target"]
			if kind == "direct-provider" || outer || inner {
				if _, err := CanonicalTarget(r.Data); err != nil {
					add("target_policy", err.Error())
				} else {
					add("target_policy", "convert-target with an explicit model allowlist to ProviderModel references")
				}
			}
			if kind == "models" {
				var p llmroute.RouteModelPolicy
				if err := json.Unmarshal(route.TargetPolicy, &p); err != nil {
					add("target_policy", "replace invalid models policy")
				} else if err := p.ValidateDefinition(route.ID); err != nil {
					add("target_policy", "replace route: "+err.Error())
				}
			}
		case "agents":
			if value, ok := obj["policy"]; ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				policy, err := object(value)
				if err != nil {
					add("policy", "replace invalid policy")
					continue
				}
				if budget, ok := policy["budget"]; ok && !bytes.Equal(bytes.TrimSpace(budget), []byte("null")) {
					add("policy.budget", "budget is not enforced; explicitly remove-budget or replace/delete the Agent")
				}
			}
		}
	}
	for _, err := range routecore.RouteConflicts(routes) {
		issues = append(issues, Issue{"routes", "", "match_policy", err.Error()})
	}
	sort.Slice(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		return a.Family+"/"+a.ID+"/"+a.Field+"/"+a.Action < b.Family+"/"+b.ID+"/"+b.Field+"/"+b.Action
	})
	return issues
}

type Operation struct {
	Family string          `json:"family"`
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// Apply stages the whole batch. It never modifies the caller's records.
// Validation and persistence belong to the enclosing offline transaction.
func Apply(records []Record, operations []Operation) ([]Record, error) {
	out := append([]Record(nil), records...)
	seen := map[string]bool{}
	for _, op := range operations {
		key := op.Family + "/" + op.ID
		if seen[key] {
			return nil, fmt.Errorf("multiple operations for %s; use one explicit replacement", key)
		}
		seen[key] = true
		index := -1
		for i, r := range out {
			if r.Family == op.Family && r.ID == op.ID && !r.Static {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, fmt.Errorf("mutable record %s not found", key)
		}
		r := &out[index]
		switch op.Action {
		case "delete":
			out = append(out[:index], out[index+1:]...)
		case "replace":
			obj, err := object(op.Data)
			if err != nil {
				return nil, err
			}
			var id string
			if op.Family == "provider_models" {
				var providerID, model string
				if json.Unmarshal(obj["provider_id"], &providerID) != nil || json.Unmarshal(obj["upstream_model"], &model) != nil {
					return nil, fmt.Errorf("replacement requires concrete model identity")
				}
				id = recordKey([]string{providerID, model})
			} else {
				_ = json.Unmarshal(obj["id"], &id)
			}
			if id != op.ID {
				return nil, fmt.Errorf("replacement ID must equal %q", op.ID)
			}
			r.Data = append([]byte(nil), op.Data...)
		case "extract-models":
			converted, models, err := ExtractModels(*r)
			if err != nil {
				return nil, err
			}
			out[index] = converted
			for _, m := range models {
				for _, existing := range out {
					if existing.Family == m.Family && existing.ID == m.ID {
						return nil, fmt.Errorf("logical model ID conflict %q; use explicit repair", m.ID)
					}
				}
				out = append(out, m)
			}
		case "convert-target":
			if op.Family != "routes" {
				return nil, fmt.Errorf("convert-target requires routes")
			}
			converted, models, err := ConvertProviderRoute(*r, op.Data)
			if err != nil {
				return nil, err
			}
			out[index] = converted
			for _, m := range models {
				exists := false
				for _, existing := range out {
					if existing.Family == m.Family && existing.ID == m.ID {
						exists = true
						break
					}
				}
				if !exists {
					out = append(out, m)
				}
			}

		case "remove-budget":
			if op.Family != "agents" {
				return nil, fmt.Errorf("remove-budget requires agents")
			}
			obj, err := object(r.Data)
			if err != nil {
				return nil, err
			}
			policy, err := object(obj["policy"])
			if err != nil {
				return nil, err
			}
			delete(policy, "budget")
			obj["policy"], _ = json.Marshal(policy)
			r.Data, _ = json.Marshal(obj)
		default:
			return nil, fmt.Errorf("unsupported action %q", op.Action)
		}
	}
	if issues := Inspect(out); len(issues) > 0 {
		var errs []error
		for _, i := range issues {
			errs = append(errs, fmt.Errorf("%s/%s %s: %s", i.Family, i.ID, i.Field, i.Action))
		}
		return nil, errors.Join(errs...)
	}
	return out, nil
}
