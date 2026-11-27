package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

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
	migrateACP := flags.Bool("migrate-acp-launchers", false, "migrate legacy ACP Agents and their launchers into SQLite after stopping services")
	importACP := flags.String("import-acp-launchers", "", "import a previous deployment launcher file into SQLite once")
	setACP := flags.String("set-acp-launcher", "", "store a deployment launcher by name, with executable arguments after --")
	acpCommand := flags.String("acp-command", "", "executable for --set-acp-launcher")
	acpCWDFlag := flags.String("acp-cwd-flag", "", "optional working-directory flag for --set-acp-launcher")
	operations := flags.String("repair", "", "JSON file containing repair operations to apply after stopping services")
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
	if *setACP == "" && (*acpCommand != "" || *acpCWDFlag != "" || flags.NArg() != 0) {
		return fmt.Errorf("executable settings and positional arguments require --set-acp-launcher")
	}
	if *setACP != "" && (*acpCommand == "" || *migrateACP || *importACP != "") {
		return fmt.Errorf("--set-acp-launcher requires --acp-command and must run separately from ACP migration or import")
	}
	acpWrite := *migrateACP || *importACP != "" || *setACP != ""
	if acpWrite && (*dbPath == "" || *target != "db") {
		return fmt.Errorf("ACP launcher changes require --db and --target db")
	}
	acpOptions := configrepair.ACPRepairOptions{Migrate: *migrateACP, Import: *importACP, ConvertConnectors: *convertConnectors}
	if *setACP != "" {
		acpOptions.Set = &launcher.Definition{ID: *setACP, Launcher: launcher.Launcher{Command: *acpCommand, Args: flags.Args(), CWDFlag: *acpCWDFlag}}
	}
	if (*operations != "" || *convertConnectors || acpWrite) && *backup == "" {
		return fmt.Errorf("repairs require an unused --backup path")
	}
	if *convertConnectors {
		if *dbPath == "" || *staticPath != "" {
			return fmt.Errorf("--convert-connectors requires --db and --backup for a SQLite-only conversion")
		}
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
		if op.Family != "routes" && op.Family != "agents" && op.Family != "connectors" && op.Family != "mcp_services" && op.Family != "provider_models" && op.Family != "logical_models" {
			return nil, fmt.Errorf("repair supports routes, agents, connectors, provider_models, logical_models and legacy mcp_services only")
		}
	}
	return ops, nil
}
