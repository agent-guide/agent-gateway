package sqlite

import (
	"context"
	"fmt"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"gorm.io/gorm"
)

func (s *SQLiteConfigStoreCreator) Transaction(ctx context.Context, schemas []configstore.StoreSchema, apply func([]configstore.ConfigStore) error) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite config store backend db is nil")
	}
	if apply == nil {
		return fmt.Errorf("transaction callback is required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stores := make([]configstore.ConfigStore, len(schemas))
		for i, schema := range schemas {
			stores[i] = &JSONConfigStore{db: tx, schema: schema}
		}
		return apply(stores)
	})
}

var _ configstore.TransactionalCreator = (*SQLiteConfigStoreCreator)(nil)

func (s *JSONConfigStore) Transaction(ctx context.Context, apply func(configstore.ConfigStore) error) error {
	if apply == nil {
		return fmt.Errorf("transaction callback is required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return apply(&JSONConfigStore{db: tx, schema: s.schema}) })
}
