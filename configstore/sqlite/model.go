package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/agent-guide/caddy-agent-gateway/pkg/configstore/intf"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type sqliteModelRecord struct {
	ProviderID    string `gorm:"primaryKey;column:provider_id"`
	UpstreamModel string `gorm:"primaryKey;column:upstream_model"`
	Data          string `gorm:"type:text;not null;column:data"`
}

type ModelStore struct {
	db     *gorm.DB
	decode intf.ConfigObjectDecoder
}

func NewModelStore(ctx context.Context, db *gorm.DB, decode intf.ConfigObjectDecoder) (*ModelStore, error) {
	store := &ModelStore{db: db, decode: decode}
	if err := db.WithContext(ctx).Table("model_configs").AutoMigrate(&sqliteModelRecord{}); err != nil {
		return nil, fmt.Errorf("auto-migrate model_configs: %w", err)
	}
	return store, nil
}

func (s *ModelStore) List(ctx context.Context) ([]any, error) {
	var rows []sqliteModelRecord
	if err := s.db.WithContext(ctx).
		Table("model_configs").
		Order("provider_id asc, upstream_model asc").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]any, 0, len(rows))
	for _, row := range rows {
		obj, err := s.decode([]byte(row.Data))
		if err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, nil
}

func (s *ModelStore) Get(ctx context.Context, providerID string, upstreamModel string) (any, bool, error) {
	var row sqliteModelRecord
	err := s.db.WithContext(ctx).
		Table("model_configs").
		Where("provider_id = ? AND upstream_model = ?", providerID, upstreamModel).
		First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	obj, err := s.decode([]byte(row.Data))
	if err != nil {
		return nil, false, err
	}
	return obj, true, nil
}

func (s *ModelStore) Upsert(ctx context.Context, obj any) error {
	providerID, upstreamModel, data, err := modelRecordParts(obj)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).
		Table("model_configs").
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider_id"}, {Name: "upstream_model"}},
			DoUpdates: clause.AssignmentColumns([]string{"data"}),
		}).
		Create(&sqliteModelRecord{
			ProviderID:    providerID,
			UpstreamModel: upstreamModel,
			Data:          data,
		}).Error
}

func (s *ModelStore) Delete(ctx context.Context, providerID string, upstreamModel string) error {
	return s.db.WithContext(ctx).
		Table("model_configs").
		Where("provider_id = ? AND upstream_model = ?", providerID, upstreamModel).
		Delete(&sqliteModelRecord{}).Error
}

func modelRecordParts(obj any) (string, string, string, error) {
	keyer, ok := obj.(intf.ModelStorageKeyer)
	if !ok || keyer == nil {
		return "", "", "", fmt.Errorf("model config type %T does not provide a storage key", obj)
	}
	providerID, upstreamModel := keyer.ModelStorageKey()
	if providerID == "" || upstreamModel == "" {
		return "", "", "", fmt.Errorf("provider_id and upstream_model are required")
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return "", "", "", err
	}
	return providerID, upstreamModel, string(data), nil
}
