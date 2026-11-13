package ctlcmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	adminapi "github.com/agent-guide/agent-gateway/pkg/admin"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gatewaybundle"
)

func TestIndependentModelsApplyExportRoundTrip(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"object":"list","data":[{"id":"m","object":"model"}]}`)
	}))
	defer upstream.Close()
	creator, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = creator.Close() })
	backend := configstore.NewBackend(creator)
	if err := schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	registry, err := extension.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	g := gateway.NewAgentGateway()
	if err := g.Bootstrap(t.Context(), gateway.BootstrapOptions{ConfigStoreBackend: backend, ExtensionRegistry: registry}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	srv := httptest.NewServer(adminapi.NewHandler(g, nil))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "models.yaml")
	yaml := fmt.Sprintf(`apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: p
    provider_type: openai
    base_url: %s/v1
providerModels:
  - provider_id: p
    upstream_model: m
    enabled: true
logicalModels:
  - id: shared
    candidates:
      - provider_id: p
        upstream_model: m
llmRoutes:
  - id: r
    protocol: openai
    match_policy:
      path_prefix: /chat
    target_policy:
      type: models
      default_model: chat
      model_targets:
        - name: chat
          kind: logical-model
          logical_model_id: shared
  - id: concrete
    protocol: openai
    match_policy:
      path_prefix: /concrete
    target_policy:
      type: models
      default_model: m
      model_targets:
        - kind: provider-model
          provider_id: p
          upstream_model: m
`, upstream.URL)
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		args = append([]string{"--admin-addr", srv.URL, "--output", "json"}, args...)
		out, stderr, err := executeAGWCTL(t, args...)
		if err != nil {
			t.Fatalf("command %v: %v\n%s\n%s", args, err, out, stderr)
		}
		return out
	}
	first := run("apply", "-f", path)
	if !strings.Contains(first, `"logical_model"`) {
		t.Fatalf("logical resource missing from apply: %s", first)
	}
	run("model", "logical", "get", "shared")
	run("model", "provider", "get", "p", "m")
	exported := filepath.Join(t.TempDir(), "export.yaml")
	run("export", "-f", exported)
	b, err := gatewaybundle.LoadFile(exported)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ValidateForConfigStore(); err != nil {
		t.Fatal(err)
	}
	if len(b.ProviderModels) != 1 || len(b.LogicalModels) != 1 || len(b.LLMRoutes) != 2 {
		t.Fatalf("export lost model resources: %+v", b)
	}
	second := run("apply", "-f", exported)
	var summary gatewayApplySummary
	if err := json.Unmarshal([]byte(second), &summary); err != nil {
		t.Fatal(err)
	}
	for _, action := range summary.Actions {
		if action.Kind != "provider" && action.Action != "skip" {
			t.Fatalf("model/route round trip drift: %s", second)
		}
	}

}
