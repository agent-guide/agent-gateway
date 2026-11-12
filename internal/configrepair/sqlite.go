package configrepair

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

var tables = []struct {
	family, table, column string
	keys                  []string
}{
	{"providers", "providers", "config", []string{"id"}}, {"routes", "routes", "config", []string{"id"}},
	{"agents", "agents", "config", []string{"id"}}, {"mcp_services", "mcp_services", "config", []string{"id"}}, {"connectors", "connectors", "config", []string{"id"}},
	{"virtual_keys", "virtual_keys", "config", []string{"id"}}, {"credentials", "cliauth_credentials", "data", []string{"id"}},
	{"managed_models", "model_configs", "data", []string{"provider_id", "upstream_model"}},
	{"extensions", "extensions", "config", []string{"module_id"}},
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// OpenSQLite never creates a missing database or performs schema migrations.
func OpenSQLite(path string, writable bool) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("SQLite input must be a regular file")
	}
	mode := "ro"
	if writable {
		mode = "rw"
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute}).String() + "?mode=" + mode + "&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func ReadSQLite(ctx context.Context, db queryer) ([]Record, error) {
	var out []Record
	for _, t := range tables {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, t.table).Scan(&count); err != nil {
			return nil, err
		}
		if count == 0 {
			continue
		}
		keys := strings.Join(t.keys, ", ")
		rows, err := db.QueryContext(ctx, `SELECT `+keys+`, `+t.column+` FROM `+t.table+` ORDER BY `+keys)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			r := Record{Family: t.family}
			var data sql.NullString
			values := make([]string, len(t.keys))
			dest := make([]any, 0, len(values)+1)
			for i := range values {
				dest = append(dest, &values[i])
			}
			dest = append(dest, &data)
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, err
			}
			r.ID = recordKey(values)
			if data.Valid {
				r.Data = json.RawMessage(data.String)
			} else {
				// Keep the damaged row addressable for explicit replacement or
				// deletion. Inspect reports null as an invalid JSON object.
				r.Data = json.RawMessage("null")
			}
			out = append(out, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RepairSQLite requires an operator-declared stopped service. That prerequisite
// does not claim multi-process coordination. VACUUM INTO captures committed WAL
// contents in a standalone backup before the single transactional repair batch.
func RepairSQLite(ctx context.Context, db *sql.DB, backup string, stopped bool, static []Record, operations []Operation) error {
	if !stopped {
		return fmt.Errorf("stop every service using this SQLite file and pass --service-stopped")
	}
	if backup == "" {
		return fmt.Errorf("an unused --backup path is required")
	}
	file, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, backup); err != nil {
		_ = os.Remove(backup)
		return fmt.Errorf("backup: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	records, err := ReadSQLite(ctx, tx)
	if err != nil {
		return err
	}
	records = append(records, static...)
	next, err := Apply(records, operations)
	if err != nil {
		return err
	}
	if err := Validate(next); err != nil {
		return fmt.Errorf("full recheck: %w", err)
	}
	byKey := map[string]Record{}
	for _, r := range next {
		if !r.Static {
			byKey[r.Family+"/"+r.ID] = r
		}
	}
	for _, op := range operations {
		if op.Family != "routes" && op.Family != "agents" && op.Family != "connectors" && op.Family != "mcp_services" {
			return fmt.Errorf("repair only supports routes, agents and connector records")
		}
		if op.Action == "delete" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+op.Family+` WHERE id=?`, op.ID); err != nil {
				return err
			}
			continue
		}
		r := byKey[op.Family+"/"+op.ID]
		var fields struct {
			Kind    string `json:"kind"`
			Runtime struct {
				Type string `json:"type"`
			} `json:"runtime"`
		}
		if err := json.Unmarshal(r.Data, &fields); err != nil {
			return err
		}
		// Route tags are operator-defined; retain them. Agent tags index runtime.
		query := `UPDATE ` + op.Family + ` SET config=?, updated_at=? WHERE id=?`
		args := []any{string(r.Data), time.Now().UTC(), op.ID}
		if op.Family == "agents" {
			query = `UPDATE agents SET config=?, updated_at=?, tag=? WHERE id=?`
			args = []any{string(r.Data), time.Now().UTC(), fields.Runtime.Type, op.ID}
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	// Read back the persisted batch before commit; a readback or validation
	// failure rolls back every mutation, never compensates after committing.
	stored, err := ReadSQLite(ctx, tx)
	if err != nil {
		return err
	}
	if err := verifySQLiteRecords(next, stored); err != nil {
		return err
	}
	if err := Validate(append(stored, static...)); err != nil {
		return fmt.Errorf("persisted recheck: %w", err)
	}
	return tx.Commit()
}

// Verify the complete staged result, including unchanged records: triggers may
// silently ignore a write or change another record while leaving valid config.
func verifySQLiteRecords(next, stored []Record) error {
	expected := make(map[string]json.RawMessage)
	for _, r := range next {
		if !r.Static {
			expected[r.Family+"/"+r.ID] = r.Data
		}
	}
	for _, r := range stored {
		key := r.Family + "/" + r.ID
		data, ok := expected[key]
		if !ok || !bytes.Equal(data, r.Data) {
			return fmt.Errorf("persisted repair differs from staged record %s", key)
		}
		delete(expected, key)
	}
	if len(expected) != 0 {
		return fmt.Errorf("persisted repair is missing staged records")
	}
	return nil
}
