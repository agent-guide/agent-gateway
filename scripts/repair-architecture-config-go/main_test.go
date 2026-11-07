package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineStaticInspectionRepairAndRecheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "static.yaml")
	original := `apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
mcpServices:
  - id: tools
    name: Tools
    transport: streamable_http
    url: https://example.org/mcp
mcpRoutes:
  - id: tools-route
    service_id: tools
    match_policy: {path_prefix: /mcp}
providers:
  - id: p
    provider_type: openai
llmRoutes:
  - id: a
    kind: llm
    protocol: openai
    match_policy: {path_prefix: /api}
    target_policy: {provider_id: p}
  - id: b
    kind: llm
    protocol: openai
    match_policy: {path_prefix: /api}
    target_policy: {provider_id: p, provider_target: {provider_id: q}}
agents:
  - id: agent
    name: Agent
    runtime:
      type: http
      http: {card_url: 'https://example.org/card', protocol: a2a}
    policy: {budget: {}}
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--static-config", path}, &out); err == nil {
		t.Fatal("hazards not detected")
	}
	for _, want := range []string{"ambiguous routes", "policy.budget", "conflicting provider_id", "convert-target"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != original {
		t.Fatal("inspection mutated static input")
	}
	ops := filepath.Join(dir, "ops.json")
	if err := os.WriteFile(ops, []byte(`[{"family":"routes","id":"a","action":"convert-target"},{"family":"routes","id":"b","action":"delete"},{"family":"agents","id":"agent","action":"remove-budget"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--static-config", path, "--target", "static", "--repair", ops, "--backup", path + ".bak", "--service-stopped"}, &out); err != nil {
		t.Fatal(err)
	}
	backed, _ := os.ReadFile(path + ".bak")
	if string(backed) != original {
		t.Fatal("backup differs from original")
	}
	out.Reset()
	if err := run(context.Background(), []string{"--static-config", path}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("recheck = %s", out.String())
	}
}

func TestCaddyJSONAndDeleteLastRoute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "caddy.json")
	data := `{"apps":{"agent_gateway":{"providers":{"p":{"provider_type":"openai"}},"llm_routes":[{"id":"a","kind":"llm","protocol":"openai","match_policy":{"path_prefix":"/api"},"target_policy":{"provider_id":"p"}}]}},"admin":{"listen":"localhost:2019"}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := filepath.Join(dir, "ops.json")
	if err := os.WriteFile(ops, []byte(`[{"family":"routes","id":"a","action":"delete"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--static-config", path, "--caddy-json", "--target", "static", "--repair", ops, "--backup", path + ".bak", "--service-stopped"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--static-config", path, "--caddy-json"}, &out); err != nil {
		t.Fatal(err)
	}
	final, _ := os.ReadFile(path)
	if !strings.Contains(string(final), "localhost:2019") {
		t.Fatal("unrelated host settings lost")
	}
}

func TestRepairFlagsFailBeforeWriting(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--db", "missing.db", "--repair", "missing.json"}, &out); err == nil || !strings.Contains(err.Error(), "service-stopped") {
		t.Fatalf("error = %v", err)
	}
}

func TestStaticInspectionAndRepairEnforceStartupRestrictions(t *testing.T) {
	for _, tc := range []struct{ name, target, extra string }{
		{"logical route", `{"model_targets":[{"name":"chat","candidates":[{"provider_id":"p","upstream_model":"model","default":true}]}]}`, ""},
		{"virtual keys", `{"provider_target":{"provider_id":"p"}}`, `,"virtualKeys":[{"id":"key","disabled":false}]`},
		{"managed models", `{"provider_target":{"provider_id":"p"}}`, `,"managedModels":[{"provider_id":"p","upstream_model":"model","enabled":true}]`},
		{"extensions", `{"provider_target":{"provider_id":"p"}}`, `,"extensions":[{"module_id":"online.agentguide.community.limiter","schema_version":1,"config":{}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "static.json")
			route := fmt.Sprintf(`{"id":"a","kind":"llm","protocol":"openai","match_policy":{"path_prefix":"/api"},"target_policy":%s}`, tc.target)
			original := fmt.Sprintf(`{"apiVersion":"gateway.agw/v1alpha1","kind":"GatewayBundle","providers":[{"id":"p","provider_type":"openai"}],"llmRoutes":[%s]%s}`, route, tc.extra)
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := run(context.Background(), []string{"--static-config", path}, &out); err == nil || !strings.Contains(out.String(), "static config") {
				t.Fatalf("invalid static config inspected as clean: %v, %s", err, out.String())
			}
			ops := filepath.Join(dir, "ops.json")
			if err := os.WriteFile(ops, []byte(`[{"family":"routes","id":"a","action":"replace","data":`+route+`}]`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := run(context.Background(), []string{"--static-config", path, "--target", "static", "--repair", ops, "--backup", path + ".bak", "--service-stopped"}, &out); err == nil || !strings.Contains(err.Error(), "static config") {
				t.Fatalf("invalid static repair accepted: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != original {
				t.Fatal("failed static validation changed the input")
			}
			if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
				t.Fatal("invalid repair reached backup/write phase")
			}
		})
	}
}
