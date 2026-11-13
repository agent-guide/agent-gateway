package configrepair

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalTarget(t *testing.T) {
	for _, tc := range []struct{ policy, want string }{
		{`{"provider_id":" p "}`, "p"},
		{`{"type":"direct-provider","provider_id":" p "}`, "p"},
		{`{"type":"logical-model","provider_id":"p"}`, ""},
		{`{"provider_target":{"provider_id":" p "}}`, "p"},
		{`{"provider_id":" ","provider_target":{"provider_id":"p"}}`, "p"},
		{`{"provider_id":"p","provider_target":{"provider_id":" p "}}`, "p"},
		{`{"provider_id":"p","provider_target":{"provider_id":"q"}}`, ""},
		{`{"provider_id":" ","provider_target":{"provider_id":""}}`, ""},
		{`{"provider_id":42}`, ""},
		{`{"provider_target":null}`, ""},
		{`{"provider_id":null}`, ""},
		{`{"provider_target":{"provider_id":false}}`, ""},
		{`[]`, ""},
	} {
		t.Run(tc.policy, func(t *testing.T) {
			data, err := CanonicalTarget([]byte(`{"id":"r","target_policy":` + tc.policy + `}`))
			if tc.want == "" {
				if err == nil {
					t.Fatal("accepted invalid target")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var r struct {
				Policy map[string]json.RawMessage `json:"target_policy"`
			}
			if err := json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			if _, ok := r.Policy["provider_id"]; ok {
				t.Fatal("outer provider retained")
			}
			var target struct {
				ID string `json:"provider_id"`
			}
			if err := json.Unmarshal(r.Policy["provider_target"], &target); err != nil {
				t.Fatal(err)
			}
			if target.ID != tc.want {
				t.Fatalf("provider changed: %s", data)
			}
		})
	}
}

func TestApplyDoesNotPartiallyConvertOrRemoveBudgets(t *testing.T) {
	records := []Record{
		{Family: "routes", ID: "a", Data: json.RawMessage(`{"id":"a","kind":"llm","match_policy":{"path_prefix":"/a"},"target_policy":{"provider_id":"p"}}`)},
		{Family: "routes", ID: "b", Data: json.RawMessage(`{"id":"b","kind":"llm","match_policy":{"path_prefix":"/b"},"target_policy":{"provider_id":"p","provider_target":{"provider_id":"q"}}}`)},
		{Family: "agents", ID: "agent", Data: json.RawMessage(`{"id":"agent","policy":{"budget":{}}}`)},
	}
	original, _ := json.Marshal(records)
	_, err := Apply(records, []Operation{{Family: "routes", ID: "a", Action: "convert-target", Data: json.RawMessage(`{"models":[{"name":"gpt-4.1","upstream_model":"gpt-4.1"}]}`)}, {Family: "routes", ID: "b", Action: "convert-target", Data: json.RawMessage(`{"models":[{"name":"gpt-4.1","upstream_model":"gpt-4.1"}]}`)}})
	if err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("error = %v", err)
	}
	after, _ := json.Marshal(records)
	if string(original) != string(after) {
		t.Fatal("failed batch mutated input")
	}
	_, err = Apply(records, []Operation{{Family: "routes", ID: "a", Action: "convert-target", Data: json.RawMessage(`{"models":[{"name":"gpt-4.1","upstream_model":"gpt-4.1"}]}`)}, {Family: "routes", ID: "b", Action: "delete"}})
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("budget silently removed: %v", err)
	}
	out, err := Apply(records, []Operation{{Family: "routes", ID: "a", Action: "convert-target", Data: json.RawMessage(`{"models":[{"name":"gpt-4.1","upstream_model":"gpt-4.1"}]}`)}, {Family: "routes", ID: "b", Action: "delete"}, {Family: "agents", ID: "agent", Action: "remove-budget"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(Inspect(out)) != 0 {
		t.Fatal("repair left hazards")
	}
}

func TestInspectionAndExplicitRepair(t *testing.T) {
	for _, budget := range []string{`{}`, `{"max_turns_per_day":0}`, `false`} {
		r := Record{Family: "agents", ID: "a", Data: json.RawMessage(`{"id":"a","policy":{"budget":` + budget + `}}`)}
		if len(Inspect([]Record{r})) != 1 {
			t.Fatal("non-null budget not reported")
		}
	}
	records := []Record{
		{Family: "routes", ID: "a", Data: json.RawMessage(`{"id":"a","kind":"mcp","match_policy":{"path_prefix":"/a"}}`)},
		{Family: "routes", ID: "b", Data: json.RawMessage(`{"id":"b","kind":"agent","match_policy":{"path_prefix":"/a"},"disabled":true}`)},
	}
	if len(Inspect(records)) != 1 {
		t.Fatal("ambiguity not reported")
	}
	if _, err := Apply(records, nil); err == nil {
		t.Fatal("implicitly chose a route")
	}
	if _, err := Apply(records, []Operation{{Family: "routes", ID: "b", Action: "replace", Data: json.RawMessage(`{"id":"b","kind":"agent","match_policy":{"path_prefix":"/b"}}`)}}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsMissingLLMPolicy(t *testing.T) {
	for _, field := range []string{"", `,"target_policy":null`, `,"target_policy":{}`} {
		t.Run(field, func(t *testing.T) {
			records := []Record{{
				Family: "routes", ID: "r",
				Data: json.RawMessage(`{"id":"r","kind":"llm","protocol":"openai"` + field + `}`),
			}}
			if err := Validate(records); err == nil {
				t.Fatal("offline validation accepted a missing or empty LLM policy")
			}
		})
	}
}
