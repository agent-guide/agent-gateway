package ctlcmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/agent-guide/agent-gateway/pkg/gateway/agentroute"
	"github.com/agent-guide/agent-gateway/pkg/gatewaybundle"
)

func TestApplyAgentRoutesStripsExportedManagedTimestamps(t *testing.T) {
	created := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/agents/routes":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/admin/agents/routes":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if _, ok := body["created_at"]; ok {
				t.Fatalf("create body retained created_at: %#v", body)
			}
			if _, ok := body["updated_at"]; ok {
				t.Fatalf("create body retained updated_at: %#v", body)
			}
			created = true
			w.WriteHeader(http.StatusCreated)
			body["source"] = "store"
			body["read_only"] = false
			_ = json.NewEncoder(w).Encode(body)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	now := time.Now().UTC()
	bundle := &gatewaybundle.GatewayBundle{AgentRoutes: []agentroute.AgentRouteConfig{{
		AgentRouteBaseConfig: agentroute.AgentRouteBaseConfig{
			ID: "agent:reviewer:review", MatchPolicy: agentroute.RouteMatch{PathPrefix: "/review"},
			CreatedAt: now, UpdatedAt: now,
		},
		AgentID: "reviewer",
	}}}
	client := adminclient.New(adminclient.Config{BaseURL: srv.URL})
	var applyErr error
	if err := applyAgentRoutes(context.Background(), client, bundle, func(_, _, _ string, err error) { applyErr = err }); err != nil {
		t.Fatal(err)
	}
	if applyErr != nil {
		t.Fatal(applyErr)
	}
	if !created {
		t.Fatal("AgentRoute create was not called")
	}
}

func TestApplyExtensionsPreservesLargeIntegerChanges(t *testing.T) {
	for _, tc := range []struct{ name, stored, desired, action string }{
		{"different", `{"epoch":9007199254740992}`, `{"epoch":9007199254740993}`, "update"},
		{"negative", `{"epoch":-9223372036854775808}`, `{"epoch":-9223372036854775807}`, "update"},
		{"unchanged", `{"epoch":9007199254740993}`, `{"epoch":9007199254740993}`, "skip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "example.fixture.numeric"
			writes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/admin/extensions" {
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []extension.Envelope{{ModuleID: id, SchemaVersion: 1, Config: json.RawMessage(tc.stored), Source: "dynamic"}}})
					return
				}
				if r.Method == http.MethodPut && r.URL.Path == "/admin/extensions/"+id {
					writes++
					var e extension.Envelope
					if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
						t.Error(err)
					}
					if string(e.Config) != tc.desired {
						t.Errorf("changed numeric payload: %s", e.Config)
					}
					_ = json.NewEncoder(w).Encode(e)
					return
				}
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()
			bundle := &gatewaybundle.GatewayBundle{Extensions: []extension.Envelope{{ModuleID: id, SchemaVersion: 1, Config: json.RawMessage(tc.desired)}}}
			action := ""
			err := applyExtensions(context.Background(), adminclient.New(adminclient.Config{BaseURL: srv.URL}), bundle, func(_, _, a string, err error) {
				if err != nil {
					t.Error(err)
				}
				action = a
			})
			if err != nil {
				t.Fatal(err)
			}
			if action != tc.action {
				t.Fatalf("action=%s want %s", action, tc.action)
			}
			if (writes == 1) != (tc.action == "update") {
				t.Fatalf("writes=%d", writes)
			}
		})
	}
}
