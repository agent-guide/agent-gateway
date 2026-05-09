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
	Short: "Agent Gateway CLI — manage the gateway, Caddy, and local CLI auth state",
}

func init() {
	initOutputFlag()
}
