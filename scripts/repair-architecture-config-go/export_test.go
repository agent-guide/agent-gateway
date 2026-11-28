package main

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportAndApplySQLiteRepairs(t *testing.T) {
	for _, legacyConnectors := range []bool{false, true} {
		name := "sqlite"
		if legacyConnectors {
			name = "connector-conversion"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "gateway.db")
			db := openExportFixture(t, path, []string{
				`CREATE TABLE agents(id TEXT PRIMARY KEY, tag TEXT, config TEXT, updated_at DATETIME)`,
				`INSERT INTO agents (id, config) VALUES ('agent', '{"id":"agent","name":"Agent","policy":{"budget":{}},"runtime":{"type":"acp","acp":{"agent_type":"codex","cwd":"/workspace","codex":{"adapter_args":["--profile","old profile"]}}}}')`,
				`CREATE TABLE routes(id TEXT PRIMARY KEY, config TEXT, updated_at DATETIME)`,
				`INSERT INTO routes (id, config) VALUES ('chat', '{"id":"chat","kind":"llm","protocol":"openai","target_policy":{"type":"logical-model","model_targets":[{"name":"chat","candidates":[{"provider_id":"p","upstream_model":"m"}]}]}}')`,
				`CREATE TABLE model_configs(provider_id TEXT, upstream_model TEXT, data TEXT, PRIMARY KEY(provider_id,upstream_model))`,
				`INSERT INTO model_configs VALUES ('p','m','{"provider_id":"p","upstream_model":"m","enabled":true}')`,
			})
			args := []string{"--db", path}
			if legacyConnectors {
				if _, err := db.Exec(`CREATE TABLE mcp_services(id TEXT PRIMARY KEY, tag TEXT, config TEXT)`); err != nil {
					t.Fatal(err)
				}
				if err := run(t.Context(), append(args, "--export-repairs", path+".json"), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--convert-connectors") {
					t.Fatalf("legacy table diagnosis: %v", err)
				}
				args = append(args, "--convert-connectors")
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			plan := filepath.Join(dir, "repairs.json")
			export := append(append([]string(nil), args...), "--export-repairs", plan)
			var out bytes.Buffer
			if err := run(t.Context(), export, &out); err != nil {
				t.Fatal(err)
			}
			ops, err := readOperations(plan)
			if err != nil || len(ops) != 3 || ops[0].Family != "acp_launchers" || ops[0].Action != "create" || ops[1].Action != "replace" || ops[2].Action != "extract-models" {
				t.Fatalf("unexpected repair batch: %v, error: %v", ops, err)
			}
			if info, err := os.Stat(plan); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("unexpected export file metadata: %v, %v", info, err)
			}
			if err := run(t.Context(), export, &out); !errors.Is(err, os.ErrExist) {
				t.Fatalf("export overwrote an existing plan or returned the wrong error: %v", err)
			}
			if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, original) {
				t.Fatalf("export changed the database: %v", err)
			}
			apply := append(args, "--repair", plan, "--backup", path+".bak")
			if err := run(t.Context(), apply, &out); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			if err := run(t.Context(), []string{"--db", path}, &out); err != nil || out.String() != "[]\n" {
				t.Fatalf("failed recheck: %q, %v", out.String(), err)
			}
		})
	}
}

func TestExportUsesExplicitModelAllowlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.db")
	openExportFixture(t, path, []string{
		`CREATE TABLE routes(id TEXT PRIMARY KEY, config TEXT, updated_at DATETIME)`,
		`INSERT INTO routes VALUES ('chat', '{"id":"chat","kind":"llm","protocol":"openai","target_policy":{"provider_id":"p"}}', NULL)`,
	})
	plan := filepath.Join(dir, "repairs.json")
	args := []string{"--db", path, "--export-repairs", plan}
	if err := run(t.Context(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "routes/chat") || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("missing route diagnostic: %v", err)
	}
	if _, err := os.Stat(plan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete plan published: %v", err)
	}
	choices := filepath.Join(dir, "choices.json")
	if err := os.WriteFile(choices, []byte(`[{"family":"routes","id":"chat","action":"convert-target","data":{"models":[{"name":"m","upstream_model":"m"}]}}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), append(args, "--repair", choices), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	flagPlan := filepath.Join(dir, "repairs-from-flags.json")
	if err := run(t.Context(), []string{"--db", path, "--export-repairs", flagPlan, "--allow-model", "chat=m"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	fromFile, err := os.ReadFile(plan)
	if err != nil {
		t.Fatal(err)
	}
	if fromFlag, err := os.ReadFile(flagPlan); err != nil || !bytes.Equal(fromFile, fromFlag) {
		t.Fatalf("explicit model flag produced a different batch: %v", err)
	}
	if err := run(t.Context(), []string{"--db", path, "--repair", plan, "--backup", path + ".bak"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestExportEmptyRepairBatch(t *testing.T) {
	t.Run("clean database", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "gateway.db")
		openExportFixture(t, path, nil)
		plan := filepath.Join(dir, "repairs.json")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		export := []string{"--db", path, "--export-repairs", plan}
		var out bytes.Buffer
		if err := run(t.Context(), export, &out); err != nil || !strings.Contains(out.String(), "Exported 0 validated repair operations") {
			t.Fatalf("empty export: %q, %v", out.String(), err)
		}
		if data, err := os.ReadFile(plan); err != nil || string(data) != "[]\n" {
			t.Fatalf("exported file = %q, %v", data, err)
		}
		if err := run(t.Context(), export, &out); !errors.Is(err, os.ErrExist) {
			t.Fatalf("export to an existing filename: %v", err)
		}
		out.Reset()
		if err := run(t.Context(), []string{"--db", path, "--repair", plan}, &out); err != nil || out.String() != "[]\n" {
			t.Fatalf("empty batch recheck: %q, %v", out.String(), err)
		}
		if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, original) {
			t.Fatalf("empty batch changed the database: %v", err)
		}
	})

	t.Run("connector layout only", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "gateway.db")
		db := openExportFixture(t, path, []string{
			`CREATE TABLE mcp_services(id TEXT PRIMARY KEY, tag TEXT, config TEXT)`,
			`INSERT INTO mcp_services VALUES ('tools', 'mcp', '{"id":"tools","name":"Tools","transport":"stdio","command":"unused"}')`,
		})
		plan := filepath.Join(dir, "repairs.json")
		var out bytes.Buffer
		if err := run(t.Context(), []string{"--db", path, "--export-repairs", plan, "--convert-connectors"}, &out); err != nil || !strings.Contains(out.String(), "Include --convert-connectors") {
			t.Fatalf("connector conversion export: %q, %v", out.String(), err)
		}
		if data, err := os.ReadFile(plan); err != nil || string(data) != "[]\n" {
			t.Fatalf("exported file = %q, %v", data, err)
		}
		if err := run(t.Context(), []string{"--db", path, "--repair", plan, "--convert-connectors", "--backup", path + ".bak"}, &out); err != nil {
			t.Fatal(err)
		}
		var kind string
		if err := db.QueryRow(`SELECT json_extract(config, '$.type') FROM connectors WHERE id='tools'`).Scan(&kind); err != nil || kind != "mcp" {
			t.Fatalf("converted connector type = %q, %v", kind, err)
		}
	})
}

func openExportFixture(t *testing.T, path string, statements []string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range append([]string{
		`CREATE TABLE providers(id TEXT PRIMARY KEY, config TEXT)`,
		`INSERT INTO providers VALUES ('p','{"id":"p","provider_type":"openai"}')`,
	}, statements...) {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
