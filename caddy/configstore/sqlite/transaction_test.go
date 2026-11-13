package sqlite

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
)

func TestCaddyAdapterPreservesTransactionRollback(t *testing.T) {
	creator, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: filepath.Join(t.TempDir(), "config.db")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, _ := creator.Database().DB()
	t.Cleanup(func() { _ = db.Close() })
	adapter := &SQLiteConfigStoreBackend{backend: configstore.NewBackend(creator)}
	if err = schema.RegisterDefaultStores(adapter); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("rollback through Caddy")
	entered := false
	err = adapter.Transaction(t.Context(), []string{schema.StoreProviders, schema.StoreProviderModels}, func(stores map[string]configstore.ConfigStore) error {
		entered = true
		if stores[schema.StoreProviders] == nil || stores[schema.StoreProviderModels] == nil {
			t.Fatal("missing transaction stores")
		}
		return sentinel
	})
	if !entered || !errors.Is(err, sentinel) {
		t.Fatalf("Caddy lost transaction capability: %v", err)
	}
}
