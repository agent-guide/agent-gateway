package main

import (
	"github.com/spf13/cobra"
)

var (
	globalCaddyAdmin  string
	globalGatewayAddr string
)

var rootCmd = &cobra.Command{
	Use:   "agwctl",
	Short: "Agent Gateway CLI — manage caddy-agent-gateway and Caddy",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&globalCaddyAdmin, "caddy-admin", envOr("CADDY_ADMIN_ADDR", "http://localhost:2019"), "Caddy admin API address")
	rootCmd.PersistentFlags().StringVar(&globalGatewayAddr, "gateway-addr", envOr("GATEWAY_ADDR", "http://localhost:8019"), "caddy-agent-gateway admin API address")
	initOutputFlag()
}
