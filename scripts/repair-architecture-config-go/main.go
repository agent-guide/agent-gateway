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
	convertConnectors := flags.Bool("convert-connectors", false, "atomically convert legacy MCP services and Agent/route references")
	operations := flags.String("repair", "", "JSON file containing an explicit repair operation array; omit for inspection")
	target := flags.String("target", "db", "repair target: db or static (the other source is read-only)")
	backup := flags.String("backup", "", "unused backup filename, required for repairs")
	stopped := flags.Bool("service-stopped", false, "confirm all services using these configuration files are stopped")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*dbPath == "" && *staticPath == "") {
		return fmt.Errorf("provide --db and/or --static-config; unexpected positional arguments are rejected")
	}
	if *target != "db" && *target != "static" {
		return fmt.Errorf("--target must be db or static")
	}
	if (*operations != "" || *convertConnectors) && (!*stopped || *backup == "") {
		return fmt.Errorf("repairs require --service-stopped and an unused --backup path")
	}
	if *convertConnectors {
		if *dbPath == "" || *staticPath != "" {
			return fmt.Errorf("--convert-connectors requires only --db, --backup and --service-stopped")
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
		if err := configrepair.ConvertConnectors(ctx, db, *backup, *stopped, ops...); err != nil {
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
		db, err := configrepair.OpenSQLite(*dbPath, *operations != "" && *target == "db")
		if err != nil {
			return err
		}
		defer db.Close()
		records, err = configrepair.ReadSQLite(ctx, db)
		if err != nil {
			return err
		}
		if *operations != "" && *target == "db" {
			ops, err := readOperations(*operations)
			if err != nil {
				return err
			}
			if err := configrepair.RepairSQLite(ctx, db, *backup, *stopped, static, ops); err != nil {
				return err
			}
			fmt.Fprintln(out, "Repair committed and rechecked; backup:", *backup)
			return nil
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
		if op.Family != "routes" && op.Family != "agents" && op.Family != "connectors" && op.Family != "mcp_services" {
			return nil, fmt.Errorf("repair supports routes, agents, connectors and legacy mcp_services only")
		}
	}
	return ops, nil
}
