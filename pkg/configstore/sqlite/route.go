package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore/intf"
	"gorm.io/gorm"
)

type routeRecord struct {
	ID        string    `gorm:"primaryKey"`
	Tag       string    `gorm:"index;not null;default:''"`
	Config    string    `gorm:"type:text;not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (routeRecord) TableName() string { return "routes" }

type RouteStore struct {
	*sqliteJSONStore
}

const defaultRouteTag = ""

func NewRouteStore(ctx context.Context, db *gorm.DB, decodeRoute intf.ConfigObjectDecoder) (*RouteStore, error) {
	if err := db.WithContext(ctx).AutoMigrate(&routeRecord{}); err != nil {
		return nil, fmt.Errorf("route store migrate: %w", err)
	}

	return &RouteStore{
		sqliteJSONStore: newSQLiteJSONStoreWithColumns(db, routeRecord{}.TableName(), "route", "id", "tag", "config", true, decodeRoute),
	}, nil
}

func (s *RouteStore) ListByTag(ctx context.Context, tag string) ([]any, error) {
	return s.sqliteJSONStore.ListByTag(ctx, tag)
}

func (s *RouteStore) ListByTagPrefix(ctx context.Context, tagPrefix string) ([]any, error) {
	return s.sqliteJSONStore.ListByTagPrefix(ctx, tagPrefix)
}

func (s *RouteStore) Create(ctx context.Context, id string, tag string, obj any) error {
	_, err := s.sqliteJSONStore.Create(ctx, id, tag, obj)
	return err
}

func (s *RouteStore) Update(ctx context.Context, id string, obj any) error {
	return s.sqliteJSONStore.Update(ctx, id, obj)
}

func (s *RouteStore) Delete(ctx context.Context, id string) error {
	return s.sqliteJSONStore.Delete(ctx, id)
}

func (s *RouteStore) Get(ctx context.Context, id string) (any, error) {
	_, value, err := s.sqliteJSONStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return value, nil
}
