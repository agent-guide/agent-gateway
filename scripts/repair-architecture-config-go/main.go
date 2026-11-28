package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/configrepair"
	"github.com/agent-guide/agent-gateway/pkg/acp/launcher"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/anthropic"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/claudecode"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/codex"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/deepseek"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/gemini"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/grok"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/ollama"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openrouter"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/qwen"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/zhipu"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("repair-architecture-config", flag.ContinueOnError)
	flags.SetOutput(out)
	dbPath := flags.String("db", "", "existing SQLite file (inspection never starts the gateway)")
	staticPath := flags.String("static-config", "", "static GatewayBundle YAML or adapted Caddy JSON")
	caddy := flags.Bool("caddy-json", false, "static file is adapted Caddy JSON")
	convertConnectors := flags.Bool("convert-connectors", false, "convert legacy MCP services and Agent/route references after stopping services")
	migrateACP := flags.Bool("migrate-acp-launchers", false, "migrate inline ACP launcher settings already in SQLite after stopping services")
	setACP := flags.String("set-acp-launcher", "", "store a deployment launcher by name, with executable arguments after --")
	acpCommand := flags.String("acp-command", "", "executable for --set-acp-launcher")
	acpCWDFlag := flags.String("acp-cwd-flag", "", "optional working-directory flag for --set-acp-launcher")
	operations := flags.String("repair", "", "repair operations JSON to apply after stopping services or include in an export")
	export := flags.String("export-repairs", "", "write a validated SQLite repair batch to a new JSON file without applying it")
	allowedModels := make(map[string][]allowedModel)
	flags.Func("allow-model", "explicit model for a legacy direct route in an export (route-id=upstream-model, repeatable)", func(value string) error {
		route, model, ok := strings.Cut(value, "=")
		if !ok || route == "" || model == "" {
			return fmt.Errorf("expected route-id=upstream-model")
		}
		allowedModels[route] = append(allowedModels[route], allowedModel{Name: model, UpstreamModel: model})
		return nil
	})
	target := flags.String("target", "db", "repair target: db or static (the other source is read-only)")
	backup := flags.String("backup", "", "unused backup filename, required for repairs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" && *staticPath == "" {
		return fmt.Errorf("provide --db and/or --static-config")
	}
	if *target != "db" && *target != "static" {
		return fmt.Errorf("--target must be db or static")
	}
	if *export != "" && (*dbPath == "" || *target != "db") {
		return fmt.Errorf("--export-repairs requires --db and --target db")
	}
	if len(allowedModels) > 0 && *export == "" {
		return fmt.Errorf("--allow-model requires --export-repairs")
	}
	if *setACP == "" && (*acpCommand != "" || *acpCWDFlag != "" || flags.NArg() != 0) {
		return fmt.Errorf("executable settings and positional arguments require --set-acp-launcher")
	}
	if *setACP != "" && (*acpCommand == "" || *migrateACP) {
		return fmt.Errorf("--set-acp-launcher requires --acp-command and must run separately from ACP migration")
	}
	acpWrite := *migrateACP || *setACP != ""
	if acpWrite && (*dbPath == "" || *target != "db") {
		return fmt.Errorf("ACP launcher changes require --db and --target db")
	}
	acpOptions := configrepair.ACPRepairOptions{Migrate: *migrateACP, ConvertConnectors: *convertConnectors}
	if *setACP != "" {
		acpOptions.Set = &launcher.Definition{ID: *setACP, Launcher: launcher.Launcher{Command: *acpCommand, Args: flags.Args(), CWDFlag: *acpCWDFlag}}
	}
	if *export == "" && (*operations != "" || *convertConnectors || acpWrite) && *backup == "" {
		return fmt.Errorf("repairs require an unused --backup path")
	}
	if *convertConnectors && (*dbPath == "" || *staticPath != "" || *target != "db") {
		return fmt.Errorf("--convert-connectors requires SQLite-only input and --target db")
	}
	if *convertConnectors && *export == "" {
		db, err := configrepair.OpenSQLite(*dbPath, true)
		if err != nil {
			return err
		}
		defer db.Close()
		var ops []configrepair.Operation
		if *operations != "" {
			ops, err = readOperations(*operations)
			if err != nil {
				return err
			}
		}
		if acpWrite {
			err = configrepair.RepairACPLaunchers(ctx, db, *backup, nil, ops, acpOptions)
		} else {
			err = configrepair.ConvertConnectors(ctx, db, *backup, ops...)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(out, "Connector conversion committed; backup:", *backup)
		return nil
	}
	var records []configrepair.Record
	var static []configrepair.Record
	var doc *configrepair.StaticDocument
	var original []byte
	if *staticPath != "" {
		var err error
		original, err = os.ReadFile(*staticPath)
		if err != nil {
			return err
		}
		doc, err = configrepair.DecodeStatic(original, *caddy)
		if err != nil {
			return err
		}
		static, err = doc.Records()
		if err != nil {
			return err
		}
	}
	if *export != "" {
		var explicit []configrepair.Operation
		if *operations != "" {
			var err error
			explicit, err = readOperations(*operations)
			if err != nil {
				return err
			}
		}
		for _, id := range slices.Sorted(maps.Keys(allowedModels)) {
			data, err := json.Marshal(struct {
				Models []allowedModel `json:"models"`
			}{Models: allowedModels[id]})
			if err != nil {
				return err
			}
			explicit = append(explicit, configrepair.Operation{Family: "routes", ID: id, Action: "convert-target", Data: data})
		}
		return exportRepairs(ctx, out, *dbPath, *export, static, explicit, acpOptions)
	}
	if *dbPath != "" {
		writeDB := *target == "db" && (*operations != "" || acpWrite)
		db, err := configrepair.OpenSQLite(*dbPath, writeDB)
		if err != nil {
			return err
		}
		defer db.Close()
		if writeDB {
			var ops []configrepair.Operation
			if *operations != "" {
				ops, err = readOperations(*operations)
				if err != nil {
					return err
				}
			}
			if acpWrite {
				err = configrepair.RepairACPLaunchers(ctx, db, *backup, static, ops, acpOptions)
			} else {
				err = configrepair.RepairSQLite(ctx, db, *backup, static, ops)
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(out, "Repair committed and rechecked; backup:", *backup)
			return nil
		}
		records, err = configrepair.ReadSQLite(ctx, db)
		if err != nil {
			return err
		}
	}
	if *operations == "" {
		records = append(records, static...)
		issues := configrepair.Inspect(records)
		if len(issues) == 0 {
			if err := configrepair.Validate(records); err != nil {
				issues = append(issues, configrepair.Issue{Field: "validation", Action: err.Error()})
			}
		}
		if issues == nil {
			issues = []configrepair.Issue{}
		}
		if err := json.NewEncoder(out).Encode(issues); err != nil {
			return err
		}
		if len(issues) > 0 {
			return fmt.Errorf("configuration needs explicit offline repair; see docs/guides/architecture-config-recovery.md")
		}
		return nil
	}
	if *target == "db" {
		return fmt.Errorf("--db is required for --target db")
	}
	if doc == nil {
		return fmt.Errorf("--static-config is required for --target static")
	}
	ops, err := readOperations(*operations)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if op.Family == "acp_launchers" {
			return fmt.Errorf("ACP launcher operations require --target db")
		}
	}
	for i := range static {
		static[i].Static = false
	}
	for i := range records {
		records[i].Static = true
	}
	staged, err := configrepair.Apply(append(static, records...), ops)
	if err != nil {
		return err
	}
	if err := configrepair.Validate(staged); err != nil {
		return fmt.Errorf("full recheck: %w", err)
	}
	var repaired []configrepair.Record
	for _, r := range staged {
		if !r.Static {
			repaired = append(repaired, r)
		}
	}
	data, err := doc.Encode(repaired)
	if err != nil {
		return err
	}
	// Reparse the final bytes to validate what the next startup will load.
	check, err := configrepair.DecodeStatic(data, *caddy)
	if err != nil {
		return err
	}
	final, err := check.Records()
	if err != nil {
		return err
	}
	if err := configrepair.Validate(append(final, records...)); err != nil {
		return err
	}
	if err := configrepair.WriteStatic(*staticPath, *backup, original, data); err != nil {
		return err
	}
	fmt.Fprintln(out, "Static repair saved and rechecked; backup:", *backup)
	return nil
}

type allowedModel struct {
	Name          string `json:"name"`
	UpstreamModel string `json:"upstream_model"`
}

func exportRepairs(ctx context.Context, out io.Writer, dbPath, outputPath string, static []configrepair.Record, explicit []configrepair.Operation, opts configrepair.ACPRepairOptions) error {
	db, err := configrepair.OpenSQLite(dbPath, false)
	if err != nil {
		return err
	}
	defer db.Close()
	ops, err := configrepair.GenerateSQLiteRepairs(ctx, db, static, explicit, opts)
	if err != nil {
		return fmt.Errorf("cannot export a complete repair batch: %w", err)
	}
	if len(ops) == 0 {
		if opts.ConvertConnectors {
			fmt.Fprintln(out, "Connector conversion validated. Apply with --convert-connectors and --backup.")
		} else {
			fmt.Fprintln(out, "Configuration passed validation. No repair operations needed.")
		}
		return nil
	}
	data, err := json.MarshalIndent(ops, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create repair export: %w", err)
	}
	_, writeErr := file.Write(append(data, '\n'))
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return errors.Join(err, os.Remove(outputPath))
	}
	fmt.Fprintf(out, "Exported %d validated repair operations to %s\n", len(ops), outputPath)
	if opts.ConvertConnectors {
		fmt.Fprintln(out, "Include --convert-connectors when applying this batch.")
	}
	return nil
}

func readOperations(path string) ([]configrepair.Operation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var ops []configrepair.Operation
	if err := decoder.Decode(&ops); err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("repair batch must not be empty")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("unexpected trailing JSON")
	}
	for _, op := range ops {
		switch op.Family {
		case "routes", "agents", "connectors", "mcp_services", "provider_models", "logical_models", "acp_launchers":
		default:
			return nil, fmt.Errorf("unknown repair family %q", op.Family)
		}
	}
	return ops, nil
}
