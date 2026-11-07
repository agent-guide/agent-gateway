package configrepair

import (
	"encoding/json"
	"testing"
)

func TestOfflineRepairAllowsStaticProviderShadow(t *testing.T) {
	path, db := oldDatabase(t)
	static := []Record{{Family: "providers", ID: "p", Static: true, StaticInput: true,
		Data: json.RawMessage(`{"id":"p","provider_type":"openai","default_model":"static"}`)}}
	before, err := ReadSQLite(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range Inspect(append(before, static...)) {
		if issue.Family == "providers" {
			t.Fatalf("valid static shadow reported as a hazard: %+v", issue)
		}
	}
	if err := RepairSQLite(t.Context(), db, path+".bak", true, static, repairOperations()); err != nil {
		t.Fatal(err)
	}
	after, err := ReadSQLite(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(append(after, static...)); err != nil {
		t.Fatalf("recheck: %v", err)
	}
	found := false
	for _, r := range after {
		if r.Family == "providers" && r.ID == "p" {
			found = true
			if string(r.Data) != `{"id":"p","provider_type":"openai"}` {
				t.Fatal("repair replaced the persisted Provider with its static shadow")
			}
		}
	}
	if !found {
		t.Fatal("repair removed the shadowed Provider")
	}
	// Static-file repair flips mutability without changing configuration source.
	static[0].Static = false
	for i := range after {
		after[i].Static = true
	}
	if _, err := Apply(append(static, after...), nil); err != nil {
		t.Fatalf("static repair view: %v", err)
	}
	if err := Validate(append(static, after...)); err != nil {
		t.Fatalf("static repair validation: %v", err)
	}
}

func TestOfflineInspectionStillRejectsDuplicateProvidersWithinSource(t *testing.T) {
	for _, static := range []bool{false, true} {
		r := Record{Family: "providers", ID: "p", StaticInput: static, Data: json.RawMessage(`{"id":"p","provider_type":"openai"}`)}
		if issues := Inspect([]Record{r, r}); len(issues) != 1 || issues[0].Field != "id" {
			t.Fatalf("same-source duplicates accepted: %+v", issues)
		}
	}
}
