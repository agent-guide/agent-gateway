package main

import (
	"github.com/agent-guide/caddy-agent-gateway/internal/agwctl/gatewayadmin"
	"github.com/spf13/cobra"
)

var (
	gwUser     string
	gwPassword string
)

// ── gateway ───────────────────────────────────────────────────────────────────

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Manage the caddy-agent-gateway via its admin API",
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
		items, raw, err := newGatewayClient().List("/admin/providers")
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(raw)
		}
		printGatewayProvidersTable(items)
		return nil
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
		items, raw, err := newGatewayClient().List("/admin/routes")
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(raw)
		}
		printGatewayRoutesTable(items)
		return nil
	},
}

// ── gateway virtual-key ───────────────────────────────────────────────────────

var gatewayVirtualKeyCmd = &cobra.Command{
	Use:   "virtual-key",
	Short: "Manage gateway virtual keys",
}

var gatewayVirtualKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all virtual keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, raw, err := newGatewayClient().List("/admin/virtual_keys")
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(raw)
		}
		printGatewayVirtualKeysTable(items)
		return nil
	},
}

// ── gateway credential ────────────────────────────────────────────────────────

var gatewayCredentialCmd = &cobra.Command{
	Use:   "credential",
	Short: "Manage gateway credentials",
}

var gatewayCredentialListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, raw, err := newGatewayClient().List("/admin/credentials")
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(raw)
		}
		printGatewayCredentialsTable(items)
		return nil
	},
}

func newGatewayClient() *gatewayadmin.Client {
	return gatewayadmin.NewClient(globalGatewayAddr, gwUser, gwPassword)
}

func init() {
	gatewayCmd.PersistentFlags().StringVar(&gwUser, "user", envOr("GATEWAY_ADMIN_USER", ""), "gateway admin username")
	gatewayCmd.PersistentFlags().StringVar(&gwPassword, "password", envOr("GATEWAY_ADMIN_PASSWORD", ""), "gateway admin password")

	gatewayProviderCmd.AddCommand(gatewayProviderListCmd)
	gatewayRouteCmd.AddCommand(gatewayRouteListCmd)
	gatewayVirtualKeyCmd.AddCommand(gatewayVirtualKeyListCmd)
	gatewayCredentialCmd.AddCommand(gatewayCredentialListCmd)

	gatewayCmd.AddCommand(gatewayProviderCmd, gatewayRouteCmd, gatewayVirtualKeyCmd, gatewayCredentialCmd)
	rootCmd.AddCommand(gatewayCmd)
}
