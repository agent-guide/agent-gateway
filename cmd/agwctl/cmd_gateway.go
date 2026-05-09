package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/pkg/adminclient"
	"github.com/agent-guide/caddy-agent-gateway/pkg/cliauth"
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/cliauth/authenticator"
	"github.com/agent-guide/caddy-agent-gateway/pkg/gatewaybundle"
	"github.com/spf13/cobra"

	_ "github.com/agent-guide/caddy-agent-gateway/pkg/dispatcher/llmapi/anthropic"
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/dispatcher/llmapi/openai"
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider/anthropic"
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider/deepseek"
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider/gemini"
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider/ollama"
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider/openai"
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider/openrouter"
	_ "github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider/zhipu"
)

var (
	gwUser     string
	gwPassword string

	gatewayBundleFile string
	gatewayExportFile string

	gatewayManagedModelProviderID string

	gatewayCLIAuthAuthenticatorConfigFile string
	gatewayCLIAuthAuthenticatorState      string
	gatewayCLIAuthCallbackPort            int
	gatewayCLIAuthNoBrowser               bool
	gatewayCLIAuthDeviceFlow              bool
	gatewayCLIAuthLoginWait               bool
	gatewayCLIAuthPollInterval            time.Duration
)

// ── gateway ───────────────────────────────────────────────────────────────────

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Manage the remote caddy-agent-gateway via its admin API",
}

var gatewayValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a gateway bundle YAML file locally",
	RunE: func(cmd *cobra.Command, args []string) error {
		if gatewayBundleFile == "" {
			return fmt.Errorf("--file is required")
		}
		bundle, err := gatewaybundle.LoadFile(gatewayBundleFile)
		if err != nil {
			return err
		}
		if err := bundle.Validate(); err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(map[string]any{
				"status": "ok",
				"file":   gatewayBundleFile,
				"counts": map[string]int{
					"provider_types":         len(bundle.ProviderTypes),
					"llm_api_handler_types":  len(bundle.LLMAPIHandlerTypes),
					"providers":              len(bundle.Providers),
					"managed_models":         len(bundle.ManagedModels),
					"routes":                 len(bundle.Routes),
					"virtual_keys":           len(bundle.VirtualKeys),
					"cliauth_authenticators": len(bundle.CLIAuthAuthenticators),
				},
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "gateway bundle is valid: %s\n", gatewayBundleFile)
		return nil
	},
}

var gatewayApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a gateway bundle YAML file to the remote admin API",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGatewayApply(context.Background(), gatewayBundleFile)
	},
}

var gatewayExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export gateway objects from the remote admin API as bundle YAML",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGatewayExport(context.Background(), gatewayExportFile)
	},
}

// ── gateway provider ─────────────────────────────────────────────────────────

var gatewayProviderCmd = &cobra.Command{
	Use:   "provider",
	Short: "Manage gateway providers",
}

var gatewayProviderListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all providers",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := newGatewayClient().ListProviders(context.Background(), adminclient.ProviderListOptions{})
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(items)
		}
		printGatewayProvidersTable(items)
		return nil
	},
}

var gatewayProviderGetCmd = &cobra.Command{
	Use:   "get <provider-id>",
	Short: "Get one provider",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().GetProvider(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayProviderDeleteCmd = &cobra.Command{
	Use:   "delete <provider-id>",
	Short: "Delete a provider",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().DeleteProvider(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayProviderEnableCmd = &cobra.Command{
	Use:   "enable <provider-id>",
	Short: "Enable a provider",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().EnableProvider(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayProviderDisableCmd = &cobra.Command{
	Use:   "disable <provider-id>",
	Short: "Disable a provider",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().DisableProvider(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

// ── gateway route ─────────────────────────────────────────────────────────────

var gatewayRouteCmd = &cobra.Command{
	Use:   "route",
	Short: "Manage gateway routes",
}

var gatewayRouteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all routes",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := newGatewayClient().ListRoutes(context.Background(), adminclient.RouteListOptions{})
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(items)
		}
		printGatewayRoutesTable(items)
		return nil
	},
}

var gatewayRouteGetCmd = &cobra.Command{
	Use:   "get <route-id>",
	Short: "Get one route",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().GetRoute(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayRouteDeleteCmd = &cobra.Command{
	Use:   "delete <route-id>",
	Short: "Delete a route",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().DeleteRoute(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayRouteEnableCmd = &cobra.Command{
	Use:   "enable <route-id>",
	Short: "Enable a route",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().EnableRoute(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayRouteDisableCmd = &cobra.Command{
	Use:   "disable <route-id>",
	Short: "Disable a route",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().DisableRoute(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

// ── gateway virtualkey ───────────────────────────────────────────────────────

var gatewayVirtualKeyCmd = &cobra.Command{
	Use:   "virtualkey",
	Short: "Manage gateway virtual keys",
}

var gatewayVirtualKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all virtual keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := newGatewayClient().ListVirtualKeys(context.Background(), adminclient.VirtualKeyListOptions{})
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(items)
		}
		printGatewayVirtualKeysTable(items)
		return nil
	},
}

var gatewayVirtualKeyGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get one virtual key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().GetVirtualKey(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayVirtualKeyDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Delete a virtual key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().DeleteVirtualKey(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayVirtualKeyEnableCmd = &cobra.Command{
	Use:   "enable <key>",
	Short: "Enable a virtual key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().EnableVirtualKey(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayVirtualKeyDisableCmd = &cobra.Command{
	Use:   "disable <key>",
	Short: "Disable a virtual key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().DisableVirtualKey(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

// ── gateway credential ────────────────────────────────────────────────────────

var gatewayCredentialCmd = &cobra.Command{
	Use:   "credential",
	Short: "Manage gateway credentials",
}

// ── gateway models ───────────────────────────────────────────────────────────

var gatewayModelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Manage gateway models",
}

// ── gateway provider types ───────────────────────────────────────────────────

var gatewayProviderTypesCmd = &cobra.Command{
	Use:   "provider-types",
	Short: "Manage gateway provider types",
}

var gatewayProviderTypesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List provider types known to the gateway runtime",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := newGatewayClient().ListProviderTypes(context.Background())
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(items)
		}
		printGatewayProviderTypesTable(items)
		return nil
	},
}

var gatewayProviderTypesEnableCmd = &cobra.Command{
	Use:   "enable <provider-type>",
	Short: "Enable one gateway provider type",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().EnableProviderType(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayProviderTypesDisableCmd = &cobra.Command{
	Use:   "disable <provider-type>",
	Short: "Disable one gateway provider type",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().DisableProviderType(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

// ── gateway llm api handler types ────────────────────────────────────────────

var gatewayLLMAPIHandlerTypesCmd = &cobra.Command{
	Use:   "llm-api-handler-types",
	Short: "Manage gateway LLM API handler types",
}

var gatewayLLMAPIHandlerTypesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List LLM API handler types known to the gateway runtime",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := newGatewayClient().ListLLMAPIHandlerTypes(context.Background())
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(items)
		}
		printGatewayLLMAPIHandlerTypesTable(items)
		return nil
	},
}

var gatewayLLMAPIHandlerTypesEnableCmd = &cobra.Command{
	Use:   "enable <handler-type>",
	Short: "Enable one gateway LLM API handler type",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().EnableLLMAPIHandlerType(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayLLMAPIHandlerTypesDisableCmd = &cobra.Command{
	Use:   "disable <handler-type>",
	Short: "Disable one gateway LLM API handler type",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().DisableLLMAPIHandlerType(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayModelsDiscoveredCmd = &cobra.Command{
	Use:   "discovered",
	Short: "Manage discovered provider models",
}

var gatewayModelsDiscoveredListCmd = &cobra.Command{
	Use:   "list <provider-id>",
	Short: "List discovered models for one provider",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := newGatewayClient().ListDiscoveredModels(context.Background(), args[0])
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(items)
		}
		printGatewayDiscoveredModelsTable(items)
		return nil
	},
}

var gatewayModelsDiscoveredRefreshCmd = &cobra.Command{
	Use:   "refresh <provider-id>",
	Short: "Refresh discovered models for one provider",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().RefreshProviderModels(context.Background(), args[0])
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(resp)
		}
		printGatewayDiscoveredModelsTable(resp.Items)
		return nil
	},
}

var gatewayModelsManagedCmd = &cobra.Command{
	Use:   "managed",
	Short: "Manage managed model overlays",
}

var gatewayModelsManagedListCmd = &cobra.Command{
	Use:   "list",
	Short: "List managed models",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := newGatewayClient().ListManagedModels(context.Background(), adminclient.ManagedModelListOptions{
			ProviderID: gatewayManagedModelProviderID,
		})
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(items)
		}
		printGatewayManagedModelsTable(items)
		return nil
	},
}

var gatewayModelsManagedGetCmd = &cobra.Command{
	Use:   "get <provider-id> <upstream-model>",
	Short: "Get one managed model overlay",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().GetManagedModel(context.Background(), args[0], args[1])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayModelsManagedDeleteCmd = &cobra.Command{
	Use:   "delete <provider-id> <upstream-model>",
	Short: "Delete a managed model overlay",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().DeleteManagedModel(context.Background(), args[0], args[1])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

// ── gateway cliauth ──────────────────────────────────────────────────────────

var gatewayCLIAuthCmd = &cobra.Command{
	Use:   "cliauth",
	Short: "Manage remote gateway CLI auth runtime via the admin API",
}

var gatewayCLIAuthAuthenticatorsCmd = &cobra.Command{
	Use:   "authenticators",
	Short: "Manage gateway CLI auth authenticators",
}

var gatewayCLIAuthAuthenticatorsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List enabled and known CLI auth authenticators",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := newGatewayClient().ListCLIAuthAuthenticators(context.Background())
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(items)
		}
		printGatewayCLIAuthAuthenticatorsTable(items)
		return nil
	},
}

var gatewayCLIAuthAuthenticatorsGetCmd = &cobra.Command{
	Use:   "get <authenticator-name>",
	Short: "Get one CLI auth authenticator",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().GetCLIAuthAuthenticator(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayCLIAuthAuthenticatorsUpdateCmd = &cobra.Command{
	Use:   "update <authenticator-name>",
	Short: "Update a CLI auth authenticator config",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		req, err := buildGatewayCLIAuthAuthenticatorUpdateRequest()
		if err != nil {
			return err
		}
		resp, err := newGatewayClient().UpdateCLIAuthAuthenticator(context.Background(), args[0], req)
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayCLIAuthLoginCmd = &cobra.Command{
	Use:   "login <authenticator-name>",
	Short: "Start a gateway-side CLI auth login flow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().StartCLIAuthLogin(context.Background(), args[0])
		if err != nil {
			return err
		}
		if !gatewayCLIAuthLoginWait {
			return printJSON(resp)
		}
		return waitForGatewayCLIAuthLogin(args[0], resp.LoginID)
	},
}

var gatewayCLIAuthLoginsCmd = &cobra.Command{
	Use:   "logins",
	Short: "Inspect gateway CLI auth login sessions",
}

var gatewayCLIAuthLoginsGetCmd = &cobra.Command{
	Use:   "get <login-id>",
	Short: "Get one gateway CLI auth login session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().GetCLIAuthLoginStatus(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayCLIAuthRefresherCmd = &cobra.Command{
	Use:   "refresher",
	Short: "Manage the gateway CLI auth refresher",
}

var gatewayCLIAuthRefresherStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show CLI auth refresher status",
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().GetCLIAuthRefresherStatus(context.Background())
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayCLIAuthRefresherEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable the CLI auth refresher",
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().EnableCLIAuthRefresher(context.Background())
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayCLIAuthRefresherDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable the CLI auth refresher",
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().DisableCLIAuthRefresher(context.Background())
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayCredentialListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := newGatewayClient().ListCredentials(context.Background(), adminclient.CredentialListOptions{})
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(items)
		}
		printGatewayCredentialsTable(items)
		return nil
	},
}

var gatewayCredentialGetCmd = &cobra.Command{
	Use:   "get <credential-id>",
	Short: "Get one credential",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().GetCredential(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayCredentialDeleteCmd = &cobra.Command{
	Use:   "delete <credential-id>",
	Short: "Delete a credential",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().DeleteCredential(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

func newGatewayClient() *adminclient.Client {
	return adminclient.New(adminclient.Config{
		BaseURL:  globalGatewayAddr,
		Username: gwUser,
		Password: gwPassword,
	})
}

func readJSONFile(path string, dest any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func buildGatewayCLIAuthAuthenticatorUpdateRequest() (adminclient.UpdateCLIAuthAuthenticatorRequest, error) {
	if gatewayCLIAuthAuthenticatorConfigFile != "" {
		var req adminclient.UpdateCLIAuthAuthenticatorRequest
		if err := readJSONFile(gatewayCLIAuthAuthenticatorConfigFile, &req); err != nil {
			return adminclient.UpdateCLIAuthAuthenticatorRequest{}, err
		}
		if req.Enabled == nil && req.Config == nil {
			return adminclient.UpdateCLIAuthAuthenticatorRequest{}, fmt.Errorf("enabled or config is required in %s", gatewayCLIAuthAuthenticatorConfigFile)
		}
		return req, nil
	}

	state := gatewayCLIAuthAuthenticatorState
	switch state {
	case "", "enabled":
		enabled := true
		cfg := cliauth.AuthenticatorConfig{
			CallbackPort: gatewayCLIAuthCallbackPort,
			NoBrowser:    gatewayCLIAuthNoBrowser,
			DeviceFlow:   gatewayCLIAuthDeviceFlow,
		}
		return adminclient.UpdateCLIAuthAuthenticatorRequest{
			Enabled: &enabled,
			Config:  &cfg,
		}, nil
	case "disabled":
		enabled := false
		return adminclient.UpdateCLIAuthAuthenticatorRequest{
			Enabled: &enabled,
		}, nil
	default:
		return adminclient.UpdateCLIAuthAuthenticatorRequest{}, fmt.Errorf("invalid --state %q: want enabled or disabled", state)
	}
}

func waitForGatewayCLIAuthLogin(_ string, loginID string) error {
	if loginID == "" {
		return fmt.Errorf("login started but no login_id was returned")
	}
	client := newGatewayClient()
	interval := gatewayCLIAuthPollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		status, err := client.GetCLIAuthLoginStatus(context.Background(), loginID)
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			if status.Status == "succeeded" || status.Status == "failed" {
				return printJSON(status)
			}
		} else {
			if status.Phase != "" || status.Message != "" || status.VerificationURL != "" || status.UserCode != "" {
				fmt.Fprintf(os.Stderr, "[%s] %s", dash(status.Phase), status.Message)
				if status.VerificationURL != "" {
					fmt.Fprintf(os.Stderr, " %s", status.VerificationURL)
				}
				if status.UserCode != "" {
					fmt.Fprintf(os.Stderr, " code=%s", status.UserCode)
				}
				fmt.Fprintln(os.Stderr)
			}
		}
		switch status.Status {
		case "succeeded":
			return printJSON(status)
		case "failed":
			return printJSON(status)
		}
		time.Sleep(interval)
	}
}

func init() {
	gatewayCmd.PersistentFlags().StringVar(&globalGatewayAddr, "addr", envOr("GATEWAY_ADDR", "http://localhost:8019"), "caddy-agent-gateway admin API address")
	gatewayCmd.PersistentFlags().StringVar(&globalGatewayAddr, "admin-addr", envOr("GATEWAY_ADDR", "http://localhost:8019"), "deprecated alias for --addr")
	gatewayCmd.PersistentFlags().StringVar(&globalGatewayAddr, "gateway-addr", envOr("GATEWAY_ADDR", "http://localhost:8019"), "deprecated alias for --addr")
	_ = gatewayCmd.PersistentFlags().MarkDeprecated("admin-addr", "use --addr instead")
	_ = gatewayCmd.PersistentFlags().MarkDeprecated("gateway-addr", "use --addr instead")
	_ = gatewayCmd.PersistentFlags().MarkHidden("admin-addr")
	_ = gatewayCmd.PersistentFlags().MarkHidden("gateway-addr")
	gatewayCmd.PersistentFlags().StringVar(&gwUser, "user", envOr("GATEWAY_ADMIN_USER", ""), "gateway admin username")
	gatewayCmd.PersistentFlags().StringVar(&gwPassword, "password", envOr("GATEWAY_ADMIN_PASSWORD", ""), "gateway admin password")

	gatewayModelsManagedListCmd.Flags().StringVar(&gatewayManagedModelProviderID, "provider-id", "", "filter managed models by provider ID")
	gatewayValidateCmd.Flags().StringVarP(&gatewayBundleFile, "file", "f", "", "path to gateway bundle YAML file")
	gatewayApplyCmd.Flags().StringVarP(&gatewayBundleFile, "file", "f", "", "path to gateway bundle YAML file")
	gatewayExportCmd.Flags().StringVarP(&gatewayExportFile, "file", "f", "", "write bundle YAML to this file instead of stdout")
	gatewayCLIAuthAuthenticatorsUpdateCmd.Flags().StringVarP(&gatewayCLIAuthAuthenticatorConfigFile, "file", "f", "", "path to CLI auth authenticator JSON file containing {\"config\":...}")
	gatewayCLIAuthAuthenticatorsUpdateCmd.Flags().StringVar(&gatewayCLIAuthAuthenticatorState, "state", "enabled", "desired authenticator state: enabled or disabled")
	gatewayCLIAuthAuthenticatorsUpdateCmd.Flags().IntVar(&gatewayCLIAuthCallbackPort, "callback-port", 0, "callback port override")
	gatewayCLIAuthAuthenticatorsUpdateCmd.Flags().BoolVar(&gatewayCLIAuthNoBrowser, "no-browser", false, "print the login URL instead of opening a browser")
	gatewayCLIAuthAuthenticatorsUpdateCmd.Flags().BoolVar(&gatewayCLIAuthDeviceFlow, "device-flow", false, "use device flow when supported")
	gatewayCLIAuthLoginCmd.Flags().BoolVar(&gatewayCLIAuthLoginWait, "wait", false, "poll login status until the flow succeeds or fails")
	gatewayCLIAuthLoginCmd.Flags().DurationVar(&gatewayCLIAuthPollInterval, "poll-interval", 2*time.Second, "poll interval used with --wait")

	gatewayProviderCmd.AddCommand(
		gatewayProviderListCmd,
		gatewayProviderGetCmd,
		gatewayProviderDeleteCmd,
		gatewayProviderEnableCmd,
		gatewayProviderDisableCmd,
	)
	gatewayRouteCmd.AddCommand(
		gatewayRouteListCmd,
		gatewayRouteGetCmd,
		gatewayRouteDeleteCmd,
		gatewayRouteEnableCmd,
		gatewayRouteDisableCmd,
	)
	gatewayVirtualKeyCmd.AddCommand(
		gatewayVirtualKeyListCmd,
		gatewayVirtualKeyGetCmd,
		gatewayVirtualKeyDeleteCmd,
		gatewayVirtualKeyEnableCmd,
		gatewayVirtualKeyDisableCmd,
	)
	gatewayCredentialCmd.AddCommand(
		gatewayCredentialListCmd,
		gatewayCredentialGetCmd,
		gatewayCredentialDeleteCmd,
	)
	gatewayModelsDiscoveredCmd.AddCommand(
		gatewayModelsDiscoveredListCmd,
		gatewayModelsDiscoveredRefreshCmd,
	)
	gatewayModelsManagedCmd.AddCommand(
		gatewayModelsManagedListCmd,
		gatewayModelsManagedGetCmd,
		gatewayModelsManagedDeleteCmd,
	)
	gatewayModelsCmd.AddCommand(gatewayModelsDiscoveredCmd, gatewayModelsManagedCmd)
	gatewayProviderTypesCmd.AddCommand(
		gatewayProviderTypesListCmd,
		gatewayProviderTypesEnableCmd,
		gatewayProviderTypesDisableCmd,
	)
	gatewayLLMAPIHandlerTypesCmd.AddCommand(
		gatewayLLMAPIHandlerTypesListCmd,
		gatewayLLMAPIHandlerTypesEnableCmd,
		gatewayLLMAPIHandlerTypesDisableCmd,
	)
	gatewayCLIAuthAuthenticatorsCmd.AddCommand(
		gatewayCLIAuthAuthenticatorsListCmd,
		gatewayCLIAuthAuthenticatorsGetCmd,
		gatewayCLIAuthAuthenticatorsUpdateCmd,
	)
	gatewayCLIAuthLoginsCmd.AddCommand(gatewayCLIAuthLoginsGetCmd)
	gatewayCLIAuthRefresherCmd.AddCommand(
		gatewayCLIAuthRefresherStatusCmd,
		gatewayCLIAuthRefresherEnableCmd,
		gatewayCLIAuthRefresherDisableCmd,
	)
	gatewayCLIAuthCmd.AddCommand(gatewayCLIAuthAuthenticatorsCmd, gatewayCLIAuthLoginCmd, gatewayCLIAuthLoginsCmd, gatewayCLIAuthRefresherCmd)

	gatewayCmd.AddCommand(
		gatewayValidateCmd,
		gatewayApplyCmd,
		gatewayExportCmd,
		gatewayProviderCmd,
		gatewayRouteCmd,
		gatewayVirtualKeyCmd,
		gatewayCredentialCmd,
		gatewayProviderTypesCmd,
		gatewayLLMAPIHandlerTypesCmd,
		gatewayModelsCmd,
		gatewayCLIAuthCmd,
	)
	rootCmd.AddCommand(gatewayCmd)
}
