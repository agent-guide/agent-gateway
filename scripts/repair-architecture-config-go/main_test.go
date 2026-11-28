package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/acp/launcher"
)

func TestMigrateACPLaunchersThroughCommand(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "config.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE agents (id TEXT PRIMARY KEY, tag TEXT, config TEXT, created_at DATETIME, updated_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE mcp_services (id TEXT PRIMARY KEY, tag TEXT, config TEXT, created_at DATETIME, updated_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mcp_services (id, tag, config) VALUES ('legacy-tools', 'mcp', '{"id":"legacy-tools","name":"Tools","transport":"stdio","command":"unused"}')`); err != nil {
		t.Fatal(err)
	}
	for id, settings := range map[string]string{
		"current": `"launcher":"legacy-acp-1"`,
		"default": `"agent_type":"codex"`,
		"legacy":  `"agent_type":"codex","codex":{"adapter_args":["--profile","two words",""]}`,
		"open":    `"agent_type":"opencode"`,
	} {
		config := fmt.Sprintf(`{"id":%q,"name":%q,"runtime":{"type":"acp","acp":{"cwd":"/workspace",%s}}}`, id, id, settings)
		if _, err := db.Exec(`INSERT INTO agents (id, config) VALUES (?, ?)`, id, config); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"--db", dbPath}, &out); err == nil || !strings.Contains(out.String(), "register ACP launcher") {
		t.Fatalf("missing launcher detection: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE acp_launchers (id TEXT PRIMARY KEY, config TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO acp_launchers (id, config) VALUES
		('codex', '{"id":"codex","command":"/opt/codex-acp"}'),
		('legacy-acp-1', '{"id":"legacy-acp-1","command":"/opt/custom-acp"}')`); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(dir, "repairs.json")
	if err := run(t.Context(), []string{"--db", dbPath, "--export-repairs", plan, "--convert-connectors"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), []string{"--db", dbPath, "--repair", plan, "--backup", dbPath + ".bak", "--convert-connectors"}, &out); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"current": "legacy-acp-1", "default": "legacy-acp-2", "legacy": "legacy-acp-3", "open": "opencode"} {
		var name string
		if err := db.QueryRow(`SELECT json_extract(config, '$.runtime.acp.launcher') FROM agents WHERE id = ?`, id).Scan(&name); err != nil || name != want {
			t.Fatalf("Agent %s launcher = %q, want %q, err = %v", id, name, want, err)
		}
	}

	if err := run(t.Context(), []string{"--db", dbPath, "--backup", dbPath + ".register.bak", "--set-acp-launcher", "third-party", "--acp-command", "/opt/bin/agent", "--acp-cwd-flag=--cwd", "--", "--stdio", "two words", ""}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []launcher.Definition{
		{ID: "codex", Launcher: launcher.Launcher{Command: "/opt/codex-acp"}},
		{ID: "legacy-acp-1", Launcher: launcher.Launcher{Command: "/opt/custom-acp"}},
		{ID: "legacy-acp-2", Launcher: launcher.Launcher{Command: "codex-acp"}},
		{ID: "legacy-acp-3", Launcher: launcher.Launcher{Command: "codex-acp", Args: []string{"--profile", "two words", ""}}},
		{ID: "third-party", Launcher: launcher.Launcher{Command: "/opt/bin/agent", Args: []string{"--stdio", "two words", ""}, CWDFlag: "--cwd"}},
	} {
		var data []byte
		if err := db.QueryRow(`SELECT config FROM acp_launchers WHERE id = ?`, want.ID).Scan(&data); err != nil {
			t.Fatal(err)
		}
		got, err := launcher.DecodeDefinition(data)
		if err != nil || got.ID != want.ID || got.Command != want.Command || got.CWDFlag != want.CWDFlag || !slices.Equal(got.Args, want.Args) {
			t.Fatalf("stored launcher = %+v, want %+v, err = %v", got, want, err)
		}
	}
	out.Reset()
	if err := run(t.Context(), []string{"--db", dbPath}, &out); err != nil || out.String() != "[]\n" {
		t.Fatalf("recheck = %q, err = %v", out.String(), err)
	}
}

func TestModelRepairThroughCommand(t *testing.T) {
	for _, family := range []string{"provider_models", "logical_models"} {
		for _, action := range []string{"replace", "delete"} {
			t.Run(family+"/"+action, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "config.db")
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				for _, statement := range []string{
					`CREATE TABLE providers(id TEXT PRIMARY KEY, config TEXT)`,
					`INSERT INTO providers VALUES ('p','{"id":"p","provider_type":"openai"}')`,
					`CREATE TABLE model_configs(provider_id TEXT, upstream_model TEXT, data TEXT, PRIMARY KEY(provider_id,upstream_model))`,
					`INSERT INTO model_configs VALUES ('p','m','{"provider_id":"p","upstream_model":"m","enabled":true}')`,
					`CREATE TABLE logical_models(id TEXT PRIMARY KEY, data TEXT)`,
				} {
					if _, err := db.Exec(statement); err != nil {
						t.Fatal(err)
					}
				}
				id, table := `["p","m"]`, "model_configs"
				replacement := `{"provider_id":"p","upstream_model":"m","enabled":true}`
				if family == "logical_models" {
					id, table = "shared", "logical_models"
					replacement = `{"id":"shared","candidates":[{"provider_id":"p","upstream_model":"m"}]}`
					if _, err := db.Exec(`INSERT INTO logical_models VALUES ('shared',NULL)`); err != nil {
						t.Fatal(err)
					}
				} else if _, err := db.Exec(`UPDATE model_configs SET data=NULL`); err != nil {
					t.Fatal(err)
				}
				operation := map[string]any{"family": family, "id": id, "action": action}
				if action == "replace" {
					operation["data"] = json.RawMessage(replacement)
				}
				data, err := json.Marshal([]any{operation})
				if err != nil {
					t.Fatal(err)
				}
				operations := filepath.Join(dir, "operations.json")
				if err := os.WriteFile(operations, data, 0600); err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				if err := run(t.Context(), []string{"--db", path, "--repair", operations, "--backup", path + ".bak"}, &out); err != nil {
					t.Fatalf("model repair: %v; %s", err, out.String())
				}
				var count int
				if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				want := 1
				if action == "delete" {
					want = 0
				}
				if count != want {
					t.Fatalf("row count = %d, want %d", count, want)
				}
				out.Reset()
				if err := run(t.Context(), []string{"--db", path}, &out); err != nil {
					t.Fatalf("recheck: %v; %s", err, out.String())
				}
				if _, err := os.Stat(path + ".bak"); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

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
			args := []string{"--db", path, "--repair", operations, "--backup", path + ".bak"}
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
	if err := os.WriteFile(ops, []byte(`[{"family":"routes","id":"a","action":"convert-target","data":{"models":[{"name":"gpt-4.1","upstream_model":"gpt-4.1"}]}},{"family":"routes","id":"b","action":"delete"},{"family":"agents","id":"agent","action":"remove-budget"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--static-config", path, "--target", "static", "--repair", ops, "--backup", path + ".bak"}, &out); err != nil {
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
	if err := run(context.Background(), []string{"--static-config", path, "--caddy-json", "--target", "static", "--repair", ops, "--backup", path + ".bak"}, &out); err != nil {
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

func TestRepairRequiresBackupBeforeOpeningDatabase(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, "repairs.json")
	if err := os.WriteFile(plan, []byte(`[{"family":"routes","id":"chat","action":"delete"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(t.Context(), []string{"--db", filepath.Join(dir, "missing.db"), "--repair", plan}, &out); err == nil || !strings.Contains(err.Error(), "--backup") {
		t.Fatalf("error = %v", err)
	}
}

func TestStaticInspectionAndRepairEnforceStartupRestrictions(t *testing.T) {
	for _, tc := range []struct{ name, target, extra string }{
		{"logical route", `{"model_targets":[{"name":"chat","candidates":[{"provider_id":"p","upstream_model":"model","default":true}]}]}`, ""},
		{"virtual keys", `{"type":"models","default_model":"gpt-4.1","model_targets":[{"name":"gpt-4.1","kind":"provider-model","provider_id":"p","upstream_model":"gpt-4.1"}]}`, `,"virtualKeys":[{"id":"key","disabled":false}]`},
		{"extensions", `{"type":"models","default_model":"gpt-4.1","model_targets":[{"name":"gpt-4.1","kind":"provider-model","provider_id":"p","upstream_model":"gpt-4.1"}]}`, `,"extensions":[{"module_id":"online.agentguide.community.limiter","schema_version":1,"config":{}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "static.json")
			route := fmt.Sprintf(`{"id":"a","kind":"llm","protocol":"openai","match_policy":{"path_prefix":"/api"},"target_policy":%s}`, tc.target)
			original := fmt.Sprintf(`{"apiVersion":"gateway.agw/v1alpha1","kind":"GatewayBundle","providers":[{"id":"p","provider_type":"openai"}],"providerModels":[{"provider_id":"p","upstream_model":"gpt-4.1","enabled":true}],"llmRoutes":[%s]%s}`, route, tc.extra)
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := run(context.Background(), []string{"--static-config", path}, &out); err == nil {
				t.Fatalf("invalid static config inspected as clean: %v, %s", err, out.String())
			}
			ops := filepath.Join(dir, "ops.json")
			if err := os.WriteFile(ops, []byte(`[{"family":"routes","id":"a","action":"replace","data":`+route+`}]`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := run(context.Background(), []string{"--static-config", path, "--target", "static", "--repair", ops, "--backup", path + ".bak"}, &out); err == nil {
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
