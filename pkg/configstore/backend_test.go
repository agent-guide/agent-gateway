package configstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
)

func TestBackendRegisterAndGet(t *testing.T) {
	store := fakeConfigStore{}
	backend := configstore.NewBackend(fakeConfigStoreCreator{store: store})

	if err := backend.Register(schema.StoreProviders, schema.ProviderConfigSchema); err != nil {
		t.Fatalf("register schema: %v", err)
	}

	got, err := backend.Get(schema.StoreProviders)
	if err != nil {
		t.Fatalf("get store: %v", err)
	}
	if got != store {
		t.Fatalf("get store = %T, want fakeConfigStore", got)
	}
}

func TestBackendGetUnknownStore(t *testing.T) {
	backend := configstore.NewBackend(fakeConfigStoreCreator{store: fakeConfigStore{}})
	if _, err := backend.Get("missing"); !errors.Is(err, configstore.ErrUnknownStoreName) {
		t.Fatalf("get unknown error = %v", err)
	}
}

func TestBackendRegisterDuplicateStore(t *testing.T) {
	backend := configstore.NewBackend(fakeConfigStoreCreator{store: fakeConfigStore{}})
	if err := backend.Register(schema.StoreProviders, schema.ProviderConfigSchema); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := backend.Register(schema.StoreProviders, schema.ProviderConfigSchema); !errors.Is(err, configstore.ErrStoreAlreadyRegistered) {
		t.Fatalf("second register error = %v", err)
	}
}

type fakeConfigStoreCreator struct {
	store configstore.ConfigStore
}

func (c fakeConfigStoreCreator) NewStore(configstore.StoreSchema) (configstore.ConfigStore, error) {
	return c.store, nil
}

type fakeConfigStore struct{}

func (fakeConfigStore) List(context.Context) ([]any, error) {
	return nil, nil
}

func (fakeConfigStore) ListByTag(context.Context, string) ([]any, error) {
	return nil, nil
}

func (fakeConfigStore) ListByTagPrefix(context.Context, string) ([]any, error) {
	return nil, nil
}

func (fakeConfigStore) Create(context.Context, any) error {
	return nil
}

func (fakeConfigStore) Update(context.Context, any) error {
	return nil
}

func (fakeConfigStore) Delete(context.Context, ...any) error {
	return nil
}

func (fakeConfigStore) Get(context.Context, ...any) (any, error) {
	return nil, nil
}

func (fakeConfigStore) GetByIndex(context.Context, string, any) (any, error) {
	return nil, nil
}
