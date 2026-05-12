package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/agent-guide/agent-gateway/internal/agwctl/caddyadminclient"
	"github.com/agent-guide/agent-gateway/pkg/adminclient"
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

func printCaddyServersTable(servers []*caddyadminclient.ServerResponse) {
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

func printCaddyRoutesTable(routes []*caddyadminclient.RouteResponse) {
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

func describeHandler(h caddyadminclient.HandlerConf) string {
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
func printGatewayProvidersTable(items []adminclient.Provider) {
	headers := []string{"ID", "TYPE", "DEFAULT-MODEL", "DISABLED", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.Id),
			dash(item.ProviderType),
			dash(item.DefaultModel),
			boolStr(item.Disabled),
			dash(item.Source),
		})
	}
	printTable(headers, rows)
}

// printGatewayRoutesTable renders a list of RouteView items.
// Fields: id, llm_api, path_prefix (from match), disabled, target, source.
func printGatewayRoutesTable(items []adminclient.Route) {
	headers := []string{"ID", "LLM-API", "PATH-PREFIX", "DISABLED", "TARGET", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.ID),
			dash(item.LLMAPI),
			dash(item.Match.PathPrefix),
			boolStr(item.Disabled),
			dash(extractRouteTargetID(item)),
			dash(item.Source),
		})
	}
	printTable(headers, rows)
}

// printGatewayVirtualKeysTable renders a list of VirtualKeyView items.
// Fields: id, key (truncated), tag, disabled, allowed_route_ids, source.
func printGatewayVirtualKeysTable(items []adminclient.VirtualKey) {
	headers := []string{"ID", "KEY", "TAG", "DISABLED", "ALLOWED-ROUTES", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		key := item.Key
		if len(key) > 16 {
			key = key[:16] + "..."
		}
		rows = append(rows, []string{
			dash(item.ID),
			dash(key),
			dash(item.Tag),
			boolStr(item.Disabled),
			joinOrDash(item.AllowedRouteIDs, ","),
			dash(item.Source),
		})
	}
	printTable(headers, rows)
}

// printGatewayCredentialsTable renders a list of CredentialView items.
// Fields: id, provider_id, provider_type, label, disabled, unavailable.
func printGatewayCredentialsTable(items []adminclient.Credential) {
	headers := []string{"ID", "PROVIDER-ID", "TYPE", "SOURCE", "LABEL", "DISABLED", "UNAVAILABLE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.ID),
			dash(item.ProviderID),
			dash(item.ProviderType),
			dash(item.Source),
			dash(item.Label),
			boolStr(item.Disabled),
			boolStr(item.Unavailable),
		})
	}
	printTable(headers, rows)
}

func printGatewayDiscoveredModelsTable(items []adminclient.DiscoveredModel) {
	headers := []string{"PROVIDER-ID", "UPSTREAM-MODEL", "TYPE", "STATUS", "DISPLAY-NAME", "LAST-ERROR"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.ProviderID),
			dash(item.UpstreamModel),
			dash(item.ProviderType),
			dash(string(item.Status)),
			dash(item.DisplayName),
			dash(item.LastError),
		})
	}
	printTable(headers, rows)
}

func printGatewayManagedModelsTable(items []adminclient.ManagedModel) {
	headers := []string{"PROVIDER-ID", "UPSTREAM-MODEL", "ENABLED", "SCOPE", "TYPE", "SNAPSHOT"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.ProviderID),
			dash(item.UpstreamModel),
			boolStr(item.Enabled),
			dash(item.CredentialScope),
			dash(item.ProviderType),
			dash(string(item.SnapshotState)),
		})
	}
	printTable(headers, rows)
}

func printGatewayCLIAuthAuthenticatorsTable(items []adminclient.CLIAuthAuthenticator) {
	headers := []string{"NAME", "PROVIDER-TYPE", "ENABLED", "CALLBACK-PORT", "NO-BROWSER", "DEVICE-FLOW"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.Name),
			dash(item.ProviderType),
			boolStr(item.Enabled),
			fmt.Sprintf("%d", item.Config.CallbackPort),
			boolStr(item.Config.NoBrowser),
			boolStr(item.Config.DeviceFlow),
		})
	}
	printTable(headers, rows)
}

func printGatewayProviderTypesTable(items []adminclient.ProviderType) {
	headers := []string{"PROVIDER-TYPE", "ENABLED"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.ProviderType),
			boolStr(item.Enabled),
		})
	}
	printTable(headers, rows)
}

func printGatewayLLMAPIHandlerTypesTable(items []adminclient.LLMAPIHandlerType) {
	headers := []string{"HANDLER-TYPE", "ENABLED"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.LLMApiHandlerType),
			boolStr(item.Enabled),
		})
	}
	printTable(headers, rows)
}

func extractRouteTargetID(item adminclient.Route) string {
	if item.TargetPolicy.ProviderID != "" {
		return item.TargetPolicy.ProviderID
	}
	return item.TargetPolicy.ProviderTarget.ProviderID
}
