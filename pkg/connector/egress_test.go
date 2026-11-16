package connector_test

import (
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/connector"
	"github.com/agent-guide/agent-gateway/pkg/connector/egress"
)

func TestMCPEgressModes(t *testing.T) {
	for _, transport := range []string{"streamable", "sse"} {
		for _, mode := range []string{"unrestricted", "public_only", "exception"} {
			t.Run(transport+"/"+mode, func(t *testing.T) {
				var cfg connector.ConnectorConfig
				if transport == "sse" {
					cfg = newFakeSSEConfig(t, "egress")
				} else {
					cfg, _ = newStreamableHTTPTestConfig(t, "egress")
				}
				manager := connector.NewManager(newTestConfigStore(t))
				t.Cleanup(manager.Close)
				p := egress.Policy{Mode: mode}
				if mode == "exception" {
					u, _ := url.Parse(cfg.MCP.URL)
					p = egress.Policy{Mode: "public_only", AllowOrigins: []string{u.Scheme + "://" + u.Host}}
				}
				if err := manager.ConfigureEgress(p); err != nil {
					t.Fatal(err)
				}
				err := manager.Create(t.Context(), cfg)
				if mode == "public_only" {
					if err == nil {
						t.Fatal("restricted MCP local destination accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := manager.Initialize(t.Context(), cfg.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := manager.ListTools(t.Context(), cfg.ID); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestMCPSSEPostURLAdmission(t *testing.T) {
	cfg := newFakeSSEConfig(t, "egress-post")
	cfg.MCP.URL = "https://example.com/sse"
	manager := connector.NewManager(newTestConfigStore(t))
	t.Cleanup(manager.Close)
	if err := manager.ConfigureEgress(egress.Policy{Mode: "public_only"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Create(t.Context(), cfg); err == nil {
		t.Fatal("restricted SSE POST endpoint accepted")
	}
}

func TestMCPRedirectCannotEscapePolicy(t *testing.T) {
	for _, tr := range []basemcp.TransportType{connector.TransportSSE, connector.TransportStreamableHTTP} {
		t.Run(string(tr), func(t *testing.T) {
			var calls atomic.Int32
			blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
			defer blocked.Close()
			allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, blocked.URL, http.StatusTemporaryRedirect)
			}))
			defer allowed.Close()
			manager := connector.NewManager(newTestConfigStore(t))
			t.Cleanup(manager.Close)
			if err := manager.ConfigureEgress(egress.Policy{Mode: "public_only", AllowOrigins: []string{allowed.URL}}); err != nil {
				t.Fatal(err)
			}
			cfg := connector.ConnectorConfig{ID: "redirect", Name: "Redirect", Type: "mcp", MCP: &connectormcp.Config{Transport: tr, URL: allowed.URL}}
			if err := manager.Create(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Initialize(t.Context(), cfg.ID); err == nil {
				t.Fatal("redirect escaped policy")
			}
			if calls.Load() != 0 {
				t.Fatalf("blocked destination received %d requests", calls.Load())
			}
		})
	}
}

func TestEgressPolicyCannotChangeAfterManagerUse(t *testing.T) {
	manager := connector.NewManager(newTestConfigStore(t))
	t.Cleanup(manager.Close)
	if err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureEgress(egress.Policy{Mode: "public_only"}); err == nil {
		t.Fatal("running manager accepted policy replacement")
	}
}
