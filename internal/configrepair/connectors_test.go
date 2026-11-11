package configrepair

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"go.uber.org/zap"
)

func legacyConnectorDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, table := range []string{"mcp_services", "routes", "agents"} {
		if _, err := db.Exec(`CREATE TABLE ` + table + ` (id TEXT PRIMARY KEY,tag TEXT,config TEXT,created_at DATETIME,updated_at DATETIME)`); err != nil {
			t.Fatal(err)
		}
	}
	return path, db
}
func TestConnectorConversionAndLegacyPreflight(t *testing.T) {
	path, db := legacyConnectorDatabase(t)
	ctx := context.Background()
	legacy := `{"id":"docs","name":"Docs","transport":"stdio","command":"mcp-server","env":{"TOKEN":"sensitive-env"},"auth":{"type":"bearer","api_key":"sensitive-inline"},"auto_auth":true,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-02-01T00:00:00Z"}`
	if _, err := db.Exec(`INSERT INTO mcp_services VALUES ('docs','stdio',?,'2026-01-01 00:00:00','2026-02-01 00:00:00')`, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO routes(id,config) VALUES ('mcp-route',?)`, `{"id":"mcp-route","kind":"mcp","protocol":"mcp","match_policy":{"path_prefix":"/mcp"},"target_policy":{"kind":"mcp-service","service_id":"docs"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO agents(id,config) VALUES ('reader',?)`, `{"id":"reader","name":"Reader","runtime":{"type":"http","http":{"card_url":"https://example.org/card","protocol":"a2a"}},"resources":{"mcp_service_ids":["docs"]}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := configsqlite.Open(ctx, configsqlite.Config{SQLitePath: path}, zap.NewNop()); err == nil || !strings.Contains(err.Error(), "legacy_connector_layout") {
		t.Fatalf("legacy startup: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='connectors'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("startup created target table")
	}
	if err := ConvertConnectors(ctx, db, path+".bak", true); err != nil {
		t.Fatal(err)
	}
	var raw, created, updated string
	if err := db.QueryRow(`SELECT config,CAST(created_at AS TEXT),CAST(updated_at AS TEXT) FROM connectors WHERE id='docs'`).Scan(&raw, &created, &updated); err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	json.Unmarshal([]byte(raw), &obj)
	mcp := obj["mcp"].(map[string]any)
	if obj["type"] != "mcp" || mcp["auto_auth"] != true || mcp["env"].(map[string]any)["TOKEN"] != "sensitive-env" || created != "2026-01-01 00:00:00" || updated != "2026-02-01 00:00:00" {
		t.Fatalf("metadata/connection relocation failed")
	}
	if _, ok := obj["transport"]; ok {
		t.Fatal("flat alias retained")
	}
	creator, err := configsqlite.Open(ctx, configsqlite.Config{SQLitePath: path}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer creator.Close()
	backend := configstore.NewBackend(creator)
	if err := schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	store, _ := backend.Get(schema.StoreConnectors)
	if _, err := store.Get(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	records, err := ReadSQLite(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(records); err != nil {
		t.Fatal(err)
	}
}
func TestConnectorConversionConflictAndRollback(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid-record", true: "target-table"}[conflict], func(t *testing.T) {
			path, db := legacyConnectorDatabase(t)
			if conflict {
				db.Exec(`CREATE TABLE connectors(id TEXT PRIMARY KEY,config TEXT)`)
			} else {
				db.Exec(`INSERT INTO mcp_services(id,config) VALUES('broken','{"id":"broken","name":"Broken","transport":"unknown"}')`)
			}
			if err := ConvertConnectors(context.Background(), db, path+".bak", true); err == nil {
				t.Fatal("invalid conversion succeeded")
			}
			var old int
			db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='mcp_services'`).Scan(&old)
			if old != 1 {
				t.Fatal("failed conversion removed old table")
			}
			if _, err := configsqlite.Open(context.Background(), configsqlite.Config{SQLitePath: path}, nil); err == nil {
				t.Fatal("mixed/legacy table startup allowed")
			}
		})
	}
}

func TestConnectorConversionRepairsDamagedRawLegacyRecordsAtomically(t *testing.T) {
	path, db := legacyConnectorDatabase(t)
	if _, err := db.Exec(`INSERT INTO mcp_services(id,config) VALUES('damaged','null')`); err != nil {
		t.Fatal(err)
	}
	op := Operation{Family: "mcp_services", ID: "damaged", Action: "replace", Data: json.RawMessage(`{"id":"damaged","name":"Repaired","transport":"stdio","command":"mcp"}`)}
	if err := ConvertConnectors(context.Background(), db, path+".bak", true, op); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow(`SELECT config FROM connectors WHERE id='damaged'`).Scan(&raw); err != nil || !strings.Contains(raw, `"type":"mcp"`) {
		t.Fatalf("repair conversion: %s %v", raw, err)
	}
}
