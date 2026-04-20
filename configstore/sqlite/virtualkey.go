package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"gorm.io/gorm"
)

type virtualKeyRecord struct {
	Key       string    `gorm:"primaryKey"`
	UserID    string    `gorm:"index;not null"`
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
		sqliteJSONStore: newSQLiteJSONStoreWithColumns(db, virtualKeyRecord{}.TableName(), "virtual key", "key", "user_id", "config", decodeVirtualKey),
	}, nil
}

func (s *VirtualKeyStore) ListByUserID(ctx context.Context, userID string) ([]any, error) {
	return s.sqliteJSONStore.ListByTagPrefix(ctx, userID)
}

func (s *VirtualKeyStore) Create(ctx context.Context, key string, userID string, obj any) error {
	_, err := s.sqliteJSONStore.Create(ctx, key, userID, obj)
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
