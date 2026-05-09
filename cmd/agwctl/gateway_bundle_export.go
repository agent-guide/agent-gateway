package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/agent-guide/caddy-agent-gateway/pkg/adminclient"
	"github.com/agent-guide/caddy-agent-gateway/pkg/gatewaybundle"
)

func runGatewayExport(ctx context.Context, path string) error {
	client := newGatewayClient()

	providerTypes, err := client.ListProviderTypes(ctx)
	if err != nil {
		return err
	}
	handlerTypes, err := client.ListLLMAPIHandlerTypes(ctx)
	if err != nil {
		return err
	}
	providers, err := client.ListProviders(ctx, adminclient.ProviderListOptions{})
	if err != nil {
		return err
	}
	managedModels, err := client.ListManagedModels(ctx, adminclient.ManagedModelListOptions{})
	if err != nil {
		return err
	}
	routes, err := client.ListRoutes(ctx, adminclient.RouteListOptions{})
	if err != nil {
		return err
	}
	virtualKeys, err := client.ListVirtualKeys(ctx, adminclient.VirtualKeyListOptions{})
	if err != nil {
		return err
	}

	bundle := &gatewaybundle.GatewayBundle{
		APIVersion: gatewaybundle.APIVersionV1Alpha1,
		Kind:       gatewaybundle.KindGatewayBundle,
	}
	for _, item := range providerTypes {
		bundle.ProviderTypes = append(bundle.ProviderTypes, gatewaybundle.ProviderTypeSetting{
			ProviderType: item.ProviderType,
			Enabled:      item.Enabled,
		})
	}
	for _, item := range handlerTypes {
		bundle.LLMAPIHandlerTypes = append(bundle.LLMAPIHandlerTypes, gatewaybundle.LLMAPIHandlerSetting{
			LLMAPIHandlerType: item.LLMApiHandlerType,
			Enabled:           item.Enabled,
		})
	}
	for _, item := range providers {
		bundle.Providers = append(bundle.Providers, item.ProviderConfig)
	}
	for _, item := range managedModels {
		bundle.ManagedModels = append(bundle.ManagedModels, item.ManagedModel)
	}
	for _, item := range routes {
		bundle.Routes = append(bundle.Routes, item.AgentRoute)
	}
	for _, item := range virtualKeys {
		bundle.VirtualKeys = append(bundle.VirtualKeys, item.VirtualKey)
	}

	sortGatewayBundle(bundle)
	yamlBytes, err := gatewaybundle.EncodeYAML(bundle)
	if err != nil {
		return err
	}
	if path == "" {
		_, err = cmdOrStdout().Write(yamlBytes)
		return err
	}
	if err := os.WriteFile(path, yamlBytes, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if outputFormat == "json" {
		return printJSON(map[string]any{
			"status": "ok",
			"file":   path,
		})
	}
	fmt.Fprintf(cmdOrStdout(), "gateway bundle exported: %s\n", path)
	return nil
}

func sortGatewayBundle(bundle *gatewaybundle.GatewayBundle) {
	sort.Slice(bundle.ProviderTypes, func(i, j int) bool {
		return bundle.ProviderTypes[i].ProviderType < bundle.ProviderTypes[j].ProviderType
	})
	sort.Slice(bundle.LLMAPIHandlerTypes, func(i, j int) bool {
		return bundle.LLMAPIHandlerTypes[i].LLMAPIHandlerType < bundle.LLMAPIHandlerTypes[j].LLMAPIHandlerType
	})
	sort.Slice(bundle.Providers, func(i, j int) bool {
		return bundle.Providers[i].Id < bundle.Providers[j].Id
	})
	sort.Slice(bundle.ManagedModels, func(i, j int) bool {
		if bundle.ManagedModels[i].ProviderID != bundle.ManagedModels[j].ProviderID {
			return bundle.ManagedModels[i].ProviderID < bundle.ManagedModels[j].ProviderID
		}
		return bundle.ManagedModels[i].UpstreamModel < bundle.ManagedModels[j].UpstreamModel
	})
	sort.Slice(bundle.Routes, func(i, j int) bool {
		return bundle.Routes[i].ID < bundle.Routes[j].ID
	})
	sort.Slice(bundle.VirtualKeys, func(i, j int) bool {
		return bundle.VirtualKeys[i].Key < bundle.VirtualKeys[j].Key
	})
}

func cmdOrStdout() *os.File {
	return os.Stdout
}
