package ctlcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	bundlepkg "github.com/agent-guide/agent-gateway/pkg/gateway/bundle"
)

func (s *commandState) runGatewayExport(ctx context.Context, path string) error {
	client := s.newGatewayClient()

	extensions, err := client.ListExtensions(ctx)
	if err != nil {
		return err
	}
	providers, err := client.ListProviders(ctx, adminclient.ProviderListOptions{})
	if err != nil {
		return err
	}
	providerModels, err := client.ListProviderModels(ctx, adminclient.ProviderModelListOptions{})
	if err != nil {
		return err
	}
	logicalModels, err := client.ListLogicalModels(ctx)
	if err != nil {
		return err
	}
	llmRoutes, err := client.ListLLMRoutes(ctx, adminclient.LLMRouteListOptions{})
	if err != nil {
		return err
	}
	virtualKeys, err := client.ListVirtualKeys(ctx, adminclient.VirtualKeyListOptions{})
	if err != nil {
		return err
	}
	connectors, err := client.ListConnectors(ctx)
	if err != nil {
		return err
	}
	mcpRoutes, err := client.ListMCPRoutes(ctx)
	if err != nil {
		return err
	}
	agentRoutes, err := client.ListAgentRoutes(ctx)
	if err != nil {
		return err
	}
	agents, err := client.ListAgents(ctx)
	if err != nil {
		return err
	}

	bundle := &bundlepkg.GatewayBundle{
		Extensions:    extensions,
		LogicalModels: logicalModels,
		APIVersion:    bundlepkg.APIVersionV1Alpha1,
		Kind:          bundlepkg.KindGatewayBundle,
	}
	for _, item := range providers {
		bundle.Providers = append(bundle.Providers, item.ProviderConfig)
	}
	for _, item := range providerModels {
		bundle.ProviderModels = append(bundle.ProviderModels, item.ProviderModel)
	}
	for _, item := range llmRoutes {
		routeCfg, err := item.LLMRoute()
		if err != nil {
			return fmt.Errorf("decode llm route %q: %w", item.ID, err)
		}
		cfg, err := routeCfg.ToConfig()
		if err != nil {
			return fmt.Errorf("encode llm route %q: %w", item.ID, err)
		}
		bundle.LLMRoutes = append(bundle.LLMRoutes, cfg)
	}
	for _, item := range virtualKeys {
		bundle.VirtualKeys = append(bundle.VirtualKeys, bundlepkg.BundleVirtualKeyFromRuntime(item.VirtualKey))
	}
	for _, item := range connectors {
		bundle.Connectors = append(bundle.Connectors, item.ConnectorConfig)
	}
	for _, item := range mcpRoutes {
		bundle.MCPRoutes = append(bundle.MCPRoutes, item.MCPRoute)
	}
	for _, item := range agentRoutes {
		bundle.AgentRoutes = append(bundle.AgentRoutes, item.AgentRouteConfig)
	}
	for _, item := range agents {
		bundle.Agents = append(bundle.Agents, item.Agent)
	}

	sortGatewayBundle(bundle)
	yamlBytes, err := bundlepkg.EncodeYAML(bundle)
	if err != nil {
		return err
	}
	if path == "" {
		_, err = s.cmdOrStdout().Write(yamlBytes)
		return err
	}
	if err := os.WriteFile(path, yamlBytes, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if s.outputFormat == "json" {
		return s.printJSON(map[string]any{
			"status": "ok",
			"file":   path,
		})
	}
	fmt.Fprintf(s.cmdOrStdout(), "gateway bundle exported: %s\n", path)
	return nil
}

func sortGatewayBundle(bundle *bundlepkg.GatewayBundle) {
	sort.Slice(bundle.LogicalModels, func(i, j int) bool { return bundle.LogicalModels[i].ID < bundle.LogicalModels[j].ID })
	sort.Slice(bundle.Extensions, func(i, j int) bool { return bundle.Extensions[i].ModuleID < bundle.Extensions[j].ModuleID })
	sort.Slice(bundle.Providers, func(i, j int) bool {
		return bundle.Providers[i].Id < bundle.Providers[j].Id
	})
	sort.Slice(bundle.ProviderModels, func(i, j int) bool {
		if bundle.ProviderModels[i].ProviderID != bundle.ProviderModels[j].ProviderID {
			return bundle.ProviderModels[i].ProviderID < bundle.ProviderModels[j].ProviderID
		}
		return bundle.ProviderModels[i].UpstreamModel < bundle.ProviderModels[j].UpstreamModel
	})
	sort.Slice(bundle.LLMRoutes, func(i, j int) bool {
		return bundle.LLMRoutes[i].ID < bundle.LLMRoutes[j].ID
	})
	sort.Slice(bundle.VirtualKeys, func(i, j int) bool {
		return bundle.VirtualKeys[i].ID < bundle.VirtualKeys[j].ID
	})
	sort.Slice(bundle.Connectors, func(i, j int) bool {
		return bundle.Connectors[i].ID < bundle.Connectors[j].ID
	})
	sort.Slice(bundle.MCPRoutes, func(i, j int) bool {
		return bundle.MCPRoutes[i].ID < bundle.MCPRoutes[j].ID
	})
	sort.Slice(bundle.AgentRoutes, func(i, j int) bool {
		return bundle.AgentRoutes[i].ID < bundle.AgentRoutes[j].ID
	})
	sort.Slice(bundle.Agents, func(i, j int) bool {
		return bundle.Agents[i].ID < bundle.Agents[j].ID
	})
}

func (s *commandState) cmdOrStdout() io.Writer {
	return s.rootCmd.OutOrStdout()
}
