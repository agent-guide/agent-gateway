package sqlite

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"gorm.io/gorm"
)

var (
	ErrTagUnsupported   = errors.New("config store schema does not support tag queries")
	ErrInvalidKeyParts  = errors.New("invalid config store key parts")
	ErrUnknownIndexName = errors.New("unknown config store index")
)

type JSONConfigStore struct {
	db     *gorm.DB
	schema configstore.StoreSchema
}

type sqliteStoreRow struct {
	Tag       string
	Data      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *JSONConfigStore) List(ctx context.Context) ([]any, error) {
	return s.listRows(ctx, nil)
}

func (s *JSONConfigStore) ListByTag(ctx context.Context, tag string) ([]any, error) {
	if s.schema.TagColumn == "" {
		return nil, fmt.Errorf("%w: %s", ErrTagUnsupported, s.schema.Name)
	}
	return s.listRows(ctx, func(query *gorm.DB) *gorm.DB {
		if tag == "" {
			return query
		}
		return query.Where(s.quotedColumnName(s.schema.TagColumn)+" = ?", tag)
	})
}

func (s *JSONConfigStore) ListByTagPrefix(ctx context.Context, tagPrefix string) ([]any, error) {
	if s.schema.TagColumn == "" {
		return nil, fmt.Errorf("%w: %s", ErrTagUnsupported, s.schema.Name)
	}
	return s.listRows(ctx, func(query *gorm.DB) *gorm.DB {
		if tagPrefix == "" {
			return query
		}
		return query.Where(s.quotedColumnName(s.schema.TagColumn)+" LIKE ?", tagPrefix+"%")
	})
}

func (s *JSONConfigStore) Create(ctx context.Context, obj any) error {
	record, err := s.recordValues(obj)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Table(s.schema.Table).Create(record).Error
}

func (s *JSONConfigStore) Update(ctx context.Context, obj any) error {
	record, err := s.recordValues(obj)
	if err != nil {
		return err
	}
	keyParts, err := s.schema.Metadata.PrimaryKey(obj)
	if err != nil {
		return err
	}

	result := s.db.WithContext(ctx).
		Table(s.schema.Table).
		Where(s.primaryKeyWhereClause(), keyParts...).
		Updates(s.updateValues(record))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return configstore.ErrNotFound
	}
	return nil
}

func (s *JSONConfigStore) Delete(ctx context.Context, keyParts ...any) error {
	if err := s.validateKeyParts(keyParts...); err != nil {
		return err
	}
	return s.db.WithContext(ctx).
		Table(s.schema.Table).
		Where(s.primaryKeyWhereClause(), keyParts...).
		Delete(map[string]any{}).Error
}

func (s *JSONConfigStore) Get(ctx context.Context, keyParts ...any) (any, error) {
	if err := s.validateKeyParts(keyParts...); err != nil {
		return nil, err
	}
	return s.getOne(ctx, func(query *gorm.DB) *gorm.DB {
		return query.Where(s.primaryKeyWhereClause(), keyParts...)
	})
}

func (s *JSONConfigStore) GetByIndex(ctx context.Context, indexName string, value any) (any, error) {
	indexSchema, ok := s.indexByName(indexName)
	if !ok {
		return nil, fmt.Errorf("%w: %s on %s", ErrUnknownIndexName, indexName, s.schema.Name)
	}
	return s.getOne(ctx, func(query *gorm.DB) *gorm.DB {
		return query.Where(s.quotedColumnName(indexSchema.Column)+" = ?", value)
	})
}

func (s *JSONConfigStore) listRows(ctx context.Context, apply func(query *gorm.DB) *gorm.DB) ([]any, error) {
	var rows []sqliteStoreRow
	query := s.baseQuery(ctx)
	if apply != nil {
		query = apply(query)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}

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

func (s *JSONConfigStore) getOne(ctx context.Context, apply func(query *gorm.DB) *gorm.DB) (any, error) {
	var row sqliteStoreRow
	query := s.baseQuery(ctx)
	if apply != nil {
		query = apply(query)
	}
	if err := query.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, configstore.ErrNotFound
		}
		return nil, err
	}
	return s.decodeRow(row)
}

func (s *JSONConfigStore) recordValues(obj any) (map[string]any, error) {
	keyParts, err := s.schema.Metadata.PrimaryKey(obj)
	if err != nil {
		return nil, err
	}
	if err := s.validateKeyParts(keyParts...); err != nil {
		return nil, err
	}

	data, err := s.schema.Codec.Encode(obj)
	if err != nil {
		return nil, err
	}

	record := make(map[string]any, len(s.schema.PrimaryKeyColumns)+len(s.schema.IndexColumns)+4)
	for idx, column := range s.schema.PrimaryKeyColumns {
		record[column] = keyParts[idx]
	}
	if s.schema.TagColumn != "" {
		tag, ok, err := s.schema.Metadata.Tag(obj)
		if err != nil {
			return nil, err
		}
		if ok {
			record[s.schema.TagColumn] = tag
		} else {
			record[s.schema.TagColumn] = ""
		}
	}
	indexes, err := s.schema.Metadata.Indexes(obj)
	if err != nil {
		return nil, err
	}
	for _, indexSchema := range s.schema.IndexColumns {
		value, ok := indexes[indexSchema.Name]
		if !ok {
			return nil, fmt.Errorf("index %q is required for store %q", indexSchema.Name, s.schema.Name)
		}
		record[indexSchema.Column] = value
	}
	record[s.schema.DataColumn] = string(data)
	if s.schema.Timestamped {
		now := time.Now().UTC()
		record["created_at"] = now
		record["updated_at"] = now
	}
	return record, nil
}

func (s *JSONConfigStore) updateValues(record map[string]any) map[string]any {
	values := make(map[string]any, len(record))
	for key, value := range record {
		if key == "created_at" {
			continue
		}
		values[key] = value
	}
	if s.schema.Timestamped {
		values["updated_at"] = time.Now().UTC()
	}
	return values
}

func (s *JSONConfigStore) decodeRow(row sqliteStoreRow) (any, error) {
	obj, err := s.schema.Codec.Decode([]byte(row.Data))
	if err != nil {
		return nil, err
	}
	if s.schema.Timestamped {
		applyObjectTimestamp(obj, "CreatedAt", row.CreatedAt)
		applyObjectTimestamp(obj, "UpdatedAt", row.UpdatedAt)
	}
	return obj, nil
}

func (s *JSONConfigStore) baseQuery(ctx context.Context) *gorm.DB {
	selectParts := []string{
		fmt.Sprintf("%s AS data", s.quotedColumnName(s.schema.DataColumn)),
	}
	if s.schema.TagColumn != "" {
		selectParts = append(selectParts, fmt.Sprintf("%s AS tag", s.quotedColumnName(s.schema.TagColumn)))
	} else {
		selectParts = append(selectParts, "'' AS tag")
	}
	if s.schema.Timestamped {
		selectParts = append(selectParts, `"created_at" AS created_at`, `"updated_at" AS updated_at`)
	}

	return s.db.WithContext(ctx).
		Table(s.schema.Table).
		Select(strings.Join(selectParts, ", ")).
		Order(s.orderByPrimaryKey())
}

func (s *JSONConfigStore) validateKeyParts(keyParts ...any) error {
	if len(keyParts) != len(s.schema.PrimaryKeyColumns) {
		return fmt.Errorf("%w: store %s expects %d key parts, got %d", ErrInvalidKeyParts, s.schema.Name, len(s.schema.PrimaryKeyColumns), len(keyParts))
	}
	return nil
}

func (s *JSONConfigStore) primaryKeyWhereClause() string {
	parts := make([]string, 0, len(s.schema.PrimaryKeyColumns))
	for _, column := range s.schema.PrimaryKeyColumns {
		parts = append(parts, s.quotedColumnName(column)+" = ?")
	}
	return strings.Join(parts, " AND ")
}

func (s *JSONConfigStore) orderByPrimaryKey() string {
	parts := make([]string, 0, len(s.schema.PrimaryKeyColumns))
	for _, column := range s.schema.PrimaryKeyColumns {
		parts = append(parts, s.quotedColumnName(column)+" asc")
	}
	return strings.Join(parts, ", ")
}

func (s *JSONConfigStore) quotedColumnName(name string) string {
	return quoteIdent(name)
}

func (s *JSONConfigStore) indexByName(name string) (configstore.IndexSchema, bool) {
	for _, indexSchema := range s.schema.IndexColumns {
		if indexSchema.Name == name {
			return indexSchema, true
		}
	}
	return configstore.IndexSchema{}, false
}

func applyObjectTimestamp(obj any, fieldName string, value time.Time) {
	if value.IsZero() {
		return
	}
	target := reflect.ValueOf(obj)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() {
		return
	}
	elem := target.Elem()
	if !elem.IsValid() || elem.Kind() != reflect.Struct {
		return
	}
	field := elem.FieldByName(fieldName)
	if !field.IsValid() || !field.CanSet() || field.Type() != reflect.TypeOf(time.Time{}) {
		return
	}
	field.Set(reflect.ValueOf(value))
}

var _ configstore.ConfigStore = (*JSONConfigStore)(nil)
