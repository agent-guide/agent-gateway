package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"gorm.io/gorm"
)

type providerConfigRecord struct {
	ID        string    `gorm:"primaryKey"`
	Tag       string    `gorm:"index;not null"` // provider type
	Config    string    `gorm:"type:text;not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (providerConfigRecord) TableName() string { return "providers" }

type ProviderConfigStore struct {
	*sqliteJSONStore
}

func NewProviderConfigStore(ctx context.Context, db *gorm.DB, decodeProviderConfig intf.ConfigObjectDecoder) (*ProviderConfigStore, error) {
	if err := db.WithContext(ctx).AutoMigrate(&providerConfigRecord{}); err != nil {
		return nil, fmt.Errorf("provider config store migrate: %w", err)
	}

	return &ProviderConfigStore{
		sqliteJSONStore: newSQLiteJSONStoreWithColumns(
			db,
			providerConfigRecord{}.TableName(),
			"provider config",
			"id",
			"tag",
			"config",
			decodeProviderConfig,
		),
	}, nil
}

func (s *ProviderConfigStore) ListByType(ctx context.Context, name string) ([]any, error) {
	return s.sqliteJSONStore.ListByTag(ctx, name)
}

func (s *ProviderConfigStore) Create(ctx context.Context, id string, name string, obj any) (string, error) {
	return s.sqliteJSONStore.Create(ctx, id, name, obj)
}

func (s *ProviderConfigStore) Update(ctx context.Context, id string, obj any) error {
	return s.sqliteJSONStore.Update(ctx, id, obj)
}

func (s *ProviderConfigStore) Delete(ctx context.Context, id string) error {
	return s.sqliteJSONStore.Delete(ctx, id)
}

func (s *ProviderConfigStore) Get(ctx context.Context, id string) (string, any, error) {
	return s.sqliteJSONStore.Get(ctx, id)
}
