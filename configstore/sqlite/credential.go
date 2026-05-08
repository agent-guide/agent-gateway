package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/pkg/configstore/intf"
	"gorm.io/gorm"
)

type credentialRecord struct {
	ID        string    `gorm:"primaryKey"`
	Tag       string    `gorm:"index;not null"`
	Data      string    `gorm:"type:text;not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (credentialRecord) TableName() string { return "cliauth_credentials" }

// CredentialStore wraps RDBStore and implements intf.CredentialStorer.
type CredentialStore struct {
	*sqliteJSONStore
}

// NewCredentialStore creates a CredentialStore and runs auto-migration.
func NewCredentialStore(ctx context.Context, db *gorm.DB, decodeCredential intf.ConfigObjectDecoder) (*CredentialStore, error) {
	if err := db.WithContext(ctx).AutoMigrate(&credentialRecord{}); err != nil {
		return nil, fmt.Errorf("credential store migrate: %w", err)
	}
	return &CredentialStore{
		sqliteJSONStore: newSQLiteJSONStore(db, credentialRecord{}.TableName(), "credential", decodeCredential),
	}, nil
}

func (s *CredentialStore) ListByProviderType(ctx context.Context, providerType string) ([]any, error) {
	return s.sqliteJSONStore.ListByTagPrefix(ctx, providerType)
}

func (s *CredentialStore) Create(ctx context.Context, id string, providerType string, obj any) (string, error) {
	return s.sqliteJSONStore.Create(ctx, id, providerType, obj)
}

func (s *CredentialStore) Update(ctx context.Context, id string, obj any) error {
	return s.sqliteJSONStore.Update(ctx, id, obj)
}

func (s *CredentialStore) Get(ctx context.Context, id string) (string, any, error) {
	return s.sqliteJSONStore.Get(ctx, id)
}
