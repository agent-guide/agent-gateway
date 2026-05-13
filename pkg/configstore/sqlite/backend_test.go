package sqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	modelcatalog "github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestStoreCreateAndListProviderByTag(t *testing.T) {
	ctx := context.Background()
	backend := newTestBackend(t)
	if err := backend.Register(schema.StoreProviders, schema.ProviderConfigSchema); err != nil {
		t.Fatalf("register schema: %v", err)
	}

	store, err := backend.Get(schema.StoreProviders)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	cfg := &provider.ProviderConfig{Id: "openai-main", ProviderType: "openai", BaseURL: "https://api.example"}
	if err := store.Create(ctx, cfg); err != nil {
		t.Fatalf("create: %v", err)
	}

	items, err := store.ListByTag(ctx, "openai")
	if err != nil {
		t.Fatalf("list by tag: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("list by tag len = %d, want 1", len(items))
	}

	gotAny, err := store.Get(ctx, "openai-main")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, ok := gotAny.(*provider.ProviderConfig)
	if !ok {
		t.Fatalf("get type = %T", gotAny)
	}
	if got.Id != cfg.Id || got.ProviderType != "openai" {
		t.Fatalf("get = %#v", got)
	}
}

func TestStoreGetByIndexVirtualKey(t *testing.T) {
	ctx := context.Background()
	backend := newTestBackend(t)
	if err := backend.Register(schema.StoreVirtualKeys, schema.VirtualKeySchema); err != nil {
		t.Fatalf("register schema: %v", err)
	}

	store, err := backend.Get(schema.StoreVirtualKeys)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	key := &virtualkeypkg.VirtualKey{ID: "vk-1", Key: "secret-1", Tag: "team-a"}
	if err := store.Create(ctx, key); err != nil {
		t.Fatalf("create: %v", err)
	}

	gotAny, err := store.GetByIndex(ctx, "key", "secret-1")
	if err != nil {
		t.Fatalf("get by index: %v", err)
	}
	got, ok := gotAny.(*virtualkeypkg.VirtualKey)
	if !ok {
		t.Fatalf("get by index type = %T", gotAny)
	}
	if got.ID != "vk-1" || got.Key != "secret-1" {
		t.Fatalf("get by index = %#v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not applied: %#v", got)
	}
}

func TestStoreCompositePrimaryKeyManagedModel(t *testing.T) {
	ctx := context.Background()
	backend := newTestBackend(t)
	if err := backend.Register(schema.StoreManagedModels, schema.ManagedModelSchema); err != nil {
		t.Fatalf("register schema: %v", err)
	}

	store, err := backend.Get(schema.StoreManagedModels)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	model := &modelcatalog.ManagedModel{ProviderID: "openai-main", UpstreamModel: "gpt-4.1", Enabled: true}
	if err := store.Create(ctx, model); err != nil {
		t.Fatalf("create: %v", err)
	}

	gotAny, err := store.Get(ctx, "openai-main", "gpt-4.1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, ok := gotAny.(*modelcatalog.ManagedModel)
	if !ok {
		t.Fatalf("get type = %T", gotAny)
	}
	if got.ProviderID != "openai-main" || got.UpstreamModel != "gpt-4.1" {
		t.Fatalf("get = %#v", got)
	}

	if err := store.Delete(ctx, "openai-main", "gpt-4.1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, "openai-main", "gpt-4.1"); !errors.Is(err, configstore.ErrNotFound) {
		t.Fatalf("get deleted error = %v", err)
	}
}

func TestStoreInvalidKeyPartCount(t *testing.T) {
	ctx := context.Background()
	backend := newTestBackend(t)
	if err := backend.Register(schema.StoreManagedModels, schema.ManagedModelSchema); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	store, err := backend.Get(schema.StoreManagedModels)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	if _, err := store.Get(ctx, "only-one-part"); !errors.Is(err, ErrInvalidKeyParts) {
		t.Fatalf("get invalid key error = %v", err)
	}
}

func TestStoreUnknownIndexName(t *testing.T) {
	ctx := context.Background()
	backend := newTestBackend(t)
	if err := backend.Register(schema.StoreVirtualKeys, schema.VirtualKeySchema); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	store, err := backend.Get(schema.StoreVirtualKeys)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	if _, err := store.GetByIndex(ctx, "missing", "value"); !errors.Is(err, ErrUnknownIndexName) {
		t.Fatalf("unknown index error = %v", err)
	}
}

func TestStoreTagUnsupported(t *testing.T) {
	ctx := context.Background()
	backend := newTestBackend(t)
	if err := backend.Register(schema.StoreManagedModels, schema.ManagedModelSchema); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	store, err := backend.Get(schema.StoreManagedModels)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	if _, err := store.ListByTag(ctx, "x"); !errors.Is(err, ErrTagUnsupported) {
		t.Fatalf("list by tag error = %v", err)
	}
}

func TestStoreUpdateMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	backend := newTestBackend(t)
	if err := backend.Register(schema.StoreProviders, schema.ProviderConfigSchema); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	store, err := backend.Get(schema.StoreProviders)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	err = store.Update(ctx, &provider.ProviderConfig{Id: "missing", ProviderType: "openai"})
	if !errors.Is(err, configstore.ErrNotFound) {
		t.Fatalf("update missing error = %v", err)
	}
}

func TestStoreTimestampUpdatePreservesCreatedAt(t *testing.T) {
	ctx := context.Background()
	backend := newTestBackend(t)
	if err := backend.Register(schema.StoreVirtualKeys, schema.VirtualKeySchema); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	store, err := backend.Get(schema.StoreVirtualKeys)
	if err != nil {
		t.Fatalf("build store: %v", err)
	}

	key := &virtualkeypkg.VirtualKey{ID: "vk-1", Key: "secret-1", Tag: "team-a"}
	if err := store.Create(ctx, key); err != nil {
		t.Fatalf("create: %v", err)
	}
	createdAny, err := store.Get(ctx, "vk-1")
	if err != nil {
		t.Fatalf("get created: %v", err)
	}
	created := createdAny.(*virtualkeypkg.VirtualKey)
	initialCreatedAt := created.CreatedAt
	initialUpdatedAt := created.UpdatedAt

	time.Sleep(10 * time.Millisecond)

	key.Description = "updated"
	if err := store.Update(ctx, key); err != nil {
		t.Fatalf("update: %v", err)
	}
	updatedAny, err := store.Get(ctx, "vk-1")
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	updated := updatedAny.(*virtualkeypkg.VirtualKey)
	if !updated.CreatedAt.Equal(initialCreatedAt) {
		t.Fatalf("updated created_at = %v, want %v", updated.CreatedAt, initialCreatedAt)
	}
	if !updated.UpdatedAt.After(initialUpdatedAt) {
		t.Fatalf("updated updated_at = %v, want after %v", updated.UpdatedAt, initialUpdatedAt)
	}
}

func newTestBackend(t *testing.T) configstore.ConfigStoreBackend {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	return configstore.NewBackend(&SQLiteConfigStoreCreator{db: db})
}
