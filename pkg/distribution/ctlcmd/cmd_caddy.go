package ctlcmd

import (
	"fmt"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/agwctl/caddyadminclient"
	"github.com/spf13/cobra"
)

func parseHandlers(specs []string) ([]caddyadminclient.HandlerConf, error) {
	handlers := make([]caddyadminclient.HandlerConf, 0, len(specs))
	for _, spec := range specs {
		htype, params, _ := strings.Cut(spec, ":")
		conf := caddyadminclient.HandlerConf{Type: htype}
		if params != "" {
			for _, kv := range strings.Split(params, ",") {
				k, v, _ := strings.Cut(kv, "=")
				switch k {
				case "apis":
					conf.APIs = strings.Split(v, ";")
				case "upstream":
					conf.Upstream = v
				case "root":
					conf.Root = v
				default:
					return nil, fmt.Errorf("unknown handler param %q in %q", k, spec)
				}
			}
		}
		handlers = append(handlers, conf)
	}
	return handlers, nil
}
func (s *commandState) initCaddy() {
	s.caddyCmd = &cobra.Command{
		Use:   "caddy",
		Short: "Manage Caddy servers and routes via the Caddy admin API",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("admin-addr") {
				return fmt.Errorf("--admin-addr configures the Agent Gateway admin API; use --addr for the Caddy admin API")
			}
			return nil
		},
	}
	s.caddyServerCmd = &cobra.Command{
		Use:   "server",
		Short: "Manage Caddy HTTP servers",
	}
	s.caddyServerListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all Caddy HTTP servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := caddyadminclient.NewManager(s.globalCaddyAdmin)
			servers, err := mgr.ListServers(cmd.Context())
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(servers)
			}
			s.printCaddyServersTable(servers)
			return nil
		},
	}
	s.caddyServerGetCmd = &cobra.Command{
		Use:   "get <server-id>",
		Short: "Get a Caddy HTTP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := caddyadminclient.NewManager(s.globalCaddyAdmin)
			srv, err := mgr.GetServer(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(srv)
			}
			s.printCaddyServersTable([]*caddyadminclient.ServerResponse{srv})
			return nil
		},
	}
	s.caddyServerCreateCmd = &cobra.Command{
		Use:   "create",
		Short: "Create a new Caddy HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if s.serverCreateID == "" {
				return fmt.Errorf("--id is required")
			}
			if len(s.serverCreateListen) == 0 {
				return fmt.Errorf("--listen is required")
			}
			mgr := caddyadminclient.NewManager(s.globalCaddyAdmin)
			req := &caddyadminclient.ServerRequest{
				ID:     s.serverCreateID,
				Listen: s.serverCreateListen,
			}
			if s.serverCreateTLSAuto {
				req.TLS = &caddyadminclient.TLSConf{Auto: true}
			}
			if err := mgr.CreateServer(cmd.Context(), req); err != nil {
				return err
			}
			srv, err := mgr.GetServer(cmd.Context(), s.serverCreateID)
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(srv)
			}
			s.printCaddyServersTable([]*caddyadminclient.ServerResponse{srv})
			return nil
		},
	}
	s.caddyServerUpdateCmd = &cobra.Command{
		Use:   "update <server-id>",
		Short: "Update an existing Caddy HTTP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(s.serverUpdateListen) == 0 {
				return fmt.Errorf("--listen is required")
			}
			mgr := caddyadminclient.NewManager(s.globalCaddyAdmin)
			req := &caddyadminclient.ServerRequest{
				ID:     args[0],
				Listen: s.serverUpdateListen,
			}
			if s.serverUpdateTLSAuto {
				req.TLS = &caddyadminclient.TLSConf{Auto: true}
			}
			if err := mgr.UpdateServer(cmd.Context(), req); err != nil {
				return err
			}
			srv, err := mgr.GetServer(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(srv)
			}
			s.printCaddyServersTable([]*caddyadminclient.ServerResponse{srv})
			return nil
		},
	}
	s.caddyServerDeleteCmd = &cobra.Command{
		Use:   "delete <server-id>",
		Short: "Delete a Caddy HTTP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := caddyadminclient.NewManager(s.globalCaddyAdmin)
			if err := mgr.DeleteServer(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(s.rootCmd.OutOrStdout(), "deleted server %q\n", args[0])
			return nil
		},
	}
	s.caddyRouteCmd = &cobra.Command{
		Use:   "route",
		Short: "Manage Caddy HTTP routes within a server",
	}
	s.caddyRouteListCmd = &cobra.Command{
		Use:   "list <server-id>",
		Short: "List routes in a Caddy server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := caddyadminclient.NewManager(s.globalCaddyAdmin)
			routes, err := mgr.ListRoutes(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if s.outputFormat == "json" {
				return s.printJSON(routes)
			}
			s.printCaddyRoutesTable(routes)
			return nil
		},
	}
	s.caddyRouteAddCmd = &cobra.Command{
		Use:   "add <server-id>",
		Short: "Add a route to a Caddy server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if s.routeAddID == "" {
				return fmt.Errorf("--id is required")
			}
			handlers, err := parseHandlers(s.routeAddHandlers)
			if err != nil {
				return err
			}
			mgr := caddyadminclient.NewManager(s.globalCaddyAdmin)
			req := &caddyadminclient.RouteRequest{
				ID:       s.routeAddID,
				Order:    s.routeAddOrder,
				Match:    caddyadminclient.MatchConf{Paths: s.routeAddPaths, Hosts: s.routeAddHosts},
				Handlers: handlers,
			}
			if err := mgr.AddRoute(cmd.Context(), args[0], req); err != nil {
				return err
			}
			routes, err := mgr.ListRoutes(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, r := range routes {
				if r.ID == s.routeAddID {
					if s.outputFormat == "json" {
						return s.printJSON(r)
					}
					s.printCaddyRoutesTable([]*caddyadminclient.RouteResponse{r})
					return nil
				}
			}
			fmt.Fprintf(s.rootCmd.OutOrStdout(), "added route %q\n", s.routeAddID)
			return nil
		},
	}
	s.caddyRouteDeleteCmd = &cobra.Command{
		Use:   "delete <server-id> <route-id>",
		Short: "Delete a route from a Caddy server",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := caddyadminclient.NewManager(s.globalCaddyAdmin)
			if err := mgr.DeleteRoute(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(s.rootCmd.OutOrStdout(), "deleted route %q from server %q\n", args[1], args[0])
			return nil
		},
	}
	s.caddyCmd.PersistentFlags().StringVar(&s.globalCaddyAdmin, "addr", envOr("CADDY_ADMIN_ADDR", "http://localhost:2019"), "Caddy admin API address")
	s.caddyCmd.PersistentFlags().StringVar(&s.rejectedCaddyAdminAddr, "admin-addr", "", "")
	_ = s.caddyCmd.PersistentFlags().MarkHidden("admin-addr")
	s.caddyServerCreateCmd.Flags().StringVar(&s.serverCreateID, "id", "", "server ID (required)")
	s.caddyServerCreateCmd.Flags().StringArrayVar(&s.serverCreateListen, "listen", nil, "listen addresses (repeatable)")
	s.caddyServerCreateCmd.Flags().BoolVar(&s.serverCreateTLSAuto, "tls-auto", false, "enable automatic TLS")
	s.caddyServerUpdateCmd.Flags().StringArrayVar(&s.serverUpdateListen, "listen", nil, "listen addresses (repeatable)")
	s.caddyServerUpdateCmd.Flags().BoolVar(&s.serverUpdateTLSAuto, "tls-auto", false, "enable automatic TLS")
	s.caddyRouteAddCmd.Flags().StringVar(&s.routeAddID, "id", "", "route ID (required)")
	s.caddyRouteAddCmd.Flags().IntVar(&s.routeAddOrder, "order", 0, "insertion position (0 = first)")
	s.caddyRouteAddCmd.Flags().StringArrayVar(&s.routeAddPaths, "path", nil, "match path (repeatable)")
	s.caddyRouteAddCmd.Flags().StringArrayVar(&s.routeAddHosts, "host", nil, "match host (repeatable)")
	s.caddyRouteAddCmd.Flags().StringArrayVar(&s.routeAddHandlers, "handler", nil, `handler spec: type[:key=val,...] (repeatable)`)
	s.caddyServerCmd.AddCommand(s.caddyServerListCmd, s.caddyServerGetCmd, s.caddyServerCreateCmd, s.caddyServerUpdateCmd, s.caddyServerDeleteCmd)
	s.caddyRouteCmd.AddCommand(s.caddyRouteListCmd, s.caddyRouteAddCmd, s.caddyRouteDeleteCmd)
	s.caddyCmd.AddCommand(s.caddyServerCmd, s.caddyRouteCmd)
	s.rootCmd.AddCommand(s.caddyCmd)
}
