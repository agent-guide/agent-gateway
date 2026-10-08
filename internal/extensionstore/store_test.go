package extensionstore

import (
	"context"
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	sqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"go.uber.org/zap"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteEnvelopeRoundTrip(t *testing.T) {
	ctx := context.Background()
	backend, err := configstore.OpenBackend(ctx, "sqlite", sqlite.Config{SQLitePath: filepath.Join(t.TempDir(), "config.db")}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	store, err := New(backend)
	if err != nil {
		t.Fatal(err)
	}
	e := extension.Envelope{ModuleID: "example.fixture.secret", SchemaVersion: 1, Config: json.RawMessage(`{"token":{"$secret_ref":"env:SQLITE_F2_SECRET"}}`), Source: "dynamic"}
	t.Setenv("SQLITE_F2_SECRET", "private-value")
	if err = store.Put(ctx, e, true); err != nil {
		t.Fatal(err)
	}
	items, err := store.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(items)
	if strings.Contains(string(raw), "private-value") || items[0].Source != "" {
		t.Fatalf("persisted runtime value: %s", raw)
	}
	if err = store.Put(ctx, e, false); err != nil {
		t.Fatal(err)
	}
	if err = store.Delete(ctx, e.ModuleID); err != nil {
		t.Fatal(err)
	}
	items, err = store.List(ctx)
	if err != nil || len(items) != 0 {
		t.Fatal(err)
	}
}
