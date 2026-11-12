package configrepair

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/agent-guide/agent-gateway/internal/strictjson"
)

// ConvertConnectors is an explicit stopped-service cutover. It never opens the
// runtime stores, creates credentials, or modifies historical usage tables.
func ConvertConnectors(ctx context.Context, db *sql.DB, backup string, stopped bool, operations ...Operation) error {
	if !stopped || backup == "" {
		return fmt.Errorf("conversion requires --service-stopped and unused --backup")
	}
	var old, target int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='mcp_services'`).Scan(&old); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='connectors'`).Scan(&target); err != nil {
		return err
	}
	if old != 1 {
		return fmt.Errorf("legacy mcp_services table not found")
	}
	if target != 0 {
		return fmt.Errorf("target connectors table already exists; resolve table conflict through explicit offline repair")
	}
	f, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `VACUUM INTO ?`, backup); err != nil {
		_ = os.Remove(backup)
		return fmt.Errorf("backup failed: %w", err)
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
	original := append([]Record(nil), records...)
	if len(operations) > 0 {
		records, err = Apply(records, operations)
		if err != nil {
			return err
		}
	}
	for i := range records {
		r := &records[i]
		v, err := strictjson.Parse(r.Data, 4<<20, 64)
		if err != nil {
			return fmt.Errorf("invalid raw %s/%s", r.Family, r.ID)
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid record %s/%s", r.Family, r.ID)
		}
		changed := false
		switch r.Family {
		case "mcp_services":
			if _, exists := obj["type"]; exists {
				return fmt.Errorf("legacy connector contains type")
			}
			if _, exists := obj["mcp"]; exists {
				return fmt.Errorf("legacy connector contains mcp branch")
			}
			connection := map[string]any{}
			for _, k := range []string{"transport", "command", "args", "url", "post_url", "env", "auto_auth", "auth"} {
				if v, ok := obj[k]; ok {
					connection[k] = v
					delete(obj, k)
				}
			}
			obj["type"] = "mcp"
			obj["mcp"] = connection
			r.Family = "connectors"
			changed = true
		case "agents":
			if resources, ok := obj["resources"].(map[string]any); ok {
				if err := moveField(resources, "mcp_service_ids", "connector_ids"); err != nil {
					return err
				}
			}
			if runtime, ok := obj["runtime"].(map[string]any); ok {
				if builtin, ok := runtime["builtin"].(map[string]any); ok {
					if err := moveBuiltin(builtin); err != nil {
						return err
					}
				}
			}
			changed = true
		case "routes":
			if obj["kind"] == "mcp" {
				if policy, ok := obj["target_policy"].(map[string]any); ok {
					if policy["kind"] == "mcp-service" {
						policy["kind"] = "connector"
					}
					if err := moveField(policy, "service_id", "connector_id"); err != nil {
						return err
					}
					changed = true
				}
			}
		}
		if changed {
			r.Data, err = json.Marshal(obj)
			if err != nil {
				return err
			}
		}
	}
	if err := Validate(records); err != nil {
		return fmt.Errorf("conversion final-state validation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE mcp_services RENAME TO connectors`); err != nil {
		return err
	}
	nextIDs := map[string]bool{}
	for _, r := range records {
		nextIDs[r.Family+"/"+r.ID] = true
	}
	for _, r := range original {
		family := r.Family
		if family == "mcp_services" {
			family = "connectors"
		}
		if !nextIDs[family+"/"+r.ID] {
			switch family {
			case "connectors", "agents", "routes":
				if _, err := tx.ExecContext(ctx, `DELETE FROM `+family+` WHERE id=?`, r.ID); err != nil {
					return err
				}
			default:
				return fmt.Errorf("connector conversion cannot delete unrelated families")
			}
		}
	}
	for _, r := range records {
		if r.Family != "connectors" && r.Family != "agents" && r.Family != "routes" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE `+r.Family+` SET config=? WHERE id=?`, string(r.Data), r.ID); err != nil {
			return err
		}
		if r.Family == "connectors" {
			if _, err := tx.ExecContext(ctx, `UPDATE connectors SET tag='mcp' WHERE id=?`, r.ID); err != nil {
				return err
			}
		}
	}
	authoritative, err := ReadSQLite(ctx, tx)
	if err != nil {
		return err
	}
	if err = Validate(authoritative); err != nil {
		return fmt.Errorf("conversion readback validation: %w", err)
	}
	if len(authoritative) != len(records) {
		return fmt.Errorf("conversion readback record count mismatch")
	}
	for i := range records {
		if records[i].Family != authoritative[i].Family || records[i].ID != authoritative[i].ID { // tables order changes after legacy relocation
			found := false
			for _, r := range authoritative {
				if r.Family == records[i].Family && r.ID == records[i].ID && jsonEqual(r.Data, records[i].Data) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("conversion readback mismatch")
			}
		} else if !jsonEqual(records[i].Data, authoritative[i].Data) {
			return fmt.Errorf("conversion readback mismatch")
		}
	}
	return tx.Commit()
}
func moveField(obj map[string]any, old, next string) error {
	v, ok := obj[old]
	if !ok {
		return nil
	}
	if _, exists := obj[next]; exists {
		return fmt.Errorf("conflicting old and new reference fields")
	}
	obj[next] = v
	delete(obj, old)
	return nil
}
func moveBuiltin(v any) error {
	switch x := v.(type) {
	case map[string]any:
		if err := moveField(x, "mcp_service_id", "connector_id"); err != nil {
			return err
		}
		for _, v := range x {
			if err := moveBuiltin(v); err != nil {
				return err
			}
		}
	case []any:
		for _, v := range x {
			if err := moveBuiltin(v); err != nil {
				return err
			}
		}
	}
	return nil
}
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}
