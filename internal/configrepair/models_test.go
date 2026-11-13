package configrepair

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
)

func TestExtractLogicalModelsOfflineAndRestart(t *testing.T) {
	for _, scenario := range []string{"unknown-field", "missing-enabled", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			failReadback := scenario == "rollback"
			ctx := context.Background()
			path, db := oldDatabase(t)
			raw := `{"id":"a","kind":"llm","protocol":"openai","match_policy":{"path_prefix":"/api"},"target_policy":{"default_model":"chat","model_selector_strategy":"priority","fallback":{"enabled":true,"max_num":2},"model_targets":[{"name":"chat","candidates":[{"provider_id":"p","upstream_model":"a","priority":5},{"provider_id":"p","upstream_model":"b","priority":1}]}]}}`
			if _, err := db.Exec(`UPDATE routes SET config=? WHERE id='a'`, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE TABLE model_configs (provider_id TEXT,upstream_model TEXT,data TEXT,PRIMARY KEY(provider_id,upstream_model))`); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"a", "b"} {
				data, _ := json.Marshal(modelcatalog.ProviderModel{ProviderID: "p", UpstreamModel: name, Enabled: true})
				if name == "a" {
					data = []byte(`{"provider_id":"p","upstream_model":"a","enabled":true,"unknown":true}`)
					if scenario == "missing-enabled" {
						data = []byte(`{"provider_id":"p","upstream_model":"a"}`)
					}
				}
				if _, err := db.Exec(`INSERT INTO model_configs VALUES (?,?,?)`, "p", name, string(data)); err != nil {
					t.Fatal(err)
				}
			}
			if failReadback {
				if _, err := db.Exec(`CREATE TRIGGER ignore_repair BEFORE UPDATE ON routes BEGIN SELECT RAISE(IGNORE); END`); err != nil {
					t.Fatal(err)
				}
			}
			ops := repairOperations()
			ops[0].Action = "extract-models"
			ops = append(ops, Operation{Family: "provider_models", ID: recordKey([]string{"p", "a"}), Action: "replace", Data: json.RawMessage(`{"provider_id":"p","upstream_model":"a","enabled":true}`)})
			err := RepairSQLite(ctx, db, path+".bak", true, nil, ops)
			if failReadback {
				if err == nil {
					t.Fatal("readback failure committed extraction")
				}
				var got string
				if err := db.QueryRow(`SELECT config FROM routes WHERE id='a'`).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != raw {
					t.Fatal("rollback changed route")
				}
				var tables int
				if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='logical_models'`).Scan(&tables); err != nil {
					t.Fatal(err)
				}
				if tables != 0 {
					t.Fatal("rollback retained new model table")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			records, err := ReadSQLite(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(records); err != nil {
				t.Fatal(err)
			}
			creator, err := configsqlite.Open(ctx, configsqlite.Config{SQLitePath: path}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = creator.Close() })
			backend := configstore.NewBackend(creator)
			if err := schema.RegisterDefaultStores(backend); err != nil {
				t.Fatal(err)
			}
			g := gateway.NewAgentGateway()
			if err := g.Bootstrap(ctx, gateway.BootstrapOptions{ConfigStoreBackend: backend}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(g.Close)
			cfg, err := g.RouteConfigManager().Get(ctx, "a")
			if err != nil {
				t.Fatal(err)
			}
			r, err := llmroute.NewLLMRouteFromConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			target, err := r.ResolveTarget(ctx, g.ModelCatalog(), g.ProviderManager(), llmroute.RequestRequirements{Model: "chat"})
			if err != nil {
				t.Fatal(err)
			}
			if target.UpstreamModel != "a" || !target.Fallback.Enabled || target.Fallback.MaxNum != 2 {
				t.Fatalf("migration lost selection semantics: %+v", target)
			}
		})
	}
}

func TestRepairInvalidModelRoutePolicies(t *testing.T) {
	for _, targets := range []string{
		`[{"name":"chat","kind":"provider-model","provider_id":"p","upstream_model":"gpt-4.1"}]`,
		`[{"name":"gpt-4.1","kind":"provider-model","provider_id":"p","upstream_model":"gpt-4.1"},{"name":"other","kind":"provider-model","provider_id":"q","upstream_model":"other"}]`,
		`[{"name":"gpt-4.1","kind":"provider-model","provider_id":"p","upstream_model":"gpt-4.1"},{"name":"chat","kind":"logical-model","logical_model_id":"shared"}]`,
	} {
		t.Run(targets, func(t *testing.T) {
			ctx := t.Context()
			path, db := oldDatabase(t)
			if err := RepairSQLite(ctx, db, path+".initial.bak", true, nil, repairOperations()); err != nil {
				t.Fatal(err)
			}
			valid := `{"id":"a","kind":"llm","protocol":"openai","match_policy":{"path_prefix":"/api"},"target_policy":{"type":"models","default_model":"gpt-4.1","model_targets":[{"kind":"provider-model","provider_id":"p","upstream_model":"gpt-4.1"}]}}`
			invalid := `{"id":"a","kind":"llm","protocol":"openai","match_policy":{"path_prefix":"/api"},"target_policy":{"type":"models","model_targets":` + targets + `}}`
			if _, err := db.Exec(`UPDATE routes SET config=? WHERE id='a'`, invalid); err != nil {
				t.Fatal(err)
			}
			records, err := ReadSQLite(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			if len(Inspect(records)) == 0 || Validate(records) == nil {
				t.Fatal("invalid model policy was not reported")
			}
			if err := RepairSQLite(ctx, db, path+".models.bak", true, nil, []Operation{{Family: "routes", ID: "a", Action: "replace", Data: json.RawMessage(valid)}}); err != nil {
				t.Fatal(err)
			}
			creator, err := configsqlite.Open(ctx, configsqlite.Config{SQLitePath: path}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = creator.Close() })
			backend := configstore.NewBackend(creator)
			if err := schema.RegisterDefaultStores(backend); err != nil {
				t.Fatal(err)
			}
			g := gateway.NewAgentGateway()
			if err := g.Bootstrap(ctx, gateway.BootstrapOptions{ConfigStoreBackend: backend}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(g.Close)
			cfg, err := g.RouteConfigManager().Get(ctx, "a")
			if err != nil {
				t.Fatal(err)
			}
			r, err := llmroute.NewLLMRouteFromConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			target, err := r.ResolveTarget(ctx, g.ModelCatalog(), g.ProviderManager(), llmroute.RequestRequirements{Model: "gpt-4.1"})
			if err != nil || target.ProviderID != "p" || target.UpstreamModel != "gpt-4.1" {
				t.Fatalf("target=%+v err=%v", target, err)
			}
		})
	}
}
