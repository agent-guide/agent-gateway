package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/agent-guide/caddy-agent-gateway/internal/agwctl/gatewayadmin"
	agwserver "github.com/agent-guide/caddy-agent-gateway/internal/agwctl/server"
	"github.com/spf13/cobra"
)

var (
	serveAddr              string
	serveAdminUser         string
	serveAdminPasswordHash string
	serveReadonlyIDs       string
	serveGatewayUser       string
	serveGatewayPassword   string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the agwctl HTTP management server",
	RunE: func(cmd *cobra.Command, args []string) error {
		if serveAdminUser == "" {
			fmt.Fprintln(os.Stderr, "warning: --admin-user not set; all authenticated endpoints will return 401")
		}

		var gw *gatewayadmin.Proxy
		if serveGatewayUser != "" {
			gw = gatewayadmin.NewProxy(globalGatewayAddr, serveGatewayUser, serveGatewayPassword)
			log.Printf("gateway proxy enabled: %s (user: %s)", globalGatewayAddr, serveGatewayUser)
		} else {
			fmt.Fprintln(os.Stderr, "warning: --gateway-admin-user not set; /admin/* proxy to gateway is disabled")
		}

		srv := agwserver.New(globalCaddyAdmin, serveAdminUser, serveAdminPasswordHash, splitCSV(serveReadonlyIDs), gw)
		log.Printf("agwctl listening on %s", serveAddr)
		return http.ListenAndServe(serveAddr, srv)
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", envOr("CADDYMGR_ADDR", ":8090"), "listen address")
	serveCmd.Flags().StringVar(&serveAdminUser, "admin-user", os.Getenv("CADDYMGR_ADMIN_USER"), "admin username for this server")
	serveCmd.Flags().StringVar(&serveAdminPasswordHash, "admin-password-hash", os.Getenv("CADDYMGR_ADMIN_PASSWORD_HASH"), "bcrypt hash of admin password")
	serveCmd.Flags().StringVar(&serveReadonlyIDs, "readonly-server-ids", os.Getenv("CADDYMGR_READONLY_SERVER_IDS"), "comma-separated Caddy server IDs that are read-only")
	serveCmd.Flags().StringVar(&serveGatewayUser, "gateway-admin-user", os.Getenv("GATEWAY_ADMIN_USER"), "gateway admin username (for proxy auth)")
	serveCmd.Flags().StringVar(&serveGatewayPassword, "gateway-admin-password", os.Getenv("GATEWAY_ADMIN_PASSWORD"), "gateway admin password (for proxy auth)")

	rootCmd.AddCommand(serveCmd)
}
