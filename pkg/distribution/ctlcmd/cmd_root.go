package ctlcmd

import (
	"github.com/spf13/cobra"
)

func (s *commandState) initRoot() {
	s.rootCmd = &cobra.Command{
		Use:   "agwctl",
		Short: "Manage Agent Gateway control-plane resources",
	}
	s.initOutputFlag()
	s.rootCmd.PersistentFlags().StringVar(&s.globalGatewayAddr, "admin-addr", envOr("AGW_ADMIN_ADDR", "http://localhost:8019"), "agent-gateway admin API address")
	s.rootCmd.PersistentFlags().StringVar(&s.gwAdminBasicAuth, "admin-basic-auth", envOr("AGW_ADMIN_BASIC_AUTH", ""), "gateway admin Basic Auth request credentials as username:password")
	s.rootCmd.PersistentFlags().StringArrayVar(&s.gwAdminHeaders, "admin-header", nil, "extra admin API header as 'Name: value'; repeat to send multiple headers")
}
