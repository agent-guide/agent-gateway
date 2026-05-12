package main

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/adminclient"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/agent-gateway/pkg/gatewaybundle"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

type gatewayApplySummary struct {
	File    string                    `json:"file"`
	Status  string                    `json:"status"`
	Actions []gatewayApplyAction      `json:"actions"`
	Counts  gatewayApplySummaryCounts `json:"counts"`
}

type gatewayApplyAction struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Action string `json:"action"`
	Error  string `json:"error,omitempty"`
}

type gatewayApplySummaryCounts struct {
	Create int `json:"create"`
	Update int `json:"update"`
	Skip   int `json:"skip"`
	Error  int `json:"error"`
}

func runGatewayApply(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("--file is required")
	}
	bundle, err := gatewaybundle.LoadFile(path)
	if err != nil {
		return err
	}
	if err := bundle.ValidateForConfigStore(); err != nil {
		return err
	}

	client := newGatewayClient()
	summary := gatewayApplySummary{
		File:   path,
		Status: "ok",
	}

	applyErrs := []error{}
	record := func(kind, id, action string, err error) {
		item := gatewayApplyAction{Kind: kind, ID: id, Action: action}
		switch action {
		case "create":
			summary.Counts.Create++
		case "update":
			summary.Counts.Update++
		case "skip":
			summary.Counts.Skip++
		case "error":
			summary.Counts.Error++
		}
		if err != nil {
			item.Error = err.Error()
			applyErrs = append(applyErrs, err)
			summary.Status = "error"
		}
		summary.Actions = append(summary.Actions, item)
	}

	if err := applyProviderTypes(ctx, client, bundle, record); err != nil {
		return err
	}
	if err := applyProviders(ctx, client, bundle, record); err != nil {
		return err
	}
	if err := applyManagedModels(ctx, client, bundle, record); err != nil {
		return err
	}
	if err := applyRoutes(ctx, client, bundle, record); err != nil {
		return err
	}
	if err := applyVirtualKeys(ctx, client, bundle, record); err != nil {
		return err
	}
	if err := applyCLIAuthAuthenticators(ctx, client, bundle, record); err != nil {
		return err
	}

	if outputFormat == "json" {
		if err := printJSON(summary); err != nil {
			return err
		}
	} else {
		printGatewayApplySummary(summary)
	}
	if len(applyErrs) > 0 {
		return fmt.Errorf("gateway apply finished with %d error(s)", len(applyErrs))
	}
	return nil
}

func applyProviderTypes(ctx context.Context, client *adminclient.Client, bundle *gatewaybundle.GatewayBundle, record func(kind, id, action string, err error)) error {
	items, err := client.ListProviderTypes(ctx)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, item := range items {
		current[strings.ToLower(strings.TrimSpace(item.ProviderType))] = item.Enabled
	}
	for _, item := range bundle.ProviderTypes {
		id := strings.ToLower(strings.TrimSpace(item.ProviderType))
		enabled, ok := current[id]
		if ok && enabled == item.Enabled {
			record("provider_type", id, "skip", nil)
			continue
		}
		var callErr error
		if item.Enabled {
			_, callErr = client.EnableProviderType(ctx, id)
		} else {
			_, callErr = client.DisableProviderType(ctx, id)
		}
		if callErr != nil {
			record("provider_type", id, "error", fmt.Errorf("provider_type %q: %w", id, callErr))
			continue
		}
		if ok {
			record("provider_type", id, "update", nil)
		} else {
			record("provider_type", id, "create", nil)
		}
	}
	return nil
}

func applyProviders(ctx context.Context, client *adminclient.Client, bundle *gatewaybundle.GatewayBundle, record func(kind, id, action string, err error)) error {
	items, err := client.ListProviders(ctx, adminclient.ProviderListOptions{})
	if err != nil {
		return err
	}
	current := map[string]adminclient.Provider{}
	for _, item := range items {
		current[item.Id] = item
	}
	for _, desired := range bundle.Providers {
		desired = provider.NormalizeConfig(desired, desired.Id, desired.ProviderType)
		item, ok := current[desired.Id]
		if !ok {
			if _, err := client.CreateProvider(ctx, desired); err != nil {
				record("provider", desired.Id, "error", fmt.Errorf("provider %q create: %w", desired.Id, err))
			} else {
				record("provider", desired.Id, "create", nil)
			}
			continue
		}
		currentCfg := provider.NormalizeConfig(item.ProviderConfig, item.Id, item.ProviderType)
		if providerConfigsEqual(currentCfg, desired) {
			record("provider", desired.Id, "skip", nil)
			continue
		}
		if item.ReadOnly {
			record("provider", desired.Id, "error", fmt.Errorf("provider %q is read-only", desired.Id))
			continue
		}
		if _, err := client.UpdateProvider(ctx, desired.Id, desired); err != nil {
			record("provider", desired.Id, "error", fmt.Errorf("provider %q update: %w", desired.Id, err))
		} else {
			record("provider", desired.Id, "update", nil)
		}
	}
	return nil
}

func applyManagedModels(ctx context.Context, client *adminclient.Client, bundle *gatewaybundle.GatewayBundle, record func(kind, id, action string, err error)) error {
	items, err := client.ListManagedModels(ctx, adminclient.ManagedModelListOptions{})
	if err != nil {
		return err
	}
	current := map[string]modelcatalog.ManagedModel{}
	for _, item := range items {
		model := item.ManagedModel
		model.Normalize()
		current[managedModelKey(model.ProviderID, model.UpstreamModel)] = model
	}
	for _, desired := range bundle.ManagedModels {
		desired.Normalize()
		key := managedModelKey(desired.ProviderID, desired.UpstreamModel)
		if currentModel, ok := current[key]; ok {
			if reflect.DeepEqual(currentModel, desired) {
				record("managed_model", key, "skip", nil)
				continue
			}
			if _, err := client.UpdateManagedModel(ctx, desired.ProviderID, desired.UpstreamModel, desired); err != nil {
				record("managed_model", key, "error", fmt.Errorf("managed_model %q update: %w", key, err))
			} else {
				record("managed_model", key, "update", nil)
			}
			continue
		}
		if _, err := client.CreateManagedModel(ctx, desired); err != nil {
			record("managed_model", key, "error", fmt.Errorf("managed_model %q create: %w", key, err))
		} else {
			record("managed_model", key, "create", nil)
		}
	}
	return nil
}

func applyRoutes(ctx context.Context, client *adminclient.Client, bundle *gatewaybundle.GatewayBundle, record func(kind, id, action string, err error)) error {
	items, err := client.ListRoutes(ctx, adminclient.RouteListOptions{})
	if err != nil {
		return err
	}
	current := map[string]adminclient.Route{}
	for _, item := range items {
		current[item.ID] = item
	}
	for _, desired := range bundle.Routes {
		desired.Normalize()
		item, ok := current[desired.ID]
		if !ok {
			if _, err := client.CreateRoute(ctx, desired); err != nil {
				record("route", desired.ID, "error", fmt.Errorf("route %q create: %w", desired.ID, err))
			} else {
				record("route", desired.ID, "create", nil)
			}
			continue
		}
		currentRoute := normalizeComparableRoute(item.AgentRoute)
		desiredRoute := normalizeComparableRoute(desired)
		if reflect.DeepEqual(currentRoute, desiredRoute) {
			record("route", desired.ID, "skip", nil)
			continue
		}
		if item.ReadOnly {
			record("route", desired.ID, "error", fmt.Errorf("route %q is read-only", desired.ID))
			continue
		}
		if _, err := client.UpdateRoute(ctx, desired.ID, desired); err != nil {
			record("route", desired.ID, "error", fmt.Errorf("route %q update: %w", desired.ID, err))
		} else {
			record("route", desired.ID, "update", nil)
		}
	}
	return nil
}

func applyCLIAuthAuthenticators(ctx context.Context, client *adminclient.Client, bundle *gatewaybundle.GatewayBundle, record func(kind, id, action string, err error)) error {
	items, err := client.ListCLIAuthAuthenticators(ctx)
	if err != nil {
		return err
	}
	current := map[string]adminclient.CLIAuthAuthenticator{}
	for _, item := range items {
		current[strings.ToLower(strings.TrimSpace(item.Name))] = item
	}
	for _, desired := range bundle.CLIAuthAuthenticators {
		name := strings.ToLower(strings.TrimSpace(desired.Name))
		item, ok := current[name]
		if ok && item.Enabled == desired.Enabled {
			if !desired.Enabled || reflect.DeepEqual(item.Config, desired.Config) {
				record("cliauth_authenticator", name, "skip", nil)
				continue
			}
		}

		req := adminclient.UpdateCLIAuthAuthenticatorRequest{
			Enabled: &desired.Enabled,
		}
		if desired.Enabled {
			cfg := desired.Config
			req.Config = &cfg
		}
		if _, err := client.UpdateCLIAuthAuthenticator(ctx, name, req); err != nil {
			record("cliauth_authenticator", name, "error", fmt.Errorf("cliauth authenticator %q update: %w", name, err))
			continue
		}
		if ok {
			record("cliauth_authenticator", name, "update", nil)
		} else {
			record("cliauth_authenticator", name, "create", nil)
		}
	}
	return nil
}

func applyVirtualKeys(ctx context.Context, client *adminclient.Client, bundle *gatewaybundle.GatewayBundle, record func(kind, id, action string, err error)) error {
	items, err := client.ListVirtualKeys(ctx, adminclient.VirtualKeyListOptions{})
	if err != nil {
		return err
	}
	current := map[string]adminclient.VirtualKey{}
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			record("virtual_key", item.Key, "error", fmt.Errorf("virtual_key with key %q has empty id", item.Key))
			continue
		}
		if existing, exists := current[id]; exists && existing.Key != item.Key {
			record("virtual_key", id, "error", fmt.Errorf("virtual_key id %q is ambiguous across keys %q and %q", id, existing.Key, item.Key))
			continue
		}
		current[id] = item
	}
	for _, desired := range bundle.VirtualKeys {
		id := desired.ID
		item, ok := current[id]
		if !ok {
			req := bundleVirtualKeyConfig(desired)
			if _, err := client.CreateVirtualKey(ctx, req); err != nil {
				record("virtual_key", id, "error", fmt.Errorf("virtual_key %q create: %w", id, err))
			} else {
				record("virtual_key", id, "create", nil)
			}
			continue
		}
		currentKey := normalizeComparableVirtualKey(item.VirtualKey)
		desiredKey := normalizeComparableVirtualKey(desired.ToRuntimeVirtualKey(item.Key))
		if reflect.DeepEqual(currentKey, desiredKey) {
			record("virtual_key", id, "skip", nil)
			continue
		}
		if item.ReadOnly {
			record("virtual_key", id, "error", fmt.Errorf("virtual_key %q is read-only", id))
			continue
		}
		req := bundleVirtualKeyConfig(desired)
		if _, err := client.UpdateVirtualKey(ctx, item.ID, req); err != nil {
			record("virtual_key", id, "error", fmt.Errorf("virtual_key %q update: %w", id, err))
		} else {
			record("virtual_key", id, "update", nil)
		}
	}
	return nil
}

func providerConfigsEqual(a, b provider.ProviderConfig) bool {
	a = provider.NormalizeConfig(a, a.Id, a.ProviderType)
	b = provider.NormalizeConfig(b, b.Id, b.ProviderType)
	return reflect.DeepEqual(a, b)
}

func normalizeComparableRoute(route routepkg.AgentRoute) routepkg.AgentRoute {
	route.Normalize()
	route.CreatedAt = time.Time{}
	route.UpdatedAt = time.Time{}
	return route
}

func normalizeComparableVirtualKey(key virtualkeypkg.VirtualKey) virtualkeypkg.VirtualKey {
	sort.Strings(key.AllowedRouteIDs)
	if len(key.AllowedRouteIDs) == 0 {
		key.AllowedRouteIDs = nil
	}
	key.CreatedAt = time.Time{}
	key.UpdatedAt = time.Time{}
	if key.ExpiresAt.IsZero() {
		key.ExpiresAt = time.Time{}
	}
	return key
}

func bundleVirtualKeyConfig(key gatewaybundle.BundleVirtualKey) adminclient.VirtualKeyConfig {
	return adminclient.VirtualKeyConfig{
		ID:              key.ID,
		Tag:             key.Tag,
		Description:     key.Description,
		Disabled:        key.Disabled,
		AllowedRouteIDs: append([]string(nil), key.AllowedRouteIDs...),
		StatusMessage:   key.StatusMessage,
		ExpiresAt:       key.ExpiresAt,
	}
}

func managedModelKey(providerID, upstreamModel string) string {
	return strings.TrimSpace(providerID) + "/" + strings.TrimSpace(upstreamModel)
}

func printGatewayApplySummary(summary gatewayApplySummary) {
	fmt.Fprintf(rootCmd.OutOrStdout(), "gateway apply: %s\n", summary.File)
	for _, action := range summary.Actions {
		if action.Error != "" {
			fmt.Fprintf(rootCmd.OutOrStdout(), "  %s %s %s: %s\n", action.Action, action.Kind, action.ID, action.Error)
			continue
		}
		fmt.Fprintf(rootCmd.OutOrStdout(), "  %s %s %s\n", action.Action, action.Kind, action.ID)
	}
	fmt.Fprintf(rootCmd.OutOrStdout(), "summary: create=%d update=%d skip=%d error=%d\n",
		summary.Counts.Create,
		summary.Counts.Update,
		summary.Counts.Skip,
		summary.Counts.Error,
	)
}
