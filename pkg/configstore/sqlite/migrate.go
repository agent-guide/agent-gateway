package sqlite

import (
	"fmt"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"gorm.io/gorm"
)

func migrateSchema(db *gorm.DB, storeSchema configstore.StoreSchema) error {
	if err := db.Exec(buildCreateTableSQL(storeSchema)).Error; err != nil {
		return fmt.Errorf("migrate %s table %q: %w", storeSchema.Kind, storeSchema.Table, err)
	}
	for _, indexSchema := range storeSchema.IndexColumns {
		if err := db.Exec(buildCreateIndexSQL(storeSchema, indexSchema)).Error; err != nil {
			return fmt.Errorf("migrate %s index %q on table %q: %w", storeSchema.Kind, indexSchema.Name, storeSchema.Table, err)
		}
	}
	return nil
}

func buildCreateTableSQL(storeSchema configstore.StoreSchema) string {
	columns := make([]string, 0, len(storeSchema.PrimaryKeyColumns)+4+len(storeSchema.IndexColumns))
	for _, column := range storeSchema.PrimaryKeyColumns {
		columns = append(columns, fmt.Sprintf("%s TEXT NOT NULL", quoteIdent(column)))
	}
	if storeSchema.TagColumn != "" {
		columns = append(columns, fmt.Sprintf("%s TEXT NOT NULL DEFAULT ''", quoteIdent(storeSchema.TagColumn)))
	}
	for _, indexSchema := range storeSchema.IndexColumns {
		columns = append(columns, fmt.Sprintf("%s TEXT NOT NULL", quoteIdent(indexSchema.Column)))
	}
	columns = append(columns, fmt.Sprintf("%s TEXT NOT NULL", quoteIdent(storeSchema.DataColumn)))
	if storeSchema.Timestamped {
		columns = append(columns, `"created_at" DATETIME`)
		columns = append(columns, `"updated_at" DATETIME`)
	}
	columns = append(columns, fmt.Sprintf("PRIMARY KEY (%s)", joinQuoted(storeSchema.PrimaryKeyColumns)))

	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (%s)",
		quoteIdent(storeSchema.Table),
		strings.Join(columns, ", "),
	)
}

func buildCreateIndexSQL(storeSchema configstore.StoreSchema, indexSchema configstore.IndexSchema) string {
	indexType := "INDEX"
	if indexSchema.Unique {
		indexType = "UNIQUE INDEX"
	}
	return fmt.Sprintf(
		"CREATE %s IF NOT EXISTS %s ON %s (%s)",
		indexType,
		quoteIdent(sqliteIndexName(storeSchema, indexSchema)),
		quoteIdent(storeSchema.Table),
		quoteIdent(indexSchema.Column),
	)
}

func sqliteIndexName(storeSchema configstore.StoreSchema, indexSchema configstore.IndexSchema) string {
	return fmt.Sprintf("idx_%s_%s", storeSchema.Table, indexSchema.Name)
}

func joinQuoted(columns []string) string {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, quoteIdent(column))
	}
	return strings.Join(quoted, ", ")
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
