package ctlcmd

import "github.com/spf13/cobra"

func (s *commandState) logicalModelsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "logical", Short: "Manage logical models (create and update through apply)"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List logical models", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		items, err := s.newGatewayClient().ListLogicalModels(cmd.Context())
		if err != nil {
			return err
		}
		return s.printJSON(items)
	}})
	cmd.AddCommand(&cobra.Command{Use: "get <id>", Short: "Get a logical model", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		item, err := s.newGatewayClient().GetLogicalModel(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return s.printJSON(item)
	}})
	cmd.AddCommand(&cobra.Command{Use: "delete <id>", Short: "Delete an unreferenced logical model", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := s.newGatewayClient().DeleteLogicalModel(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return s.printJSON(resp)
	}})
	return cmd
}
