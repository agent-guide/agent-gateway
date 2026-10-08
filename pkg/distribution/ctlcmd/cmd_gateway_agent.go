package ctlcmd

import (
	"context"
	"encoding/json"
	"fmt"

	agentruntime "github.com/agent-guide/agent-gateway/pkg/agent/runtime"

	"github.com/spf13/cobra"
)

func (s *commandState) rawAgentReadCommand(use, short string, read func(context.Context, string) (json.RawMessage, error)) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := read(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return s.printJSON(resp)
	}}
}
func (s *commandState) initAgent() {
	s.gatewayAgentCmd = &cobra.Command{
		Use:   "agent",
		Short: "Manage gateway agents",
	}
	s.gatewayAgentListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListAgents(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayAgentsTable(items)
			return nil
		},
	}
	s.gatewayAgentGetCmd = &cobra.Command{
		Use:   "get <agent-id>",
		Short: "Get one agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetAgent(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(item)
		},
	}
	s.gatewayAgentDeleteCmd = &cobra.Command{
		Use:   "delete <agent-id>",
		Short: "Delete an agent after its AgentRoutes are removed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().DeleteAgent(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayAgentWorkspaceCmd = &cobra.Command{
		Use:   "workspace <agent-id>",
		Short: "Get the aggregated workspace summary for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().GetAgentWorkspace(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayAgentActivityCmd = &cobra.Command{
		Use:   "activity <agent-id>",
		Short: "Get recent activity for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().GetAgentActivity(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayAgentUsageCmd = &cobra.Command{
		Use:   "usage <agent-id>",
		Short: "Get usage summary for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().GetAgentUsage(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayAgentInteractionsCmd = &cobra.Command{
		Use:   "interactions <agent-id>",
		Short: "Get interaction events for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().GetAgentInteractions(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayAgentResourcesCmd = &cobra.Command{
		Use:   "resources <agent-id>",
		Short: "Get linked resources for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().GetAgentResources(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayAgentHealthCmd = &cobra.Command{
		Use:   "health <agent-id>",
		Short: "Get shallow health for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().GetAgentHealth(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayAgentCapabilitiesCmd = s.rawAgentReadCommand("capabilities <agent-id>", "Get runtime capabilities for an agent", func(ctx context.Context, id string) (json.RawMessage, error) {
		return s.newGatewayClient().GetAgentCapabilities(ctx, id)
	})
	s.gatewayAgentRunsCmd = s.rawAgentReadCommand("runs <agent-id>", "List active and retained runs for an agent", func(ctx context.Context, id string) (json.RawMessage, error) {
		return s.newGatewayClient().ListAgentRuns(ctx, id)
	})
	s.gatewayAgentPermissionsCmd = s.rawAgentReadCommand("permissions <agent-id>", "List pending permissions for an agent", func(ctx context.Context, id string) (json.RawMessage, error) {
		return s.newGatewayClient().ListAgentPermissions(ctx, id)
	})
	s.gatewayAgentCancelCmd = &cobra.Command{Use: "cancel <agent-id> <run-id>", Short: "Cancel one exact agent run", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := s.newGatewayClient().CancelAgentRun(cmd.Context(), args[0], args[1], agentruntime.CancelMode(s.agentCancelMode))
		if err != nil {
			return err
		}
		return s.printJSON(resp)
	}}
	s.gatewayAgentDecideCmd = &cobra.Command{Use: "decide <agent-id> <request-id>", Short: "Resolve one pending agent permission", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		decision := agentruntime.PermissionDecision{RequestID: args[1], Outcome: s.agentPermissionOutcome, OptionID: s.agentPermissionOptionID}
		if s.agentPermissionDecisions != "" {
			if err := json.Unmarshal([]byte(s.agentPermissionDecisions), &decision.Decisions); err != nil {
				return fmt.Errorf("decode --decisions: %w", err)
			}
		}
		resp, err := s.newGatewayClient().ResolveAgentPermission(cmd.Context(), args[0], args[1], decision)
		if err != nil {
			return err
		}
		return s.printJSON(resp)
	}}
	s.gatewayAgentSessionsCmd = &cobra.Command{Use: "sessions <agent-id>", Short: "List sessions exposed by an agent runtime", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := s.newGatewayClient().ListAgentSessions(cmd.Context(), args[0], s.agentSessionCWD, s.agentSessionCursor)
		if err != nil {
			return err
		}
		return s.printJSON(resp)
	}}
	s.gatewayAgentTranscriptCmd = &cobra.Command{Use: "transcript <agent-id> <session-id>", Short: "Load a session transcript exposed by an agent runtime", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := s.newGatewayClient().GetAgentTranscript(cmd.Context(), args[0], args[1], s.agentSessionCWD)
		if err != nil {
			return err
		}
		return s.printJSON(resp)
	}}
	s.gatewayAgentCancelCmd.Flags().StringVar(&s.agentCancelMode, "mode", "force", "cancel mode: force or graceful")
	s.gatewayAgentDecideCmd.Flags().StringVar(&s.agentPermissionOutcome, "outcome", "", "runtime-native common outcome")
	s.gatewayAgentDecideCmd.Flags().StringVar(&s.agentPermissionOptionID, "option-id", "", "ACP option id")
	s.gatewayAgentDecideCmd.Flags().StringVar(&s.agentPermissionDecisions, "decisions", "", "builtin action decisions as JSON array")
	s.gatewayAgentSessionsCmd.Flags().StringVar(&s.agentSessionCWD, "cwd", "", "session cwd filter")
	s.gatewayAgentSessionsCmd.Flags().StringVar(&s.agentSessionCursor, "cursor", "", "session page cursor")
	s.gatewayAgentTranscriptCmd.Flags().StringVar(&s.agentSessionCWD, "cwd", "", "session cwd")
	s.gatewayAgentCmd.AddCommand(s.gatewayAgentListCmd, s.gatewayAgentGetCmd, s.gatewayAgentDeleteCmd, s.gatewayAgentWorkspaceCmd, s.gatewayAgentActivityCmd, s.gatewayAgentUsageCmd, s.gatewayAgentInteractionsCmd, s.gatewayAgentResourcesCmd, s.gatewayAgentHealthCmd, s.gatewayAgentCapabilitiesCmd, s.gatewayAgentRunsCmd, s.gatewayAgentCancelCmd, s.gatewayAgentPermissionsCmd, s.gatewayAgentDecideCmd, s.gatewayAgentSessionsCmd, s.gatewayAgentTranscriptCmd)
	s.rootCmd.AddCommand(s.gatewayAgentCmd)
}
