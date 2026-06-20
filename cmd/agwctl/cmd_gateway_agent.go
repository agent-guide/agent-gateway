package main

import (
	"context"

	"github.com/spf13/cobra"
)

// ── gateway agent ────────────────────────────────────────────────────────────
//
// Reads and lifecycle deletes only. Agents are created/updated through
// `agwctl gateway apply` like every other gateway-bundle object.

var gatewayAgentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage gateway agents",
}

var gatewayAgentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all agents",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := newGatewayClient().ListAgents(context.Background())
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return printJSON(items)
		}
		printGatewayAgentsTable(items)
		return nil
	},
}

var gatewayAgentGetCmd = &cobra.Command{
	Use:   "get <agent-id>",
	Short: "Get one agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		item, err := newGatewayClient().GetAgent(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(item)
	},
}

var gatewayAgentDeleteCmd = &cobra.Command{
	Use:   "delete <agent-id>",
	Short: "Delete an agent (does not delete its backing ACP service or routes)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().DeleteAgent(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayAgentWorkspaceCmd = &cobra.Command{
	Use:   "workspace <agent-id>",
	Short: "Get the aggregated workspace summary for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().GetAgentWorkspace(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayAgentActivityCmd = &cobra.Command{
	Use:   "activity <agent-id>",
	Short: "Get recent activity for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().GetAgentActivity(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayAgentUsageCmd = &cobra.Command{
	Use:   "usage <agent-id>",
	Short: "Get usage summary for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().GetAgentUsage(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayAgentInteractionsCmd = &cobra.Command{
	Use:   "interactions <agent-id>",
	Short: "Get interaction events for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().GetAgentInteractions(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayAgentResourcesCmd = &cobra.Command{
	Use:   "resources <agent-id>",
	Short: "Get linked resources for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().GetAgentResources(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

var gatewayAgentHealthCmd = &cobra.Command{
	Use:   "health <agent-id>",
	Short: "Get shallow health for an agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newGatewayClient().GetAgentHealth(context.Background(), args[0])
		if err != nil {
			return err
		}
		return printJSON(resp)
	},
}

func init() {
	gatewayAgentCmd.AddCommand(
		gatewayAgentListCmd,
		gatewayAgentGetCmd,
		gatewayAgentDeleteCmd,
		gatewayAgentWorkspaceCmd,
		gatewayAgentActivityCmd,
		gatewayAgentUsageCmd,
		gatewayAgentInteractionsCmd,
		gatewayAgentResourcesCmd,
		gatewayAgentHealthCmd,
	)
	gatewayCmd.AddCommand(gatewayAgentCmd)
}
