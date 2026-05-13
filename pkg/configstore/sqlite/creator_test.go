package sqlite

import (
	"context"
	"testing"

	configstore "github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
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
