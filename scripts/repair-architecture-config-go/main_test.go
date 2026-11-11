package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectorRepairThroughCommand(t *testing.T) {
	for _, family := range []string{"mcp_services", "connectors"} {
		t.Run(family, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE ` + family + ` (id TEXT PRIMARY KEY, tag TEXT, config TEXT, created_at DATETIME, updated_at DATETIME)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO ` + family + `(id,tag,config) VALUES ('tools','mcp','null')`); err != nil {
				t.Fatal(err)
			}
			replacement := `{"id":"tools","name":"Tools","type":"mcp","mcp":{"transport":"stdio","command":"unused"}}`
			if family == "mcp_services" {
				replacement = `{"id":"tools","name":"Tools","transport":"stdio","command":"unused"}`
			}
			operations := filepath.Join(dir, "operations.json")
			if err := os.WriteFile(operations, []byte(fmt.Sprintf(`[{"family":%q,"id":"tools","action":"replace","data":%s}]`, family, replacement)), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--db", path, "--repair", operations, "--backup", path + ".bak", "--service-stopped"}
			if family == "mcp_services" {
				args = append(args, "--convert-connectors")
			}
			var out bytes.Buffer
			if err := run(t.Context(), args, &out); err != nil {
				t.Fatalf("connector repair command: %v; %s", err, out.String())
			}
			var stored string
			if err := db.QueryRow(`SELECT config FROM connectors WHERE id='tools'`).Scan(&stored); err != nil || !strings.Contains(stored, `"type":"mcp"`) {
				t.Fatalf("repaired connector missing: %s %v", stored, err)
			}
			if _, err := os.Stat(path + ".bak"); err != nil {
				t.Fatalf("missing backup: %v", err)
			}
		})
	}
}

func TestOfflineStaticInspectionRepairAndRecheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "static.yaml")
	original := `agents:
    - id: agent
      name: Agent
      policy:
        budget: {}
      runtime:
        http:
            card_url: https://example.org/card
            protocol: a2a
        type: http
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
llmRoutes:
    - id: a
      kind: llm
      match_policy:
        path_prefix: /api
      protocol: openai
      target_policy:
        provider_id: p
    - id: b
      kind: llm
      match_policy:
        path_prefix: /api
      protocol: openai
      target_policy:
        provider_id: p
        provider_target:
            provider_id: q
providers:
    - id: p
      provider_type: openai
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
