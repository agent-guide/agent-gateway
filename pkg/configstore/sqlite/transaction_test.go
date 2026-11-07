package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func TestTransactionProviderAndModelDeletionIsAtomic(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			ctx := context.Background()
			backend := newTestBackend(t)
			if err := backend.Register(schema.StoreProviders, schema.ProviderConfigSchema); err != nil {
				t.Fatal(err)
			}
			if err := backend.Register(schema.StoreManagedModels, schema.ManagedModelSchema); err != nil {
				t.Fatal(err)
			}
			providers, _ := backend.Get(schema.StoreProviders)
			models, _ := backend.Get(schema.StoreManagedModels)
			if err := providers.Create(ctx, &provider.ProviderConfig{Id: "p", ProviderType: "openai"}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"a", "b"} {
				if err := models.Create(ctx, &modelcatalog.ManagedModel{ProviderID: "p", UpstreamModel: name}); err != nil {
					t.Fatal(err)
				}
			}
			if fail {
				db := models.(*JSONConfigStore).db
				if err := db.Exec(`CREATE TRIGGER reject_model_delete BEFORE DELETE ON model_configs WHEN OLD.upstream_model='b' BEGIN SELECT RAISE(ABORT,'injected cleanup failure'); END`).Error; err != nil {
					t.Fatal(err)
				}
			}
			err := backend.(configstore.TransactionalBackend).Transaction(ctx, []string{schema.StoreProviders, schema.StoreManagedModels}, func(stores map[string]configstore.ConfigStore) error {
				if err := stores[schema.StoreProviders].Delete(ctx, "p"); err != nil {
					return err
				}
				for _, name := range []string{"a", "b"} {
					if err := stores[schema.StoreManagedModels].Delete(ctx, "p", name); err != nil {
						return err
					}
				}
				if _, err := stores[schema.StoreProviders].Get(ctx, "p"); !errors.Is(err, configstore.ErrNotFound) {
					t.Fatalf("transaction readback = %v", err)
				}
				return nil
			})
			if (err != nil) != fail {
				t.Fatalf("transaction error = %v", err)
			}
			_, providerErr := providers.Get(ctx, "p")
			remaining, err := models.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if providerErr != nil || len(remaining) != 2 {
					t.Fatal("transaction partially deleted configuration")
				}
			} else if !errors.Is(providerErr, configstore.ErrNotFound) || len(remaining) != 0 {
				t.Fatal("transaction did not commit all deletions")
			}
		})
	}
}

func TestTransactionReadbackFailureAndInvalidStores(t *testing.T) {
	ctx := context.Background()
	backend := newTestBackend(t)
	if err := backend.Register(schema.StoreProviders, schema.ProviderConfigSchema); err != nil {
		t.Fatal(err)
	}
	store, _ := backend.Get(schema.StoreProviders)
	if err := store.Create(ctx, &provider.ProviderConfig{Id: "p", ProviderType: "openai"}); err != nil {
		t.Fatal(err)
	}
	tx := backend.(configstore.TransactionalBackend)
	injected := errors.New("readback failed")
	if err := tx.Transaction(ctx, []string{schema.StoreProviders}, func(stores map[string]configstore.ConfigStore) error {
		if err := stores[schema.StoreProviders].Delete(ctx, "p"); err != nil {
			return err
		}
		return injected
	}); !errors.Is(err, injected) {
		t.Fatalf("error = %v", err)
	}
	if _, err := store.Get(ctx, "p"); err != nil {
		t.Fatal("failed callback did not roll back")
	}
	for _, names := range [][]string{{schema.StoreProviders, "missing"}, {schema.StoreProviders, schema.StoreProviders}, nil} {
		if err := tx.Transaction(ctx, names, func(map[string]configstore.ConfigStore) error {
			t.Fatal("invalid transaction invoked callback")
			return nil
		}); err == nil {
			t.Fatal("invalid transaction accepted")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := tx.Transaction(canceled, []string{schema.StoreProviders}, func(map[string]configstore.ConfigStore) error {
		t.Fatal("canceled transaction invoked callback")
		return nil
	}); err == nil {
		t.Fatal("canceled transaction accepted")
	}
}
