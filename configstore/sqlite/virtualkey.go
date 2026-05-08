package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/pkg/configstore/intf"
	"gorm.io/gorm"
)

type virtualKeyRecord struct {
	Key       string    `gorm:"primaryKey"`
	Tag       string    `gorm:"index;not null;default:''"`
	Config    string    `gorm:"type:text;not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (virtualKeyRecord) TableName() string { return "virtual_keys" }

type VirtualKeyStore struct {
	*sqliteJSONStore
}

func NewVirtualKeyStore(ctx context.Context, db *gorm.DB, decodeVirtualKey intf.ConfigObjectDecoder) (*VirtualKeyStore, error) {
	if err := db.WithContext(ctx).AutoMigrate(&virtualKeyRecord{}); err != nil {
		return nil, fmt.Errorf("virtual key store migrate: %w", err)
	}
	return &VirtualKeyStore{
		sqliteJSONStore: newSQLiteJSONStoreWithColumns(db, virtualKeyRecord{}.TableName(), "virtual key", "key", "tag", "config", decodeVirtualKey),
	}, nil
}

func (s *VirtualKeyStore) ListByTag(ctx context.Context, tag string) ([]any, error) {
	return s.sqliteJSONStore.ListByTag(ctx, tag)
}

func (s *VirtualKeyStore) Create(ctx context.Context, key string, tag string, obj any) error {
	_, err := s.sqliteJSONStore.Create(ctx, key, tag, obj)
	return err
}

func (s *VirtualKeyStore) Update(ctx context.Context, key string, obj any) error {
	return s.sqliteJSONStore.Update(ctx, key, obj)
}

func (s *VirtualKeyStore) Delete(ctx context.Context, key string) error {
	return s.sqliteJSONStore.Delete(ctx, key)
}

func (s *VirtualKeyStore) Get(ctx context.Context, key string) (any, error) {
	_, value, err := s.sqliteJSONStore.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	return value, nil
}
