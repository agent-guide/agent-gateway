package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/agent-guide/caddy-agent-gateway/internal/agwctl/model"
)

var outputFormat string

func initOutputFlag() {
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "output format: table or json")
}

// newTabWriter returns a tabwriter suitable for aligned CLI output.
func newTabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}

// printTable writes headers and rows to stdout using tabwriter.
func printTable(headers []string, rows [][]string) {
	w := newTabWriter()
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	sep := make([]string, len(headers))
	for i, h := range headers {
		sep[i] = strings.Repeat("-", len(h))
	}
	fmt.Fprintln(w, strings.Join(sep, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	w.Flush()
}

// printJSON pretty-prints v as JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// boolStr returns "yes" or "no" for boolean values.
func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// dash returns s if non-empty, otherwise "-".
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// joinOrDash joins a string slice with sep; returns "-" if empty.
func joinOrDash(parts []string, sep string) string {
	s := strings.Join(parts, sep)
	return dash(s)
}

// ── Caddy table formatters ────────────────────────────────────────────────────

func printCaddyServersTable(servers []*model.ServerResponse) {
	headers := []string{"ID", "LISTEN", "READ-ONLY", "SOURCE", "PUBLIC-URL"}
	rows := make([][]string, 0, len(servers))
	for _, s := range servers {
		rows = append(rows, []string{
			dash(s.ID),
			joinOrDash(s.Listen, " "),
			boolStr(s.ReadOnly),
			dash(s.Source),
			dash(s.PublicURL),
		})
	}
	printTable(headers, rows)
}

func printCaddyRoutesTable(routes []*model.RouteResponse) {
	headers := []string{"ID", "ORDER", "PATHS", "HOSTS", "HANDLERS"}
	rows := make([][]string, 0, len(routes))
	for _, r := range routes {
		handlerDescs := make([]string, 0, len(r.Handlers))
		for _, h := range r.Handlers {
			handlerDescs = append(handlerDescs, describeHandler(h))
		}
		rows = append(rows, []string{
			dash(r.ID),
			fmt.Sprintf("%d", r.Order),
			joinOrDash(r.Match.Paths, " "),
			joinOrDash(r.Match.Hosts, " "),
			joinOrDash(handlerDescs, " "),
		})
	}
	printTable(headers, rows)
}

func describeHandler(h model.HandlerConf) string {
	switch h.Type {
	case "agent_route_dispatcher":
		if len(h.APIs) > 0 {
			return "agent_route_dispatcher(" + strings.Join(h.APIs, ",") + ")"
		}
		return "agent_route_dispatcher"
	case "reverse_proxy":
		if h.Upstream != "" {
			return "reverse_proxy(" + h.Upstream + ")"
		}
		return "reverse_proxy"
	case "file_server":
		if h.Root != "" {
			return "file_server(" + h.Root + ")"
		}
		return "file_server"
	default:
		return h.Type
	}
}

// ── Gateway table formatters ──────────────────────────────────────────────────

// printGatewayProvidersTable renders a list of ProviderView items.
// Fields: id, provider_type, default_model, disabled, source.
func printGatewayProvidersTable(items []map[string]any) {
	headers := []string{"ID", "TYPE", "DEFAULT-MODEL", "DISABLED", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(strField(item, "id")),
			dash(strField(item, "provider_type")),
			dash(strField(item, "default_model")),
			boolStr(boolField(item, "disabled")),
			dash(strField(item, "source")),
		})
	}
	printTable(headers, rows)
}

// printGatewayRoutesTable renders a list of RouteView items.
// Fields: id, llm_api, path_prefix (from match), disabled, targets, source.
func printGatewayRoutesTable(items []map[string]any) {
	headers := []string{"ID", "LLM-API", "PATH-PREFIX", "DISABLED", "TARGETS", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		pathPrefix := "-"
		if match, ok := item["match"].(map[string]any); ok {
			pathPrefix = dash(strField(match, "path_prefix"))
		}
		targets := extractTargetIDs(item)
		rows = append(rows, []string{
			dash(strField(item, "id")),
			dash(strField(item, "llm_api")),
			pathPrefix,
			boolStr(boolField(item, "disabled")),
			joinOrDash(targets, ","),
			dash(strField(item, "source")),
		})
	}
	printTable(headers, rows)
}

// printGatewayVirtualKeysTable renders a list of VirtualKeyView items.
// Fields: key (truncated), tag, disabled, allowed_route_ids, source.
func printGatewayVirtualKeysTable(items []map[string]any) {
	headers := []string{"KEY", "TAG", "DISABLED", "ALLOWED-ROUTES", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		key := strField(item, "key")
		if len(key) > 16 {
			key = key[:16] + "..."
		}
		routes := strSliceField(item, "allowed_route_ids")
		rows = append(rows, []string{
			dash(key),
			dash(strField(item, "tag")),
			boolStr(boolField(item, "disabled")),
			joinOrDash(routes, ","),
			dash(strField(item, "source")),
		})
	}
	printTable(headers, rows)
}

// printGatewayCredentialsTable renders a list of CredentialView items.
// Fields: id, provider_id, provider_type, label, disabled, unavailable.
func printGatewayCredentialsTable(items []map[string]any) {
	headers := []string{"ID", "PROVIDER-ID", "TYPE", "LABEL", "DISABLED", "UNAVAILABLE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(strField(item, "id")),
			dash(strField(item, "provider_id")),
			dash(strField(item, "provider_type")),
			dash(strField(item, "label")),
			boolStr(boolField(item, "disabled")),
			boolStr(boolField(item, "unavailable")),
		})
	}
	printTable(headers, rows)
}

// ── map[string]any helpers ────────────────────────────────────────────────────

func strField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func strSliceField(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func extractTargetIDs(item map[string]any) []string {
	raw, ok := item["targets"].([]any)
	if !ok {
		return nil
	}
	var ids []string
	for _, t := range raw {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if id := strField(tm, "provider_id"); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
