package ctlcmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/agent-guide/agent-gateway/internal/agwctl/caddyadminclient"
	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	llmroutepkg "github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
)

func (s *commandState) initOutputFlag() {
	s.rootCmd.PersistentFlags().StringVarP(&s.outputFormat, "output", "o", "table", "output format: table or json")
}

func (s *commandState) newTabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(s.rootCmd.OutOrStdout(), 0, 0, 2, ' ', 0)
}

func (s *commandState) printTable(headers []string, rows [][]string) {
	w := s.newTabWriter()
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

func (s *commandState) printJSON(v any) error {
	enc := json.NewEncoder(s.rootCmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func joinOrDash(parts []string, sep string) string {
	s := strings.Join(parts, sep)
	return dash(s)
}

func (s *commandState) printCaddyServersTable(servers []*caddyadminclient.ServerResponse) {
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
	s.printTable(headers, rows)
}

func (s *commandState) printCaddyRoutesTable(routes []*caddyadminclient.RouteResponse) {
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
	s.printTable(headers, rows)
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

func (s *commandState) printGatewayProvidersTable(items []adminclient.Provider) {
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
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayLLMRoutesTable(items []adminclient.LLMRoute) {
	headers := []string{"ID", "PROTOCOL", "PATH-PREFIX", "DISABLED", "TARGET", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.ID),
			dash(string(item.Protocol)),
			dash(item.MatchPolicy.PathPrefix),
			boolStr(item.Disabled),
			dash(extractLLMRouteTargetID(item)),
			dash(item.Source),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayMCPServicesTable(items []adminclient.MCPServiceView) {
	headers := []string{"ID", "NAME", "TRANSPORT", "ENDPOINT", "DISABLED", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		endpoint := item.URL
		if item.Transport == "stdio" {
			endpoint = item.Command
		}
		rows = append(rows, []string{
			dash(item.ID),
			dash(item.Name),
			dash(string(item.Transport)),
			dash(endpoint),
			boolStr(item.Disabled),
			dash(item.Source),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayMCPSessionTable(item *adminclient.MCPServiceSessionView) {
	headers := []string{"SERVICE-ID", "SESSION-ID", "UPSTREAM-SESSION-ID", "TRANSPORT", "STATE", "CREATED-AT", "LAST-USED-AT"}
	rows := [][]string{}
	if item != nil {
		rows = append(rows, []string{
			dash(item.ServiceID),
			dash(item.ID),
			dash(item.UpstreamSessionID),
			dash(string(item.Transport)),
			dash(string(item.State)),
			dash(formatTimestamp(item.CreatedAt)),
			dash(formatTimestamp(item.LastUsedAt)),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayMCPToolsTable(items []adminclient.MCPTool) {
	headers := []string{"NAME", "DESCRIPTION"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.Name),
			dash(item.Description),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayMCPResourcesTable(items []adminclient.MCPResource) {
	headers := []string{"URI", "NAME", "MIME-TYPE", "DESCRIPTION"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.URI),
			dash(item.Name),
			dash(item.MimeType),
			dash(item.Description),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayMCPResourceTemplatesTable(items []adminclient.MCPResourceTemplate) {
	headers := []string{"NAME", "TITLE", "URI-TEMPLATE", "MIME-TYPE", "DESCRIPTION"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.Name),
			dash(item.Title),
			dash(item.URITemplate),
			dash(item.MimeType),
			dash(item.Description),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayMCPPromptsTable(items []adminclient.MCPPrompt) {
	headers := []string{"NAME", "DESCRIPTION"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.Name),
			dash(item.Description),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayMCPRoutesTable(items []adminclient.MCPRouteView) {
	headers := []string{"ID", "PATH-PREFIX", "SERVICE-ID", "VIRTUALKEY", "DISABLED", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.ID),
			dash(item.MatchPolicy.PathPrefix),
			dash(item.ServiceID),
			boolStr(item.AuthPolicy.RequireVirtualKey),
			boolStr(item.Disabled),
			dash(item.Source),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayMCPRuntimeOverview(runtime *adminclient.MCPRuntimeView) {
	if runtime == nil {
		s.printGatewayMCPRuntimeInFlightTable(nil)
		fmt.Fprintln(s.rootCmd.OutOrStdout())
		s.printGatewayMCPRuntimeProgressTable(nil)
		return
	}
	s.printGatewayMCPRuntimeInFlightTable(runtime.InFlight)
	fmt.Fprintln(s.rootCmd.OutOrStdout())
	s.printGatewayMCPRuntimeProgressTable(runtime.Progress)
}

func (s *commandState) printGatewayMCPRuntimeInFlightTable(items []adminclient.MCPRuntimeInFlightRequest) {
	headers := []string{"ROUTE-ID", "REQUEST-ID", "METHOD", "STARTED-AT", "PROGRESS-TOKEN", "CANCELLED"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.RouteID),
			dash(fmt.Sprint(item.RequestID)),
			dash(item.Method),
			dash(formatTimestamp(item.StartedAt)),
			dash(fmt.Sprint(item.ProgressToken)),
			boolStr(!item.CancelledAt.IsZero()),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayMCPRuntimeProgressTable(items []adminclient.MCPRuntimeProgressNotification) {
	headers := []string{"ROUTE-ID", "REQUEST-ID", "METHOD", "PROGRESS", "TOTAL", "MESSAGE", "UPDATED-AT"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		total := "-"
		if item.Total != nil {
			total = fmt.Sprintf("%g", *item.Total)
		}
		rows = append(rows, []string{
			dash(item.RouteID),
			dash(fmt.Sprint(item.RequestID)),
			dash(item.LastMethod),
			fmt.Sprintf("%g", item.Progress),
			total,
			dash(item.Message),
			dash(formatTimestamp(item.UpdatedAt)),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayMCPRuntimeHistoryTable(items []adminclient.MCPRuntimeCompletedRequest) {
	headers := []string{"ROUTE-ID", "REQUEST-ID", "METHOD", "STARTED-AT", "COMPLETED-AT", "CANCELLED", "ERROR"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.RouteID),
			dash(fmt.Sprint(item.RequestID)),
			dash(item.Method),
			dash(formatTimestamp(item.StartedAt)),
			dash(formatTimestamp(item.CompletedAt)),
			boolStr(item.Cancelled),
			dash(item.Error),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayAgentsTable(items []adminclient.AgentView) {
	headers := []string{"ID", "NAME", "RUNTIME", "RUNTIME-STATE", "EXECUTABLE", "RUNTIME-TARGET", "DISABLED", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		target := ""
		if item.Runtime.ACP != nil {
			target = item.Runtime.ACP.AgentType
		} else if item.Runtime.HTTP != nil {
			target = item.Runtime.HTTP.CardURL
		}
		state := "unknown"
		if item.RuntimeStatus != nil && item.RuntimeStatus.State != "" {
			state = string(item.RuntimeStatus.State)
		}
		executable := "unknown"
		if item.Capabilities != nil {
			executable = boolStr(item.Capabilities.Executable)
		}
		rows = append(rows, []string{
			dash(item.ID),
			dash(item.Name),
			dash(item.Runtime.Type),
			dash(state),
			executable,
			dash(target),
			boolStr(item.Disabled),
			dash(item.Source),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayACPRuntimeOverview(runtime *adminclient.ACPRuntimeView) {
	if runtime == nil {
		runtime = &adminclient.ACPRuntimeView{}
	}
	s.printGatewayACPInFlightTable(runtime.InFlight)
	fmt.Fprintln(s.rootCmd.OutOrStdout())
	s.printGatewayACPInstancesTable(runtime.Instances)
	fmt.Fprintln(s.rootCmd.OutOrStdout())
	s.printGatewayACPPendingPermissionsTable(runtime.PendingPermissions)
}

func (s *commandState) printGatewayACPInFlightTable(items []adminclient.ACPInFlightTurn) {
	headers := []string{"SCOPE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{dash(item.Scope)})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayACPInstancesTable(items []adminclient.ACPPooledInstanceInfo) {
	headers := []string{"SCOPE", "SESSION-ID", "ALIVE", "ACTIVE", "LAST-USED"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.Scope),
			dash(item.SessionID),
			boolStr(item.Alive),
			boolStr(item.Active),
			dash(formatTimestamp(item.LastUsed)),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayACPPendingPermissionsTable(items []adminclient.ACPPendingPermissionInfo) {
	headers := []string{"REQUEST-ID", "AGENT-ID", "SESSION-ID", "CREATED-AT"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.RequestID),
			dash(item.OwnerID),
			dash(item.SessionID),
			dash(formatTimestamp(item.CreatedAt)),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayVirtualKeysTable(items []adminclient.VirtualKey) {
	headers := []string{"ID", "KEY", "TAG", "DISABLED", "ALLOWED-ROUTES", "RATE-LIMITS", "SOURCE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		key := item.Key
		if len(key) > 16 {
			key = key[:16] + "..."
		}
		var limits []string
		if item.RateLimits != nil {
			if limit := item.RateLimits.LLM; limit != nil {
				limits = append(limits, fmt.Sprintf("llm=%d/%d", limit.RequestsPerMinute, limit.Burst))
			}
			if limit := item.RateLimits.MCP; limit != nil {
				limits = append(limits, fmt.Sprintf("mcp=%d/%d", limit.RequestsPerMinute, limit.Burst))
			}
			if limit := item.RateLimits.Agent; limit != nil {
				limits = append(limits, fmt.Sprintf("agent=%d/%d", limit.RequestsPerMinute, limit.Burst))
			}
		}
		rows = append(rows, []string{
			dash(item.ID),
			dash(key),
			dash(item.Tag),
			boolStr(item.Disabled),
			joinOrDash(item.AllowedRouteIDs, ","),
			dash(strings.Join(limits, ",")),
			dash(item.Source),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayCredentialsTable(items []adminclient.Credential) {
	headers := []string{"ID", "PROVIDER-ID", "PROVIDER-TYPE", "TYPE", "LABEL", "DISABLED", "UNAVAILABLE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.ID),
			dash(item.ProviderID),
			dash(item.ProviderType),
			dash(item.Type),
			dash(item.Label),
			boolStr(item.Disabled),
			boolStr(item.Unavailable),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayDiscoveredModelsTable(items []adminclient.DiscoveredModel) {
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
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayManagedModelsTable(items []adminclient.ManagedModel) {
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
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayProviderTypesTable(items []adminclient.ProviderType) {
	headers := []string{"PROVIDER-TYPE", "ENABLED"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.ProviderType),
			boolStr(item.Enabled),
		})
	}
	s.printTable(headers, rows)
}

func (s *commandState) printGatewayLLMAPIHandlerTypesTable(items []adminclient.LLMAPIHandlerType) {
	headers := []string{"HANDLER-TYPE"}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			dash(item.LLMApiHandlerType),
		})
	}
	s.printTable(headers, rows)
}

func formatTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339)
}

func extractLLMRouteTargetID(item adminclient.LLMRoute) string {
	routeCfg, err := item.LLMRouteConfig()
	if err != nil {
		return ""
	}
	if directPolicy, ok := llmroutepkg.DirectProviderPolicyOf(routeCfg.TargetPolicy); ok {
		if directPolicy.ProviderID != "" {
			return directPolicy.ProviderID
		}
		return directPolicy.ProviderTarget.ProviderID
	}
	return ""
}
