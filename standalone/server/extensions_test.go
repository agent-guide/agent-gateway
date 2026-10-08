package server

import (
	"context"
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/admin"
	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"go.uber.org/zap"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type extensionResource struct{ value string }

func (*extensionResource) Health(context.Context) error { return nil }
func (*extensionResource) Close(context.Context) error  { return nil }
func extensionAssembly() distribution.Assembly {
	a := distribution.CommunityRuntime()
	a.Modules = append(a.Modules, extension.Descriptor{ID: "example.fixture.secret", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime", "validation"}})
	a.Factories = map[string]*extension.Factory{"example.fixture.secret": {SecretFields: []string{"/token"}, Validate: func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) { return raw, nil }, Prepare: func(_ context.Context, c extension.PreparedConfig, _ extension.Snapshot) (extension.Resource, error) {
		return &extensionResource{value: c.Secrets["/token"]}, nil
	}}}
	return a
}
func TestExtensionAdminAndStartup(t *testing.T) {
	ctx := context.Background()
	a := extensionAssembly()
	r, err := a.Registry()
	if err != nil {
		t.Fatal(err)
	}
	g, svc, err := bootstrapGateway(ctx, Options{Assembly: a, Registry: r, ConfigStorePath: filepath.Join(t.TempDir(), "db.sqlite")}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	defer svc.Close()
	srv := httptest.NewServer(admin.NewHandler(g, zap.NewNop()))
	defer srv.Close()
	client := adminclient.New(adminclient.Config{BaseURL: srv.URL})
	manifest, err := client.Manifest(ctx)
	if err != nil || len(manifest.Modules) != len(a.Modules) {
		t.Fatal(err)
	}
	e := extension.Envelope{ModuleID: "example.fixture.secret", SchemaVersion: 1, Config: json.RawMessage(`{"token":{"$secret_ref":"env:STANDALONE_F2_TEST_TOKEN"}}`)}
	t.Setenv("STANDALONE_F2_TEST_TOKEN", "do-not-export")
	if _, err = client.PutExtension(ctx, e); err != nil {
		t.Fatal(err)
	}
	lease, err := g.Extensions().Acquire()
	if err != nil {
		t.Fatal(err)
	}
	res, _ := lease.Snapshot.Resource(e.ModuleID)
	if res.(*extensionResource).value != "do-not-export" {
		t.Fatal("resolution missing")
	}
	lease.Release()
	exported, err := client.ListExtensions(ctx)
	if err != nil || len(exported) != 1 {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(exported)
	if strings.Contains(string(raw), "do-not-export") || exported[0].Source != "dynamic" {
		t.Fatalf("bad envelope: %s", raw)
	}
	_ = os.Unsetenv("STANDALONE_F2_TEST_TOKEN")
	if _, err = client.PutExtension(ctx, e); err == nil || strings.Contains(err.Error(), "do-not-export") {
		t.Fatal("missing reference accepted or leaked")
	}
	if len(g.Extensions().List()) != 1 {
		t.Fatal("failed apply removed old state")
	}
	if err = client.DeleteExtension(ctx, e.ModuleID); err != nil {
		t.Fatal(err)
	}
	e.ModuleID = "example.fixture.unknown"
	if _, err = client.PutExtension(ctx, e); err == nil {
		t.Fatal("unknown accepted")
	}
}
