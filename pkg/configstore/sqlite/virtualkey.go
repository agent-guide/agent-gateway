package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore/intf"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"gorm.io/gorm"
)

type virtualKeyRecord struct {
	ID        string    `gorm:"primaryKey"`
	Key       string    `gorm:"uniqueIndex;not null"`
	Tag       string    `gorm:"index;not null;default:''"`
	Config    string    `gorm:"type:text;not null"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (virtualKeyRecord) TableName() string { return "virtual_keys" }

type VirtualKeyStore struct {
	db               *gorm.DB
	decodeVirtualKey intf.ConfigObjectDecoder
}

func NewVirtualKeyStore(ctx context.Context, db *gorm.DB, decodeVirtualKey intf.ConfigObjectDecoder) (*VirtualKeyStore, error) {
	if err := db.WithContext(ctx).AutoMigrate(&virtualKeyRecord{}); err != nil {
		return nil, fmt.Errorf("virtual key store migrate: %w", err)
	}
	return &VirtualKeyStore{db: db, decodeVirtualKey: decodeVirtualKey}, nil
}

func (s *VirtualKeyStore) ListByTag(ctx context.Context, tag string) ([]any, error) {
	var rows []virtualKeyRecord
	query := s.db.WithContext(ctx).Order("id asc")
	if tag != "" {
		query = query.Where("tag = ?", tag)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return s.decodeRows(rows)
}

func (s *VirtualKeyStore) Create(ctx context.Context, id string, tag string, obj any) error {
	row, err := s.newRecord(id, tag, obj)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *VirtualKeyStore) Update(ctx context.Context, id string, obj any) error {
	row, err := s.newRecord(id, "", obj)
	if err != nil {
		return err
	}
	result := s.db.WithContext(ctx).
		Model(&virtualKeyRecord{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"key":    row.Key,
			"tag":    row.Tag,
			"config": row.Config,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return intf.ErrNotFound
	}
	return nil
}

func (s *VirtualKeyStore) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&virtualKeyRecord{}).Error
}

func (s *VirtualKeyStore) Get(ctx context.Context, id string) (any, error) {
	var row virtualKeyRecord
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, intf.ErrNotFound
		}
		return nil, err
	}
	return s.decodeRow(row)
}

func (s *VirtualKeyStore) GetByKey(ctx context.Context, key string) (any, error) {
	var row virtualKeyRecord
	if err := s.db.WithContext(ctx).Where("key = ?", key).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, intf.ErrNotFound
		}
		return nil, err
	}
	return s.decodeRow(row)
}

func (s *VirtualKeyStore) newRecord(id string, tag string, obj any) (virtualKeyRecord, error) {
	if strings.TrimSpace(id) == "" {
		return virtualKeyRecord{}, fmt.Errorf("virtual key id is empty")
	}
	if obj == nil {
		return virtualKeyRecord{}, fmt.Errorf("virtual key config is nil")
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return virtualKeyRecord{}, fmt.Errorf("virtual key marshal: %w", err)
	}

	var meta struct {
		ID  string `json:"id"`
		Key string `json:"key"`
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return virtualKeyRecord{}, fmt.Errorf("virtual key decode metadata: %w", err)
	}
	if strings.TrimSpace(meta.ID) == "" {
		return virtualKeyRecord{}, fmt.Errorf("virtual key id is empty")
	}
	if meta.ID != id {
		return virtualKeyRecord{}, fmt.Errorf("virtual key id mismatch: %q != %q", meta.ID, id)
	}
	if strings.TrimSpace(meta.Key) == "" {
		return virtualKeyRecord{}, fmt.Errorf("virtual key key is empty")
	}
	if tag == "" {
		tag = meta.Tag
	}

	return virtualKeyRecord{
		ID:     id,
		Key:    meta.Key,
		Tag:    tag,
		Config: string(data),
	}, nil
}

func (s *VirtualKeyStore) decodeRows(rows []virtualKeyRecord) ([]any, error) {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		obj, err := s.decodeRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, nil
}

func (s *VirtualKeyStore) decodeRow(row virtualKeyRecord) (any, error) {
	obj, err := s.decodeVirtualKey([]byte(row.Config))
	if err != nil {
		return nil, err
	}
	if key, ok := obj.(*virtualkeypkg.VirtualKey); ok {
		key.CreatedAt = row.CreatedAt
		key.UpdatedAt = row.UpdatedAt
	}
	return obj, nil
}
