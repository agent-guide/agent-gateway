package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	configstoreintf "github.com/agent-guide/agent-gateway/pkg/configstore/intf"
	"github.com/agent-guide/agent-gateway/pkg/gateway/route"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRouteStoreTagColumn(t *testing.T) {
	ctx := context.Background()

	db, err := gorm.Open(sqlite.Open("file:route_store_timestamps?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	store, err := NewRouteStore(ctx, db, route.DecodeStoredRoute)
	if err != nil {
		t.Fatalf("new route store: %v", err)
	}

	want := &route.AgentRoute{
		ID: "chat-prod",
		TargetPolicy: &route.RouteDirectProviderPolicy{
			ProviderTarget: route.DirectProviderTarget{ProviderID: "openai"},
		},
	}

	if err := store.Create(ctx, want.ID, defaultRouteTag, want); err != nil {
		t.Fatalf("create route: %v", err)
	}

	items, err := store.ListByTag(ctx, defaultRouteTag)
	if err != nil {
		t.Fatalf("list routes: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("list routes len = %d, want 1", len(items))
	}

	gotAny, err := store.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("get route: %v", err)
	}

	got, ok := gotAny.(*route.AgentRoute)
	if !ok {
		t.Fatalf("get route type = %T, want *route.AgentRoute", gotAny)
	}
	if got.ID != want.ID {
		t.Fatalf("get route id = %q, want %q", got.ID, want.ID)
	}

	if err := store.Delete(ctx, want.ID); err != nil {
		t.Fatalf("delete route: %v", err)
	}

	items, err = store.ListByTag(ctx, defaultRouteTag)
	if err != nil {
		t.Fatalf("list routes after delete: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("list routes after delete len = %d, want 0", len(items))
	}
}

func TestRouteStoreCreateRejectsDuplicateID(t *testing.T) {
	ctx := context.Background()

	db, err := gorm.Open(sqlite.Open("file:route_store_timestamps?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	store, err := NewRouteStore(ctx, db, route.DecodeStoredRoute)
	if err != nil {
		t.Fatalf("new route store: %v", err)
	}

	item := &route.AgentRoute{
		ID: "chat-prod",
		TargetPolicy: &route.RouteDirectProviderPolicy{
			ProviderTarget: route.DirectProviderTarget{ProviderID: "openai"},
		},
	}

	if err := store.Create(ctx, item.ID, defaultRouteTag, item); err != nil {
		t.Fatalf("create route: %v", err)
	}

	err = store.Create(ctx, item.ID, defaultRouteTag, item)
	if err == nil {
		t.Fatal("expected duplicate create to fail")
	}
}

func TestRouteStoreUpdateRejectsMissingID(t *testing.T) {
	ctx := context.Background()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	store, err := NewRouteStore(ctx, db, route.DecodeStoredRoute)
	if err != nil {
		t.Fatalf("new route store: %v", err)
	}

	err = store.Update(ctx, "missing", &route.AgentRoute{
		ID: "missing",
		TargetPolicy: &route.RouteDirectProviderPolicy{
			ProviderTarget: route.DirectProviderTarget{ProviderID: "openai"},
		},
	})
	if !errors.Is(err, configstoreintf.ErrNotFound) {
		t.Fatalf("update missing route error = %v, want %v", err, configstoreintf.ErrNotFound)
	}
}

func TestRouteStoreMaintainsDBTimestamps(t *testing.T) {
	ctx := context.Background()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	store, err := NewRouteStore(ctx, db, route.DecodeStoredRoute)
	if err != nil {
		t.Fatalf("new route store: %v", err)
	}

	item := &route.AgentRoute{
		ID: "chat-prod",
		TargetPolicy: &route.RouteDirectProviderPolicy{
			ProviderTarget: route.DirectProviderTarget{ProviderID: "openai"},
		},
	}

	if err := store.Create(ctx, item.ID, defaultRouteTag, item); err != nil {
		t.Fatalf("create route: %v", err)
	}

	var created routeRecord
	if err := db.WithContext(ctx).Where("id = ?", item.ID).First(&created).Error; err != nil {
		t.Fatalf("load created row: %v", err)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("created row CreatedAt is zero")
	}
	if created.UpdatedAt.IsZero() {
		t.Fatal("created row UpdatedAt is zero")
	}
	initialCreatedAt := created.CreatedAt
	initialUpdatedAt := created.UpdatedAt

	time.Sleep(10 * time.Millisecond)

	item.Description = "updated"
	if err := store.Update(ctx, item.ID, item); err != nil {
		t.Fatalf("update route: %v", err)
	}

	var updated routeRecord
	if err := db.WithContext(ctx).Where("id = ?", item.ID).First(&updated).Error; err != nil {
		t.Fatalf("load updated row: %v", err)
	}
	if !updated.CreatedAt.Equal(initialCreatedAt) {
		t.Fatalf("updated CreatedAt = %v, want %v", updated.CreatedAt, initialCreatedAt)
	}
	if !updated.UpdatedAt.After(initialUpdatedAt) {
		t.Fatalf("updated UpdatedAt = %v, want after %v", updated.UpdatedAt, initialUpdatedAt)
	}
}
