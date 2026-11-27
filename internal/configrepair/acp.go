package configrepair

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/strictjson"
	acplauncher "github.com/agent-guide/agent-gateway/pkg/acp/launcher"
)

func rawACP(agent map[string]json.RawMessage) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	runtime, err := object(agent["runtime"])
	if err != nil {
		return nil, nil, err
	}
	var kind string
	if err := json.Unmarshal(runtime["type"], &kind); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(kind) != "acp" {
		return runtime, nil, nil
	}
	acp, err := object(runtime["acp"])
	return runtime, acp, err
}

func legacyACPLauncher(acp map[string]json.RawMessage) (string, acplauncher.Launcher, error) {
	var old acplauncher.Launcher
	if _, exists := acp["launcher"]; exists {
		return "", old, fmt.Errorf("launcher is already present; use explicit replacement to resolve conflicting fields")
	}
	var oldType string
	if err := json.Unmarshal(acp["agent_type"], &oldType); err != nil {
		return "", old, fmt.Errorf("legacy ACP agent_type is invalid; use explicit replacement")
	}
	oldType = strings.TrimSpace(oldType)
	// These commands describe the old configuration schema, including when
	// the current distribution ships a different launcher for the same name.
	switch oldType {
	case "codex":
		old = acplauncher.Launcher{Command: "codex-acp"}
	case "opencode":
		old = acplauncher.Launcher{Command: "opencode", Args: []string{"acp"}, CWDFlag: "--cwd"}
	default:
		return "", old, fmt.Errorf("unrecognized legacy ACP agent_type; use explicit replacement")
	}
	if value, exists := acp["codex"]; oldType == "codex" && exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		var legacy struct {
			Mode           string   `json:"mode"`
			AdapterCommand string   `json:"adapter_command"`
			AdapterArgs    []string `json:"adapter_args"`
		}
		if err := json.Unmarshal(value, &legacy); err != nil {
			return "", old, fmt.Errorf("legacy ACP Codex settings are invalid; use explicit replacement")
		}
		if mode := strings.TrimSpace(legacy.Mode); mode != "" && mode != "adapter" {
			return "", old, fmt.Errorf("unsupported legacy Codex mode; use explicit replacement")
		}
		if command := strings.TrimSpace(legacy.AdapterCommand); command != "" {
			old.Command = command
		}
		old.Args = legacy.AdapterArgs
	}
	if err := old.Validate(); err != nil {
		return "", old, fmt.Errorf("invalid legacy ACP launcher: %w; use explicit replacement", err)
	}
	return oldType, old, nil
}

func sameACPLauncher(a, b acplauncher.Launcher) bool {
	return a.Command == b.Command && a.CWDFlag == b.CWDFlag && slices.Equal(a.Args, b.Args)
}

// ACPRepairOptions describes an explicit deployment-side launcher change.
type ACPRepairOptions struct {
	Migrate           bool
	Import            string
	Set               *acplauncher.Definition
	ConvertConnectors bool
}

func RepairACPLaunchers(ctx context.Context, db *sql.DB, backup string, static []Record, operations []Operation, opts ACPRepairOptions) error {
	var imported acplauncher.Launchers
	if opts.Import != "" {
		data, err := os.ReadFile(opts.Import)
		if err != nil {
			return fmt.Errorf("read legacy ACP launchers: %w", err)
		}
		if err := strictjson.Decode(data, &imported, 4<<20, 16); err != nil {
			return fmt.Errorf("decode legacy ACP launchers: %w", err)
		}
		if imported == nil {
			return fmt.Errorf("legacy ACP launchers must be a JSON object")
		}
	}
	prepare := func(records []Record) ([]Record, []Operation, error) {
		return stageACPLauncherRepair(records, operations, opts, imported)
	}
	if opts.ConvertConnectors {
		if len(static) != 0 {
			return fmt.Errorf("legacy connector conversion requires a SQLite-only repair")
		}
		return convertConnectors(ctx, db, backup, prepare)
	}
	return repairSQLite(ctx, db, backup, static, prepare)
}

func stageACPLauncherRepair(records []Record, operations []Operation, opts ACPRepairOptions, imported acplauncher.Launchers) ([]Record, []Operation, error) {
	launchers := acplauncher.DefaultLaunchers()
	stored := map[string]bool{}
	for _, record := range records {
		if record.Family == "acp_launchers" {
			if opts.Set != nil && record.ID == opts.Set.ID && !record.Static && !record.StaticInput {
				stored[record.ID] = true
				delete(launchers, record.ID)
				continue
			}
			def, err := acplauncher.DecodeDefinition(record.Data)
			if err != nil {
				return nil, nil, fmt.Errorf("acp_launchers/%s: %w", record.ID, err)
			}
			if record.StaticInput || record.Static || record.ID != def.ID || stored[def.ID] {
				return nil, nil, fmt.Errorf("acp_launchers/%s has an invalid or duplicate storage identity", record.ID)
			}
			stored[def.ID] = true
			launchers[def.ID] = def.Launcher
		}
	}
	records = slices.Clone(records)
	ops := slices.Clone(operations)
	put := func(id string, launcher acplauncher.Launcher) error {
		launcher.Command = strings.TrimSpace(launcher.Command)
		if err := acplauncher.ValidateLauncherName(id); err != nil {
			return err
		}
		if err := launcher.Validate(); err != nil {
			return fmt.Errorf("ACP launcher %q: %w", id, err)
		}
		if stored[id] && sameACPLauncher(launchers[id], launcher) {
			return nil
		}
		data, err := json.Marshal(acplauncher.Definition{ID: id, Launcher: launcher})
		if err != nil {
			return err
		}
		if _, err := acplauncher.DecodeDefinition(data); err != nil {
			return fmt.Errorf("ACP launcher %q: %w", id, err)
		}
		if !stored[id] {
			records = append(records, Record{Family: "acp_launchers", ID: id, Data: data})
		}
		ops = append(ops, Operation{Family: "acp_launchers", ID: id, Action: "replace", Data: data})
		stored[id] = true
		launchers[id] = launcher
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(imported)) {
		launcher := imported[name]
		launcher.Command = strings.TrimSpace(launcher.Command)
		if stored[name] && !sameACPLauncher(launchers[name], launcher) {
			return nil, nil, fmt.Errorf("imported ACP launcher %q conflicts with the stored definition", name)
		}
		if err := put(name, launcher); err != nil {
			return nil, nil, err
		}
	}
	if opts.Set != nil {
		if err := put(opts.Set.ID, opts.Set.Launcher); err != nil {
			return nil, nil, err
		}
	}
	if !opts.Migrate {
		return records, ops, nil
	}

	reserved := map[string]bool{}
	reserve := func(data json.RawMessage) {
		obj, _ := object(data)
		_, acp, _ := rawACP(obj)
		var name string
		if json.Unmarshal(acp["launcher"], &name) == nil {
			reserved[strings.TrimSpace(name)] = true
		}
	}
	explicit := map[string]bool{}
	for _, op := range operations {
		if op.Family == "agents" {
			explicit[op.ID] = true
			if op.Action == "replace" {
				reserve(op.Data)
			}
		}
	}
	for _, record := range records {
		if record.Family == "agents" {
			reserve(record.Data)
		}
	}
	names := slices.Sorted(maps.Keys(launchers))
	nextID := 1
	for _, record := range records {
		if record.Family != "agents" || record.Static || explicit[record.ID] {
			continue
		}
		obj, _ := object(record.Data)
		runtime, acp, _ := rawACP(obj)
		if acp["agent_type"] == nil && acp["codex"] == nil {
			continue
		}
		name, legacy, err := legacyACPLauncher(acp)
		if err != nil {
			return nil, nil, fmt.Errorf("agents/%s: %w", record.ID, err)
		}
		if !sameACPLauncher(launchers[name], legacy) {
			name = ""
			for _, candidate := range names {
				if sameACPLauncher(launchers[candidate], legacy) {
					name = candidate
					break
				}
			}
		}
		for name == "" {
			candidate := fmt.Sprintf("legacy-acp-%d", nextID)
			nextID++
			if _, exists := launchers[candidate]; exists || reserved[candidate] {
				continue
			}
			if err := put(candidate, legacy); err != nil {
				return nil, nil, err
			}
			names = append(names, candidate)
			name = candidate
		}
		delete(acp, "agent_type")
		delete(acp, "codex")
		acp["launcher"], _ = json.Marshal(name)
		runtime["acp"], _ = json.Marshal(acp)
		obj["runtime"], _ = json.Marshal(runtime)
		data, _ := json.Marshal(obj)
		ops = append(ops, Operation{Family: "agents", ID: record.ID, Action: "replace", Data: data})
	}
	return records, ops, nil
}
