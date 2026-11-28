package configrepair

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// GenerateSQLiteRepairs stages a complete repair batch from one SQLite read
// transaction. Explicit operations supply any decisions the stored data lacks.
func GenerateSQLiteRepairs(ctx context.Context, db *sql.DB, static []Record, explicit []Operation, opts SQLiteRepairOptions) ([]Operation, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := checkConnectorLayout(ctx, tx, opts.ConvertConnectors); err != nil {
		return nil, err
	}
	records, err := ReadSQLite(ctx, tx)
	if err != nil {
		return nil, err
	}
	records = append(records, static...)
	ops, err := stageACPLauncherRepair(records, explicit, opts.Set, true)
	if err != nil {
		return nil, err
	}
	byRecord := make(map[[2]string]int, len(ops))
	for i, op := range ops {
		byRecord[[2]string{op.Family, op.ID}] = i
	}
	for _, r := range records {
		if r.Static || r.StaticInput || (r.Family != "routes" && r.Family != "agents") {
			continue
		}
		pos, exists := byRecord[[2]string{r.Family, r.ID}]
		if exists && pos < len(explicit) {
			continue
		}
		obj, _ := object(r.Data)
		switch r.Family {
		case "routes":
			var kind string
			_ = json.Unmarshal(obj["kind"], &kind)
			policy, _ := object(obj["target_policy"])
			if kind == "llm" && hasInlineModelTargets(policy) {
				ops = append(ops, Operation{Family: r.Family, ID: r.ID, Action: "extract-models"})
			}
		case "agents":
			policy, _ := object(obj["policy"])
			if !hasAgentBudget(policy) {
				continue
			}
			if exists {
				// ACP migration already replaces this Agent. Include both edits
				// in that replacement, since Apply accepts one operation per ID.
				data, err := removeAgentBudget(ops[pos].Data)
				if err != nil {
					return nil, fmt.Errorf("agents/%s: %w", r.ID, err)
				}
				ops[pos].Data = data
			} else {
				ops = append(ops, Operation{Family: r.Family, ID: r.ID, Action: "remove-budget"})
			}
		}
	}
	if opts.ConvertConnectors {
		if _, err := stageConnectorConversion(records, ops); err != nil {
			return nil, err
		}
		return ops, nil
	}
	staged, err := Apply(records, ops)
	if err != nil {
		return nil, err
	}
	if err := Validate(staged); err != nil {
		return nil, err
	}
	return ops, nil
}
