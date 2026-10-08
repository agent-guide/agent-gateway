package ctlcmd

import (
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"

	"github.com/agent-guide/agent-gateway/pkg/gateway/agentroute"
	"github.com/spf13/cobra"
)

func (s *commandState) loadAgentRouteFile() (agentroute.AgentRouteConfig, error) {
	if s.gatewayAgentRouteFile == "" {
		return agentroute.AgentRouteConfig{}, fmt.Errorf("--file is required")
	}
	data, err := os.ReadFile(s.gatewayAgentRouteFile)
	if err != nil {
		return agentroute.AgentRouteConfig{}, err
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return agentroute.AgentRouteConfig{}, err
	}
	data, err = json.Marshal(raw)
	if err != nil {
		return agentroute.AgentRouteConfig{}, err
	}
	var cfg agentroute.AgentRouteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
func (s *commandState) initAgentRoute() {
	s.gatewayAgentRouteCmd = &cobra.Command{Use: "agent-route", Short: "Manage unified Agent ingress routes"}
	s.gatewayAgentRouteListCmd = &cobra.Command{Use: "list", Short: "List Agent routes", RunE: func(cmd *cobra.Command, args []string) error {
		items, err := s.newGatewayClient().ListAgentRoutes(cmd.Context())
		if err != nil {
			return err
		}
		return s.printJSON(items)
	}}
	s.gatewayAgentRouteGetCmd = &cobra.Command{Use: "get <agent-route-id>", Short: "Get one Agent route", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		item, err := s.newGatewayClient().GetAgentRoute(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return s.printJSON(item)
	}}
	s.gatewayAgentRouteDeleteCmd = &cobra.Command{Use: "delete <agent-route-id>", Short: "Delete one Agent route", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		item, err := s.newGatewayClient().DeleteAgentRoute(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return s.printJSON(item)
	}}
	s.gatewayAgentRouteCreateCmd = &cobra.Command{Use: "create", Short: "Create an Agent route from --file", RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := s.loadAgentRouteFile()
		if err != nil {
			return err
		}
		item, err := s.newGatewayClient().CreateAgentRoute(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		return s.printJSON(item)
	}}
	s.gatewayAgentRouteUpdateCmd = &cobra.Command{Use: "update <agent-route-id>", Short: "Update an Agent route from --file", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := s.loadAgentRouteFile()
		if err != nil {
			return err
		}
		item, err := s.newGatewayClient().UpdateAgentRoute(cmd.Context(), args[0], cfg)
		if err != nil {
			return err
		}
		return s.printJSON(item)
	}}
	s.gatewayAgentRouteCreateCmd.Flags().StringVarP(&s.gatewayAgentRouteFile, "file", "f", "", "Agent route YAML or JSON")
	s.gatewayAgentRouteUpdateCmd.Flags().StringVarP(&s.gatewayAgentRouteFile, "file", "f", "", "Agent route YAML or JSON")
	s.gatewayAgentRouteCmd.AddCommand(s.gatewayAgentRouteListCmd, s.gatewayAgentRouteGetCmd, s.gatewayAgentRouteCreateCmd, s.gatewayAgentRouteUpdateCmd, s.gatewayAgentRouteDeleteCmd)
	s.rootCmd.AddCommand(s.gatewayAgentRouteCmd)
}
