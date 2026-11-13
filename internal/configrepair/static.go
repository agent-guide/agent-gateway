package configrepair

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// StaticDocument retains unrelated static settings as raw JSON-compatible YAML.
// Inputs are bundle YAML or adapted Caddy JSON; no gateway is provisioned.
type StaticDocument struct {
	root  map[string]any
	app   map[string]any
	caddy bool
}

func DecodeStatic(data []byte, caddy bool) (*StaticDocument, error) {
	d := &StaticDocument{caddy: caddy}
	if err := yaml.Unmarshal(data, &d.root); err != nil {
		return nil, err
	}
	if d.root == nil {
		return nil, fmt.Errorf("static config must be an object")
	}
	if caddy {
		apps, _ := d.root["apps"].(map[string]any)
		d.app, _ = apps["agent_gateway"].(map[string]any)
		if d.app == nil {
			return nil, fmt.Errorf("expected apps.agent_gateway in adapted Caddy JSON")
		}
	} else {
		if d.root["kind"] != "GatewayBundle" || d.root["apiVersion"] != "gateway.agw/v1alpha1" {
			return nil, fmt.Errorf("expected gateway.agw/v1alpha1 GatewayBundle")
		}
		d.app = d.root
	}
	return d, nil
}

func (d *StaticDocument) Records() ([]Record, error) {
	var out []Record
	add := func(family string, value any, kind string) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		obj, err := object(data)
		if err != nil {
			return err
		}
		if kind != "" {
			if _, ok := obj["kind"]; !ok {
				obj["kind"], _ = json.Marshal(kind)
			}
			// Bundle MCP routes default the protocol in their typed decoder.
			// Retain that default in the raw envelope used by offline checks.
			if kind == "mcp" {
				var protocol string
				raw, exists := obj["protocol"]
				if !exists || (json.Unmarshal(raw, &protocol) == nil && strings.TrimSpace(protocol) == "") {
					obj["protocol"], _ = json.Marshal("mcp")
				}
			}
			if kind == "mcp" || kind == "agent" {
				field := "connector_id"
				if kind == "agent" {
					field = "agent_id"
				}
				if id, ok := obj[field]; ok {
					obj["target_policy"], _ = json.Marshal(map[string]json.RawMessage{field: id})
					delete(obj, field)
				}
			}
		}
		fields := []string{"id"}
		if family == "provider_models" {
			fields = []string{"provider_id", "upstream_model"}
		} else if family == "extensions" {
			fields = []string{"module_id"}
		}
		values := make([]string, len(fields))
		for i, field := range fields {
			if err := json.Unmarshal(obj[field], &values[i]); err != nil || values[i] == "" {
				return fmt.Errorf("static %s requires explicit IDs for offline repair", family)
			}
		}
		data, _ = json.Marshal(obj)
		out = append(out, Record{Family: family, ID: recordKey(values), Data: data, Static: true, StaticInput: true})
		return nil
	}
	if d.caddy {
		providers, _ := d.app["providers"].(map[string]any)
		for id, value := range providers {
			obj, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid static provider %q", id)
			}
			clone := map[string]any{}
			for k, v := range obj {
				clone[k] = v
			}
			clone["id"] = id
			if err := add("providers", clone, ""); err != nil {
				return nil, err
			}
		}
	}
	for _, f := range []struct{ key, family, kind string }{
		{"llmRoutes", "routes", "llm"}, {"mcpRoutes", "routes", "mcp"}, {"agentRoutes", "routes", "agent"},
		{"agents", "agents", ""}, {"providers", "providers", ""}, {"connectors", "connectors", ""}, {"virtualKeys", "virtual_keys", ""},
		{"providerModels", "provider_models", ""}, {"logicalModels", "logical_models", ""}, {"extensions", "extensions", ""},
	} {
		key := f.key
		if d.caddy {
			if key != "llmRoutes" && key != "providerModels" && key != "logicalModels" {
				continue
			}
			switch key {
			case "llmRoutes":
				key = "llm_routes"
			case "providerModels":
				key = "provider_models"
			case "logicalModels":
				key = "logical_models"
			}
		}
		value, exists := d.app[key]
		if !exists {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("static %s must be an array", key)
		}
		for _, item := range items {
			if err := add(f.family, item, f.kind); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func (d *StaticDocument) Encode(records []Record) ([]byte, error) {
	groups := map[string][]any{}
	for _, r := range records {
		if r.Family != "routes" && r.Family != "agents" && r.Family != "provider_models" && r.Family != "logical_models" {
			continue
		}
		obj, err := object(r.Data)
		if err != nil {
			return nil, err
		}
		key := "agents"
		if r.Family == "provider_models" {
			key = "providerModels"
		}
		if r.Family == "logical_models" {
			key = "logicalModels"
		}
		if r.Family == "routes" {
			var kind string
			_ = json.Unmarshal(obj["kind"], &kind)
			key = map[string]string{"llm": "llmRoutes", "mcp": "mcpRoutes", "agent": "agentRoutes"}[kind]
			if key == "" {
				return nil, fmt.Errorf("unsupported route kind %q", kind)
			}
			if kind != "llm" {
				target, err := object(obj["target_policy"])
				if err != nil {
					return nil, err
				}
				field := "connector_id"
				if kind == "agent" {
					field = "agent_id"
				}
				obj[field] = target[field]
				delete(obj, "target_policy")
			}
		}
		if d.caddy {
			if key != "llmRoutes" && key != "providerModels" && key != "logicalModels" {
				return nil, fmt.Errorf("Caddy static configuration only supports LLM routes")
			}
			switch key {
			case "llmRoutes":
				key = "llm_routes"
			case "providerModels":
				key = "provider_models"
			case "logicalModels":
				key = "logical_models"
			}
		}
		groups[key] = append(groups[key], obj)
	}
	for _, key := range []string{"llmRoutes", "mcpRoutes", "agentRoutes", "agents", "llm_routes", "providerModels", "logicalModels", "provider_models", "logical_models"} {
		if _, exists := d.app[key]; exists {
			items := groups[key]
			if items == nil {
				items = []any{}
			}
			d.app[key] = items
		}
	}
	for key, items := range groups {
		d.app[key] = items
	}
	if d.caddy {
		return json.MarshalIndent(d.root, "", "  ")
	}
	// RawMessage needs JSON conversion before YAML encoding.
	data, err := json.Marshal(d.root)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return yaml.Marshal(raw)
}

// WriteStatic saves an exclusive private backup, then atomically replaces the
// static file in its own directory. The database is never mutated here.
func WriteStatic(path, backup string, original, repaired []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("static repair requires a regular file, not a symlink")
	}
	file, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(original); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-repair-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(repaired); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
