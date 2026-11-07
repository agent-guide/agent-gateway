package sqlite

import (
	"context"
	"testing"

	configstore "github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"gorm.io/gorm"
)

func TestRegisterDefaultStoresRegistersAllSchemas(t *testing.T) {
	t.Helper()

	creator, err := Open(context.Background(), Config{
		SQLitePath: t.TempDir() + "/configstore.db",
	}, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	backend := configstore.NewBackend(creator)
	if err := schema.RegisterDefaultStores(backend); err != nil {
		t.Fatalf("register default stores: %v", err)
	}

	for _, name := range []string{
		schema.StoreProviders,
		schema.StoreCredentials,
		schema.StoreRoutes,
		schema.StoreVirtualKeys,
		schema.StoreManagedModels,
	} {
		if _, err := backend.Get(name); err != nil {
			t.Fatalf("build registered store %q: %v", name, err)
		}
	}
}

// TestBackendForwardsDatabase guards generic connection borrowing without
// giving the configuration backend responsibility for another domain schema.
func TestBackendForwardsDatabase(t *testing.T) {
	creator, err := Open(context.Background(), Config{
		SQLitePath: t.TempDir() + "/configstore.db",
	}, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	var backend configstore.ConfigStoreBackend = configstore.NewBackend(creator)
	provider, ok := backend.(interface{ Database() *gorm.DB })
	if !ok {
		t.Fatalf("generic backend does not expose its database")
	}
	if provider.Database() == nil {
		t.Fatalf("forwarded Database returned nil")
	}
}

func TestOpenEnablesWALJournalMode(t *testing.T) {
	creator, err := Open(t.Context(), Config{SQLitePath: t.TempDir() + "/configstore.db"}, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	var mode string
	if err := creator.db.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatalf("query journal mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}
