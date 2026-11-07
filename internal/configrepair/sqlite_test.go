package configrepair

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
	"go.uber.org/zap"
)

func oldDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"providers", "routes", "agents"} {
		if _, err := db.Exec(`CREATE TABLE ` + table + ` (id TEXT PRIMARY KEY,tag TEXT,config TEXT,created_at DATETIME,updated_at DATETIME)`); err != nil {
			t.Fatal(err)
		}
	}
	rows := []Record{
		{Family: "providers", ID: "p", Data: json.RawMessage(`{"id":"p","provider_type":"openai"}`)},
		{Family: "routes", ID: "a", Data: json.RawMessage(`{"id":"a","kind":"llm","protocol":"openai","match_policy":{"path_prefix":"/api"},"target_policy":{"provider_id":"p"}}`)},
		{Family: "routes", ID: "b", Data: json.RawMessage(`{"id":"b","kind":"llm","protocol":"openai","disabled":true,"match_policy":{"path_prefix":"/api"},"target_policy":{"provider_id":"p","provider_target":{"provider_id":"q"}}}`)},
		{Family: "agents", ID: "agent", Data: json.RawMessage(`{"id":"agent","name":"Agent","disabled":true,"runtime":{"type":"http","http":{"card_url":"https://example.org/card","protocol":"a2a"}},"policy":{"budget":{}}}`)},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO `+r.Family+` (id,tag,config) VALUES (?,?,?)`, r.ID, "", string(r.Data)); err != nil {
			t.Fatal(err)
		}
	}
	return path, db
}

func repairOperations() []Operation {
	return []Operation{{Family: "routes", ID: "a", Action: "convert-target"}, {Family: "routes", ID: "b", Action: "delete"}, {Family: "agents", ID: "agent", Action: "remove-budget"}}
}

func TestOfflineSQLiteRepairAndRestart(t *testing.T) {
	ctx := context.Background()
	path, db := oldDatabase(t)
	ro, err := OpenSQLite(path, false)
	if err != nil {
		t.Fatal(err)
	}
	before, err := ReadSQLite(ctx, ro)
	ro.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(Inspect(before)) != 4 {
		t.Fatalf("issues = %+v", Inspect(before))
	}
	creator, err := configsqlite.Open(ctx, configsqlite.Config{SQLitePath: path}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	backend := configstore.NewBackend(creator)
	if err := schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	startup := gateway.NewAgentGateway()
	if err := startup.Bootstrap(ctx, gateway.BootstrapOptions{ConfigStoreBackend: backend}); err == nil || !strings.Contains(err.Error(), "repair-architecture-config") {
		t.Fatalf("invalid startup was not rejected with recovery guidance: %v", err)
	}
	startup.Close()
	if err := RepairSQLite(ctx, db, path+".bak", false, nil, repairOperations()); err == nil {
		t.Fatal("repair did not require stopped service")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatal("created backup before confirmation")
	}
	if err := RepairSQLite(ctx, db, path+".bak", true, nil, repairOperations()); err != nil {
		t.Fatal(err)
	}
	backup, err := OpenSQLite(path+".bak", false)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	backed, err := ReadSQLite(ctx, backup)
	if err != nil {
		t.Fatal(err)
	}
	if len(Inspect(backed)) != 4 {
		t.Fatal("backup did not preserve original invalid records")
	}
	if info, _ := os.Stat(path + ".bak"); info.Mode().Perm() != 0o600 {
		t.Fatal("backup contains secrets and must be private")
	}
	// Normal schema registration and decoding work after offline repair. No
	// listener, Admin endpoint, Card fetch or data-plane startup was required.
	store, err := backend.Get(schema.StoreRoutes)
	if err != nil {
		t.Fatal(err)
	}
	manager := routecore.NewAgentRouteConfigManager(store)
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	agents, err := backend.Get(schema.StoreAgents)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agents.List(ctx); err != nil {
		t.Fatal(err)
	}
	final, err := ReadSQLite(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(final); err != nil {
		t.Fatal(err)
	}
	restarted := gateway.NewAgentGateway()
	if err := restarted.Bootstrap(ctx, gateway.BootstrapOptions{ConfigStoreBackend: backend}); err != nil {
		t.Fatalf("restart after offline repair: %v", err)
	}
	restarted.Close()
}

func TestRepairRollbackAndReferenceProtection(t *testing.T) {
	for _, tc := range []string{"trigger failure", "dangling ingress", "batch conflict"} {
		t.Run(tc, func(t *testing.T) {
			ctx := context.Background()
			path, db := oldDatabase(t)
			ops := repairOperations()
			switch tc {
			case "trigger failure":
				if _, err := db.Exec(`CREATE TRIGGER fail_agent BEFORE UPDATE ON agents BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "dangling ingress":
				if _, err := db.Exec(`INSERT INTO routes (id,config) VALUES ('ingress','{"id":"ingress","kind":"agent","protocol":"agent","match_policy":{"path_prefix":"/agent"},"target_policy":{"agent_id":"agent"}}')`); err != nil {
					t.Fatal(err)
				}
				ops[2].Action = "delete"
			case "batch conflict":
				ops[1].Action = "convert-target"
			}
			before, err := ReadSQLite(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			if err := RepairSQLite(ctx, db, path+".bak", true, nil, ops); err == nil {
				t.Fatal("invalid repair committed")
			}
			after, err := ReadSQLite(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if string(a) != string(b) {
				t.Fatal("failed repair partially committed")
			}
		})
	}
}

func TestReadOnlyInspectionDoesNotCreateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, err := OpenSQLite(path, false); err == nil {
		t.Fatal("missing input accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("inspection created input")
	}
	if _, err := Apply([]Record{{Family: "agents", ID: "a", Static: true, Data: []byte(`{"id":"a"}`)}}, []Operation{{Family: "agents", ID: "a", Action: "delete"}}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatal("database repair changed static record")
	}
}

func TestOldBudgetCanBeRepairedAfterStartupRejection(t *testing.T) {
	ctx := context.Background()
	path, db := oldDatabase(t)
	// Isolate the old budget from the other migration hazards.
	if _, err := db.Exec(`DELETE FROM routes WHERE id='b'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE routes SET config='{"id":"a","kind":"llm","protocol":"openai","match_policy":{"path_prefix":"/api"},"target_policy":{"provider_target":{"provider_id":"p"}}}' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	creator, err := configsqlite.Open(ctx, configsqlite.Config{SQLitePath: path}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	backend := configstore.NewBackend(creator)
	if err := schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	startup := gateway.NewAgentGateway()
	err = startup.Bootstrap(ctx, gateway.BootstrapOptions{ConfigStoreBackend: backend})
	startup.Close()
	if err == nil || !strings.Contains(err.Error(), `agent "agent" policy.budget is unsupported`) || !strings.Contains(err.Error(), "repair-architecture-config") {
		t.Fatalf("startup error = %v", err)
	}
	if err := RepairSQLite(ctx, db, path+".bak", true, nil, []Operation{{Family: "agents", ID: "agent", Action: "remove-budget"}}); err != nil {
		t.Fatal(err)
	}
	restarted := gateway.NewAgentGateway()
	if err := restarted.Bootstrap(ctx, gateway.BootstrapOptions{ConfigStoreBackend: backend}); err != nil {
		t.Fatal(err)
	}
	restarted.Close()
}

func TestRepairRollsBackSilentlyIgnoredWrites(t *testing.T) {
	for _, trigger := range []string{
		`CREATE TRIGGER ignore_update BEFORE UPDATE ON routes BEGIN SELECT RAISE(IGNORE); END`,
		`CREATE TRIGGER ignore_delete BEFORE DELETE ON agents BEGIN SELECT RAISE(IGNORE); END`,
		`CREATE TRIGGER change_other AFTER UPDATE ON routes BEGIN UPDATE agents SET config=json_set(config,'$.name','Changed by trigger') WHERE id='agent'; END`,
	} {
		t.Run(trigger, func(t *testing.T) {
			ctx := context.Background()
			path, db := oldDatabase(t)
			if err := RepairSQLite(ctx, db, path+".initial.bak", true, nil, repairOperations()); err != nil {
				t.Fatal(err)
			}
			before, err := ReadSQLite(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			ops := []Operation{{Family: "routes", ID: "a", Action: "replace", Data: json.RawMessage(`{"id":"a","kind":"llm","protocol":"openai","match_policy":{"path_prefix":"/changed"},"target_policy":{"provider_target":{"provider_id":"p"}}}`)}}
			if !strings.Contains(trigger, "change_other") {
				ops = append(ops, Operation{Family: "agents", ID: "agent", Action: "delete"})
			}
			if err := RepairSQLite(ctx, db, path+".bak", true, nil, ops); err == nil || !strings.Contains(err.Error(), "persisted repair") {
				t.Fatalf("expected staged readback mismatch, got %v", err)
			}
			after, err := ReadSQLite(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if string(a) != string(b) {
				t.Fatal("failed readback verification partially committed")
			}
		})
	}
}

func TestRepairPreservesRetainedAgentReferences(t *testing.T) {
	ctx := context.Background()
	path, db := oldDatabase(t)
	if _, err := db.Exec(`UPDATE agents SET config=json_set(config,
		'$.resources',json('{"provider_ids":["removed-provider"],"mcp_service_ids":["removed-service"],"virtual_key_ids":["removed-key"]}'),
		'$.runtime.http.auth_ref','revoked-credential') WHERE id='agent'`); err != nil {
		t.Fatal(err)
	}
	if err := RepairSQLite(ctx, db, path+".bak", true, nil, repairOperations()); err != nil {
		t.Fatalf("retained references blocked unrelated repairs: %v", err)
	}
	records, err := ReadSQLite(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(records); err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Family == "agents" {
			for _, ref := range []string{"removed-provider", "removed-service", "removed-key", "revoked-credential"} {
				if !strings.Contains(string(r.Data), ref) {
					t.Fatalf("repair removed retained reference %q", ref)
				}
			}
		}
	}
	// Execution dependencies remain strong even though inventory links are weak.
	for _, refs := range []string{`{"llm_route_ids":["missing"]}`, `{"mcp_route_ids":["missing"]}`, `{"mcp_route_ids":["a"]}`} {
		candidate := append([]Record(nil), records...)
		for i, r := range candidate {
			if r.Family == "agents" {
				var obj map[string]json.RawMessage
				if err := json.Unmarshal(r.Data, &obj); err != nil {
					t.Fatal(err)
				}
				obj["routes"] = json.RawMessage(refs)
				candidate[i].Data, _ = json.Marshal(obj)
			}
		}
		if err := Validate(candidate); err == nil {
			t.Fatalf("accepted invalid route dependencies: %s", refs)
		}
	}
}

func TestRepairRollsBackManagedModelAndExtensionChanges(t *testing.T) {
	for _, tc := range []struct{ name, trigger string }{
		{"model deletion", `DELETE FROM model_configs;`},
		{"model update", `UPDATE model_configs SET data=json_set(data,'$.enabled',false);`},
		{"extension deletion", `DELETE FROM extensions;`},
		{"extension update", `UPDATE extensions SET config=json_set(config,'$.config.timeout_ms',200);`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			path, db := oldDatabase(t)
			for _, sql := range []string{
				`CREATE TABLE model_configs (provider_id TEXT, upstream_model TEXT, data TEXT, PRIMARY KEY(provider_id,upstream_model))`,
				`INSERT INTO model_configs VALUES ('p','model/with/slashes','{"provider_id":"p","upstream_model":"model/with/slashes","enabled":true}')`,
				`CREATE TABLE extensions (module_id TEXT PRIMARY KEY, config TEXT)`,
				`INSERT INTO extensions VALUES ('online.agentguide.community.limiter','{"module_id":"online.agentguide.community.limiter","schema_version":1,"config":{"timeout_ms":100}}')`,
			} {
				if _, err := db.Exec(sql); err != nil {
					t.Fatal(err)
				}
			}
			before, err := ReadSQLite(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			if len(before) != 6 {
				t.Fatalf("missing configuration families: %+v", before)
			}
			for _, r := range before {
				if r.Family == "managed_models" && r.ID != `["p","model/with/slashes"]` {
					t.Fatalf("invalid composite identity: %s", r.ID)
				}
			}
			if _, err := db.Exec(`CREATE TRIGGER change_config AFTER UPDATE ON routes BEGIN ` + tc.trigger + ` END`); err != nil {
				t.Fatal(err)
			}
			if err := RepairSQLite(ctx, db, path+".bak", true, nil, repairOperations()); err == nil || !strings.Contains(err.Error(), "persisted repair") {
				t.Fatalf("expected cross-table readback mismatch, got %v", err)
			}
			after, err := ReadSQLite(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if string(a) != string(b) {
				t.Fatal("cross-table changes escaped rollback")
			}
		})
	}
}
