package configrepair

import (
	"encoding/json"
	"testing"
)

func TestOfflineSQLiteNullPayloadInspectionAndRepair(t *testing.T) {
	for _, family := range []string{"routes", "agents"} {
		for _, action := range []string{"replace", "delete"} {
			t.Run(family+"/"+action, func(t *testing.T) {
				path, db := oldDatabase(t)
				if _, err := db.Exec(`CREATE TABLE model_configs(provider_id TEXT,upstream_model TEXT,data TEXT,PRIMARY KEY(provider_id,upstream_model))`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO model_configs VALUES ('p','gpt-4.1','{"provider_id":"p","upstream_model":"gpt-4.1","enabled":true}')`); err != nil {
					t.Fatal(err)
				}
				id, opIndex := "a", 0
				data := json.RawMessage(`{"id":"a","kind":"llm","protocol":"openai","match_policy":{"path_prefix":"/api"},"target_policy":{"type":"models","default_model":"gpt-4.1","model_targets":[{"name":"gpt-4.1","kind":"provider-model","provider_id":"p","upstream_model":"gpt-4.1"}]}}`)
				if family == "agents" {
					id, opIndex = "agent", 2
					data = json.RawMessage(`{"id":"agent","name":"Agent","disabled":true,"runtime":{"type":"http","http":{"card_url":"https://example.org/card","protocol":"a2a"}}}`)
				}
				if _, err := db.Exec(`UPDATE `+family+` SET config=NULL WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
				ro, err := OpenSQLite(path, false)
				if err != nil {
					t.Fatal(err)
				}
				records, err := ReadSQLite(t.Context(), ro)
				ro.Close()
				if err != nil {
					t.Fatalf("inspection aborted on NULL: %v", err)
				}
				found := false
				for _, issue := range Inspect(records) {
					if issue.Family == family && issue.ID == id && issue.Field == "record" {
						found = true
					}
				}
				if !found {
					t.Fatal("inspection did not identify the damaged record")
				}
				ops := repairOperations()
				ops[opIndex] = Operation{Family: family, ID: id, Action: action, Data: data}
				if err := RepairSQLite(t.Context(), db, path+".bak", true, nil, ops); err != nil {
					t.Fatal(err)
				}
				after, err := ReadSQLite(t.Context(), db)
				if err != nil || len(Inspect(after)) != 0 {
					t.Fatalf("recheck failed: %v, %+v", err, Inspect(after))
				}
				backup, err := OpenSQLite(path+".bak", false)
				if err != nil {
					t.Fatal(err)
				}
				defer backup.Close()
				var preserved bool
				if err := backup.QueryRow(`SELECT config IS NULL FROM `+family+` WHERE id=?`, id).Scan(&preserved); err != nil || !preserved {
					t.Fatalf("backup did not retain original NULL: %v", err)
				}
			})
		}
	}
}
