package ctlcmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	"github.com/agent-guide/agent-gateway/pkg/gatewaybundle"
	"github.com/spf13/cobra"
)

func (s *commandState) newGatewayClient() *adminclient.Client {
	return adminclient.New(adminclient.Config{
		BaseURL:   s.globalGatewayAddr,
		BasicAuth: s.gwAdminBasicAuth,
		Headers:   s.gwAdminHeaders,
	})
}

func parseOptionalJSONObjectFlag(raw string, flagName string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object: %w", flagName, err)
	}
	if payload == nil {
		return nil, fmt.Errorf("%s must be a JSON object", flagName)
	}
	return payload, nil
}
func (s *commandState) initGateway() {
	s.gatewayValidateCmd = &cobra.Command{
		Use:   "validate",
		Short: "Validate a gateway bundle YAML file locally",
		RunE: func(cmd *cobra.Command, args []string) error {
			if s.gatewayBundleFile == "" {
				return fmt.Errorf("--file is required")
			}
			bundle, err := gatewaybundle.LoadFile(s.gatewayBundleFile)
			if err != nil {
				return err
			}
			if err := bundle.ValidateForConfigStore(); err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(map[string]any{
					"status": "ok",
					"file":   s.gatewayBundleFile,
					"counts": map[string]int{
						"providers":      len(bundle.Providers),
						"managed_models": len(bundle.ManagedModels),
						"llm_routes":     len(bundle.LLMRoutes),
						"virtual_keys":   len(bundle.VirtualKeys),
						"mcp_services":   len(bundle.MCPServices),
						"mcp_routes":     len(bundle.MCPRoutes),
						"agent_routes":   len(bundle.AgentRoutes),
						"agents":         len(bundle.Agents),
					},
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "gateway bundle is valid: %s\n", s.gatewayBundleFile)
			return nil
		},
	}
	s.gatewayApplyCmd = &cobra.Command{
		Use:   "apply",
		Short: "Apply a gateway bundle YAML file to the remote admin API",
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.runGatewayApply(cmd.Context(), s.gatewayBundleFile)
		},
	}
	s.gatewayExportCmd = &cobra.Command{
		Use:   "export",
		Short: "Export gateway objects from the remote admin API as bundle YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			return s.runGatewayExport(cmd.Context(), s.gatewayExportFile)
		},
	}
	s.gatewayProviderCmd = &cobra.Command{
		Use:   "provider",
		Short: "Manage gateway providers",
	}
	s.gatewayProviderListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListProviders(cmd.Context(), adminclient.ProviderListOptions{})
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayProvidersTable(items)
			return nil
		},
	}
	s.gatewayProviderGetCmd = &cobra.Command{
		Use:   "get <provider-id>",
		Short: "Get one provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetProvider(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayProviderDeleteCmd = &cobra.Command{
		Use:   "delete <provider-id>",
		Short: "Delete a provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().DeleteProvider(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayProviderEnableCmd = &cobra.Command{
		Use:   "enable <provider-id>",
		Short: "Enable a provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().EnableProvider(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayProviderDisableCmd = &cobra.Command{
		Use:   "disable <provider-id>",
		Short: "Disable a provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().DisableProvider(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayLLMRouteCmd = &cobra.Command{
		Use:   "llm-route",
		Short: "Manage gateway LLM routes",
	}
	s.gatewayLLMRouteListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all LLM routes",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListLLMRoutes(cmd.Context(), adminclient.LLMRouteListOptions{})
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayLLMRoutesTable(items)
			return nil
		},
	}
	s.gatewayLLMRouteGetCmd = &cobra.Command{
		Use:   "get <llm-route-id>",
		Short: "Get one LLM route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetLLMRoute(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayLLMRouteDeleteCmd = &cobra.Command{
		Use:   "delete <llm-route-id>",
		Short: "Delete an LLM route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().DeleteLLMRoute(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayLLMRouteEnableCmd = &cobra.Command{
		Use:   "enable <llm-route-id>",
		Short: "Enable an LLM route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().EnableLLMRoute(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayLLMRouteDisableCmd = &cobra.Command{
		Use:   "disable <llm-route-id>",
		Short: "Disable an LLM route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().DisableLLMRoute(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayMCPServiceCmd = &cobra.Command{
		Use:   "mcp-service",
		Short: "Manage gateway MCP services",
	}
	s.gatewayMCPServiceListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all MCP services",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListMCPServices(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayMCPServicesTable(items)
			return nil
		},
	}
	s.gatewayMCPServiceGetCmd = &cobra.Command{
		Use:   "get <mcp-service-id>",
		Short: "Get one MCP service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetMCPService(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayMCPServiceDeleteCmd = &cobra.Command{
		Use:   "delete <mcp-service-id>",
		Short: "Delete an MCP service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().DeleteMCPService(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayMCPServiceSessionCmd = &cobra.Command{
		Use:   "session <mcp-service-id>",
		Short: "Get the active gateway session for an MCP service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetMCPServiceSession(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(item)
			}
			s.printGatewayMCPSessionTable(item)
			return nil
		},
	}
	s.gatewayMCPServiceCapabilitiesCmd = &cobra.Command{
		Use:   "capabilities <mcp-service-id>",
		Short: "Get the upstream initialize payload for an MCP service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetMCPServiceCapabilities(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayMCPServiceToolsCmd = &cobra.Command{
		Use:   "tools <mcp-service-id>",
		Short: "List tools exposed by an MCP service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListMCPServiceTools(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayMCPToolsTable(items)
			return nil
		},
	}
	s.gatewayMCPServiceToolCallCmd = &cobra.Command{
		Use:   "tool-call <mcp-service-id> <tool-name>",
		Short: "Call a tool exposed by an MCP service",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			arguments, err := parseOptionalJSONObjectFlag(s.gatewayMCPToolArguments, "--arguments")
			if err != nil {
				return err
			}
			item, err := s.newGatewayClient().CallMCPServiceTool(cmd.Context(), args[0], adminclient.MCPToolCallRequest{
				Name:      args[1],
				Arguments: arguments,
			})
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayMCPServiceResourcesCmd = &cobra.Command{
		Use:   "resources <mcp-service-id>",
		Short: "List resources exposed by an MCP service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListMCPServiceResources(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayMCPResourcesTable(items)
			return nil
		},
	}
	s.gatewayMCPServiceResourceTemplatesCmd = &cobra.Command{
		Use:   "resource-templates <mcp-service-id>",
		Short: "List resource templates exposed by an MCP service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListMCPServiceResourceTemplates(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayMCPResourceTemplatesTable(items)
			return nil
		},
	}
	s.gatewayMCPServiceResourceReadCmd = &cobra.Command{
		Use:   "resource-read <mcp-service-id> <resource-uri>",
		Short: "Read one resource exposed by an MCP service",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().ReadMCPServiceResource(cmd.Context(), args[0], adminclient.MCPResourceReadRequest{
				URI: args[1],
			})
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayMCPServicePromptsCmd = &cobra.Command{
		Use:   "prompts <mcp-service-id>",
		Short: "List prompts exposed by an MCP service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListMCPServicePrompts(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayMCPPromptsTable(items)
			return nil
		},
	}
	s.gatewayMCPServicePromptGetCmd = &cobra.Command{
		Use:   "prompt-get <mcp-service-id> <prompt-name>",
		Short: "Get one prompt from an MCP service",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			arguments, err := parseOptionalJSONObjectFlag(s.gatewayMCPPromptArguments, "--arguments")
			if err != nil {
				return err
			}
			item, err := s.newGatewayClient().GetMCPServicePrompt(cmd.Context(), args[0], adminclient.MCPPromptGetRequest{
				Name:      args[1],
				Arguments: arguments,
			})
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayMCPRouteCmd = &cobra.Command{
		Use:   "mcp-route",
		Short: "Manage gateway MCP routes",
	}
	s.gatewayMCPRouteListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all MCP routes",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListMCPRoutes(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayMCPRoutesTable(items)
			return nil
		},
	}
	s.gatewayMCPRouteGetCmd = &cobra.Command{
		Use:   "get <mcp-route-id>",
		Short: "Get one MCP route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetMCPRoute(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayMCPRouteDeleteCmd = &cobra.Command{
		Use:   "delete <mcp-route-id>",
		Short: "Delete an MCP route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().DeleteMCPRoute(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayMCPRuntimeCmd = &cobra.Command{
		Use:   "mcp-runtime",
		Short: "Inspect gateway MCP runtime state",
	}
	s.gatewayMCPRuntimeGetCmd = &cobra.Command{
		Use:   "get",
		Short: "Get the MCP runtime overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetMCPRuntime(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(item)
			}
			s.printGatewayMCPRuntimeOverview(item)
			return nil
		},
	}
	s.gatewayMCPRuntimeInFlightCmd = &cobra.Command{
		Use:   "inflight",
		Short: "List in-flight MCP requests",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListMCPRuntimeInFlight(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayMCPRuntimeInFlightTable(items)
			return nil
		},
	}
	s.gatewayMCPRuntimeProgressCmd = &cobra.Command{
		Use:   "progress",
		Short: "List MCP progress notifications",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListMCPRuntimeProgress(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayMCPRuntimeProgressTable(items)
			return nil
		},
	}
	s.gatewayMCPRuntimeHistoryCmd = &cobra.Command{
		Use:   "history",
		Short: "List completed MCP request history",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListMCPRuntimeHistory(cmd.Context(), s.gatewayMCPRuntimeRouteID)
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayMCPRuntimeHistoryTable(items)
			return nil
		},
	}
	s.gatewayVirtualKeyCmd = &cobra.Command{
		Use:   "virtualkey",
		Short: "Manage gateway virtual keys",
	}
	s.gatewayVirtualKeyListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all virtual keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListVirtualKeys(cmd.Context(), adminclient.VirtualKeyListOptions{})
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayVirtualKeysTable(items)
			return nil
		},
	}
	s.gatewayVirtualKeyGetCmd = &cobra.Command{
		Use:   "get <id>",
		Short: "Get one virtual key by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetVirtualKey(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayVirtualKeyDeleteCmd = &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a virtual key by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().DeleteVirtualKey(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayVirtualKeyEnableCmd = &cobra.Command{
		Use:   "enable <id>",
		Short: "Enable a virtual key by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().EnableVirtualKey(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayVirtualKeyDisableCmd = &cobra.Command{
		Use:   "disable <id>",
		Short: "Disable a virtual key by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().DisableVirtualKey(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayCredentialCmd = &cobra.Command{
		Use:   "credential",
		Short: "Manage gateway credentials",
	}
	s.gatewayModelsCmd = &cobra.Command{
		Use:   "model",
		Short: "Manage gateway models",
	}
	s.gatewayProviderTypesCmd = &cobra.Command{
		Use:   "provider-type",
		Short: "Inspect gateway provider types",
	}
	s.gatewayProviderTypesListCmd = &cobra.Command{
		Use:   "list",
		Short: "List provider types known to the gateway runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListProviderTypes(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayProviderTypesTable(items)
			return nil
		},
	}
	s.gatewayLLMAPIHandlerTypesCmd = &cobra.Command{
		Use:   "llm-api-handler-type",
		Short: "Manage gateway LLM API handler types",
	}
	s.gatewayLLMAPIHandlerTypesListCmd = &cobra.Command{
		Use:   "list",
		Short: "List LLM API handler types known to the gateway runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListLLMAPIHandlerTypes(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayLLMAPIHandlerTypesTable(items)
			return nil
		},
	}
	s.gatewayModelsDiscoveredCmd = &cobra.Command{
		Use:   "discovered",
		Short: "Manage discovered provider models",
	}
	s.gatewayModelsDiscoveredListCmd = &cobra.Command{
		Use:   "list <provider-id>",
		Short: "List discovered models for one provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListDiscoveredModels(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayDiscoveredModelsTable(items)
			return nil
		},
	}
	s.gatewayModelsDiscoveredRefreshCmd = &cobra.Command{
		Use:   "refresh <provider-id>",
		Short: "Refresh discovered models for one provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().RefreshProviderModels(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(resp)
			}
			s.printGatewayDiscoveredModelsTable(resp.Items)
			return nil
		},
	}
	s.gatewayModelsManagedCmd = &cobra.Command{
		Use:   "managed",
		Short: "Manage managed model overlays",
	}
	s.gatewayModelsManagedListCmd = &cobra.Command{
		Use:   "list",
		Short: "List managed models",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListManagedModels(cmd.Context(), adminclient.ManagedModelListOptions{
				ProviderID: s.gatewayManagedModelProviderID,
			})
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayManagedModelsTable(items)
			return nil
		},
	}
	s.gatewayModelsManagedGetCmd = &cobra.Command{
		Use:   "get <provider-id> <upstream-model>",
		Short: "Get one managed model overlay",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetManagedModel(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayModelsManagedDeleteCmd = &cobra.Command{
		Use:   "delete <provider-id> <upstream-model>",
		Short: "Delete a managed model overlay",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().DeleteManagedModel(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayCredentialListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListCredentials(cmd.Context(), adminclient.CredentialListOptions{
				Type:         s.gatewayCredentialType,
				ProviderType: s.gatewayCredentialProviderType,
				ProviderID:   s.gatewayCredentialProviderID,
			})
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayCredentialsTable(items)
			return nil
		},
	}
	s.gatewayCredentialGetCmd = &cobra.Command{
		Use:   "get <credential-id>",
		Short: "Get one credential",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetCredential(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayCredentialDeleteCmd = &cobra.Command{
		Use:   "delete <credential-id>",
		Short: "Delete a credential",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().DeleteCredential(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayModelsManagedListCmd.Flags().StringVar(&s.gatewayManagedModelProviderID, "provider-id", "", "filter managed models by provider ID")
	s.gatewayCredentialListCmd.Flags().StringVar(&s.gatewayCredentialType, "type", "", "filter credentials by type (api_key or oauth_token)")
	s.gatewayCredentialListCmd.Flags().StringVar(&s.gatewayCredentialProviderType, "provider-type", "", "filter credentials by provider type")
	s.gatewayCredentialListCmd.Flags().StringVar(&s.gatewayCredentialProviderID, "provider-id", "", "filter credentials by provider ID")
	s.gatewayMCPRuntimeHistoryCmd.Flags().StringVar(&s.gatewayMCPRuntimeRouteID, "route-id", "", "filter completed MCP requests by route ID")
	s.gatewayMCPServiceToolCallCmd.Flags().StringVar(&s.gatewayMCPToolArguments, "arguments", "", "JSON object passed as tool arguments")
	s.gatewayMCPServicePromptGetCmd.Flags().StringVar(&s.gatewayMCPPromptArguments, "arguments", "", "JSON object passed as prompt arguments")
	s.gatewayValidateCmd.Flags().StringVarP(&s.gatewayBundleFile, "file", "f", "", "path to gateway bundle YAML file")
	s.gatewayApplyCmd.Flags().StringVarP(&s.gatewayBundleFile, "file", "f", "", "path to gateway bundle YAML file")
	s.gatewayExportCmd.Flags().StringVarP(&s.gatewayExportFile, "file", "f", "", "write bundle YAML to this file instead of stdout")
	s.gatewayProviderCmd.AddCommand(s.gatewayProviderListCmd, s.gatewayProviderGetCmd, s.gatewayProviderDeleteCmd, s.gatewayProviderEnableCmd, s.gatewayProviderDisableCmd)
	s.gatewayLLMRouteCmd.AddCommand(s.gatewayLLMRouteListCmd, s.gatewayLLMRouteGetCmd, s.gatewayLLMRouteDeleteCmd, s.gatewayLLMRouteEnableCmd, s.gatewayLLMRouteDisableCmd)
	s.gatewayMCPServiceCmd.AddCommand(s.gatewayMCPServiceListCmd, s.gatewayMCPServiceGetCmd, s.gatewayMCPServiceDeleteCmd, s.gatewayMCPServiceSessionCmd, s.gatewayMCPServiceCapabilitiesCmd, s.gatewayMCPServiceToolsCmd, s.gatewayMCPServiceToolCallCmd, s.gatewayMCPServiceResourcesCmd, s.gatewayMCPServiceResourceTemplatesCmd, s.gatewayMCPServiceResourceReadCmd, s.gatewayMCPServicePromptsCmd, s.gatewayMCPServicePromptGetCmd)
	s.gatewayMCPRouteCmd.AddCommand(s.gatewayMCPRouteListCmd, s.gatewayMCPRouteGetCmd, s.gatewayMCPRouteDeleteCmd)
	s.gatewayMCPRuntimeCmd.AddCommand(s.gatewayMCPRuntimeGetCmd, s.gatewayMCPRuntimeInFlightCmd, s.gatewayMCPRuntimeProgressCmd, s.gatewayMCPRuntimeHistoryCmd)
	s.gatewayVirtualKeyCmd.AddCommand(s.gatewayVirtualKeyListCmd, s.gatewayVirtualKeyGetCmd, s.gatewayVirtualKeyDeleteCmd, s.gatewayVirtualKeyEnableCmd, s.gatewayVirtualKeyDisableCmd)
	s.gatewayCredentialCmd.AddCommand(s.gatewayCredentialListCmd, s.gatewayCredentialGetCmd, s.gatewayCredentialDeleteCmd)
	s.gatewayModelsDiscoveredCmd.AddCommand(s.gatewayModelsDiscoveredListCmd, s.gatewayModelsDiscoveredRefreshCmd)
	s.gatewayModelsManagedCmd.AddCommand(s.gatewayModelsManagedListCmd, s.gatewayModelsManagedGetCmd, s.gatewayModelsManagedDeleteCmd)
	s.gatewayModelsCmd.AddCommand(s.gatewayModelsDiscoveredCmd, s.gatewayModelsManagedCmd)
	s.gatewayProviderTypesCmd.AddCommand(s.gatewayProviderTypesListCmd)
	s.gatewayLLMAPIHandlerTypesCmd.AddCommand(s.gatewayLLMAPIHandlerTypesListCmd)
	s.rootCmd.AddCommand(s.gatewayValidateCmd, s.gatewayApplyCmd, s.gatewayExportCmd, s.gatewayProviderCmd, s.gatewayLLMRouteCmd, s.gatewayMCPServiceCmd, s.gatewayMCPRouteCmd, s.gatewayMCPRuntimeCmd, s.gatewayVirtualKeyCmd, s.gatewayCredentialCmd, s.gatewayProviderTypesCmd, s.gatewayLLMAPIHandlerTypesCmd, s.gatewayModelsCmd)
}
