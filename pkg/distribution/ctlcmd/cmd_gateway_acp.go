package ctlcmd

import (
	"github.com/spf13/cobra"
)

func (s *commandState) initACP() {
	s.gatewayACPRuntimeCmd = &cobra.Command{Use: "acp-runtime", Short: "Inspect gateway ACP runtime state"}
	s.gatewayACPRuntimeGetCmd = &cobra.Command{
		Use: "get", Short: "Get the ACP runtime overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := s.newGatewayClient().GetACPRuntime(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(item)
			}
			s.printGatewayACPRuntimeOverview(item)
			return nil
		},
	}
	s.gatewayACPRuntimeInFlightCmd = &cobra.Command{
		Use: "inflight", Short: "List in-flight ACP turns",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListACPRuntimeInFlight(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(items)
			}
			s.printGatewayACPInFlightTable(items)
			return nil
		},
	}
	s.gatewayACPRuntimeCloseThreadCmd = &cobra.Command{
		Use: "close-thread <agent-id> <thread-id>", Short: "Close pooled instances for one ACP Agent thread", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := s.newGatewayClient().CloseACPThread(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return s.printJSON(resp)
		},
	}
	s.gatewayACPRuntimeCmd.AddCommand(s.gatewayACPRuntimeGetCmd, s.gatewayACPRuntimeInFlightCmd, s.gatewayACPRuntimeCloseThreadCmd)
	s.rootCmd.AddCommand(s.gatewayACPRuntimeCmd)
}
