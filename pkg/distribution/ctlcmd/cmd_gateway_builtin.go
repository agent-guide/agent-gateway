package ctlcmd

import (
	"github.com/spf13/cobra"
)

func (s *commandState) initBuiltin() {
	s.gatewayBuiltinRuntimeCmd = &cobra.Command{Use: "builtin-runtime", Short: "Inspect the builtin ADK host runtime"}
	s.gatewayBuiltinRuntimeGetCmd = &cobra.Command{
		Use: "get", Short: "Get builtin materialization and runtime diagnostics",
		RunE: func(cmd *cobra.Command, args []string) error {
			view, err := s.newGatewayClient().GetBuiltinRuntime(cmd.Context())
			if err != nil {
				return err
			}
			return s.printJSON(view)
		},
	}
	s.gatewayBuiltinRuntimeInFlightCmd = &cobra.Command{
		Use: "inflight", Short: "List in-flight builtin turns",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := s.newGatewayClient().ListBuiltinInFlight(cmd.Context())
			if err != nil {
				return err
			}
			return s.printJSON(items)
		},
	}
	s.gatewayBuiltinRuntimeCmd.AddCommand(s.gatewayBuiltinRuntimeGetCmd, s.gatewayBuiltinRuntimeInFlightCmd)
	s.rootCmd.AddCommand(s.gatewayBuiltinRuntimeCmd)
}
