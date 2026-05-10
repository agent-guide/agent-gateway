package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/agent-guide/agent-gateway/pkg/configstore/intf"
)

type sqliteJSONRecord struct {
	ID   string `gorm:"primaryKey"`
	Tag  string `gorm:"index;not null"`
	Data string `gorm:"type:text;not null"`
}

type sqliteJSONStore struct {
	db        *gorm.DB
	table     string
	kind      string
	idField   string
	tagField  string
	dataField string
	decode    intf.ConfigObjectDecoder
}

func newSQLiteJSONStore(db *gorm.DB, table string, kind string, decode intf.ConfigObjectDecoder) *sqliteJSONStore {
	return newSQLiteJSONStoreWithColumns(db, table, kind, "id", "tag", "data", decode)
}

func newSQLiteJSONStoreWithColumns(db *gorm.DB, table string, kind string, idField string, tagField string, dataField string, decode intf.ConfigObjectDecoder) *sqliteJSONStore {
	return &sqliteJSONStore{
		db:        db,
		table:     table,
		kind:      kind,
		idField:   idField,
		tagField:  tagField,
		dataField: dataField,
		decode:    decode,
	}
}

func (s *sqliteJSONStore) ListByTagPrefix(ctx context.Context, tagPrefix string) ([]any, error) {
	var rows []sqliteJSONRecord
	query := s.baseQuery(ctx)
	if tagPrefix != "" {
		query = query.Where(s.quotedColumnName(s.tagField)+" LIKE ?", tagPrefix+"%")
	}
	if err := query.Find(&rows).Error; err != nil {
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

func (s *sqliteJSONStore) ListByTag(ctx context.Context, tag string) ([]any, error) {
	var rows []sqliteJSONRecord
	query := s.baseQuery(ctx)
	if tag != "" {
		query = query.Where(s.quotedColumnName(s.tagField)+" = ?", tag)
	}
	if err := query.Find(&rows).Error; err != nil {
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

func (s *sqliteJSONStore) Upsert(ctx context.Context, id string, tag string, obj any) (string, error) {
	row, err := s.newRecord(id, tag, obj)
	if err != nil {
		return "", err
	}
	if err := s.db.WithContext(ctx).
		Table(s.table).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: s.idField}},
			DoUpdates: clause.AssignmentColumns([]string{s.tagField, s.dataField}),
		}).
		Create(s.dbRecord(row)).Error; err != nil {
		return "", err
	}
	return row.ID, nil
}

func (s *sqliteJSONStore) Create(ctx context.Context, id string, tag string, obj any) (string, error) {
	row, err := s.newRecord(id, tag, obj)
	if err != nil {
		return "", err
	}
	if err := s.db.WithContext(ctx).
		Table(s.table).
		Create(s.dbRecord(row)).Error; err != nil {
		return "", err
	}
	return row.ID, nil
}

func (s *sqliteJSONStore) Update(ctx context.Context, id string, obj any) error {
	if id == "" {
		return fmt.Errorf("%s id is empty", s.kind)
	}
	if obj == nil {
		return fmt.Errorf("%s config is nil", s.kind)
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("%s marshal: %w", s.kind, err)
	}

	result := s.db.WithContext(ctx).
		Table(s.table).
		Where(s.quotedColumnName(s.idField)+" = ?", id).
		Update(s.columnName(s.dataField), string(data))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return intf.ErrNotFound
	}
	return nil
}

func (s *sqliteJSONStore) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).
		Table(s.table).
		Where(s.quotedColumnName(s.idField)+" = ?", id).
		Delete(&sqliteJSONRecord{}).Error
}

func (s *sqliteJSONStore) Get(ctx context.Context, id string) (string, any, error) {
	var row sqliteJSONRecord
	if err := s.baseQuery(ctx).Where(s.quotedColumnName(s.idField)+" = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil, intf.ErrNotFound
		}
		return "", nil, err
	}

	obj, err := s.decode([]byte(row.Data))
	if err != nil {
		return "", nil, err
	}
	return row.Tag, obj, nil
}

func (s *sqliteJSONStore) newRecord(id string, tag string, obj any) (sqliteJSONRecord, error) {
	if id == "" {
		return sqliteJSONRecord{}, fmt.Errorf("%s id is empty", s.kind)
	}
	if obj == nil {
		return sqliteJSONRecord{}, fmt.Errorf("%s config is nil", s.kind)
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return sqliteJSONRecord{}, fmt.Errorf("%s marshal: %w", s.kind, err)
	}

	return sqliteJSONRecord{
		ID:   id,
		Tag:  tag,
		Data: string(data),
	}, nil
}

func (s *sqliteJSONStore) baseQuery(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx).
		Table(s.table).
		Select(strings.Join([]string{
			fmt.Sprintf("%s AS id", s.quotedColumnName(s.idField)),
			fmt.Sprintf("%s AS tag", s.quotedColumnName(s.tagField)),
			fmt.Sprintf("%s AS data", s.quotedColumnName(s.dataField)),
		}, ", ")).
		Order(s.quotedColumnName(s.idField) + " asc")
}

func (s *sqliteJSONStore) dbRecord(row sqliteJSONRecord) map[string]any {
	return map[string]any{
		s.idField:   row.ID,
		s.tagField:  row.Tag,
		s.dataField: row.Data,
	}
}

func (s *sqliteJSONStore) columnName(name string) string {
	return s.db.NamingStrategy.ColumnName(s.table, name)
}

func (s *sqliteJSONStore) quotedColumnName(name string) string {
	return s.db.Statement.Quote(clause.Column{Name: s.columnName(name)})
}
