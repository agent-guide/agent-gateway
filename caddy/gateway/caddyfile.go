package gateway

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/caddy-agent-gateway/pkg/gateway/route"
	virtualkeypkg "github.com/agent-guide/caddy-agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/credentialmgr"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

func init() {
	httpcaddyfile.RegisterGlobalOption("agent_gateway", parseApp)
}

func parseApp(d *caddyfile.Dispenser, existingVal any) (any, error) {
	app := &App{}
	if current, ok := existingVal.(*App); ok && current != nil {
		app = current
	}

	if !d.Next() {
		return nil, d.Err("expected directive name")
	}
	if d.NextArg() {
		return nil, d.ArgErr()
	}

	for d.NextBlock(0) {
		switch d.Val() {
		case "provider":
			if err := parseProvider(d, app); err != nil {
				return nil, err
			}
		case "config_store":
			if err := parseConfigStore(d, app); err != nil {
				return nil, err
			}
		case "route":
			if err := parseRoute(d, app); err != nil {
				return nil, err
			}
		case "logical_model":
			return nil, d.Err("logical_model is no longer supported; define model targets inline within route blocks")
		case "virtualkey":
			if err := parseVirtualKey(d, app); err != nil {
				return nil, err
			}
		default:
			return nil, d.Errf("unknown subdirective: %s", d.Val())
		}
	}

	return httpcaddyfile.App{
		Name:  "agent_gateway",
		Value: caddyconfig.JSON(app, nil),
	}, nil
}

func parseProvider(d *caddyfile.Dispenser, app *App) error {
	segment := d.NextSegment()
	scan := caddyfile.NewDispenser(segment)
	if !scan.Next() {
		return d.Err("expected provider directive")
	}
	if !scan.NextArg() {
		return scan.ArgErr()
	}
	providerID := scan.Val()
	if scan.NextArg() {
		return scan.ArgErr()
	}
	providerType, err := providerTypeFromSegment(scan)
	if err != nil {
		return err
	}

	unmarshal := caddyfile.NewDispenser(segment)
	if !unmarshal.Next() || !unmarshal.NextArg() {
		return d.ArgErr()
	}
	modID := "llm.providers." + providerType
	unm, err := caddyfile.UnmarshalModule(unmarshal, modID)
	if err != nil {
		return err
	}

	if app.ProvidersRaw == nil {
		app.ProvidersRaw = make(map[string]json.RawMessage)
	}
	if _, exists := app.ProvidersRaw[providerID]; exists {
		return d.Errf("duplicate provider %q", providerID)
	}
	app.ProvidersRaw[providerID] = caddyconfig.JSON(unm, nil)
	return nil
}

func providerTypeFromSegment(d *caddyfile.Dispenser) (string, error) {
	var providerType string
	for d.NextBlock(0) {
		if d.Val() != "provider_type" {
			continue
		}
		if providerType != "" {
			return "", d.Err("provider_type already configured")
		}
		if !d.NextArg() {
			return "", d.ArgErr()
		}
		providerType = d.Val()
		if d.NextArg() {
			return "", d.ArgErr()
		}
	}
	if providerType == "" {
		return "", d.Err("provider_type is required")
	}
	return providerType, nil
}

func parseConfigStore(d *caddyfile.Dispenser, app *App) error {
	if len(app.ConfigStoreRaw) != 0 {
		return d.Err("config_store already configured")
	}
	if !d.NextArg() {
		return d.ArgErr()
	}
	name := d.Val()
	modID := "agent_gateway.config_stores." + name
	unm, err := caddyfile.UnmarshalModule(d, modID)
	if err != nil {
		return err
	}

	app.ConfigStoreRaw = caddy.ModuleMap{
		name: caddyconfig.JSON(unm, nil),
	}
	return nil
}

func parseRoute(d *caddyfile.Dispenser, app *App) error {
	route, err := parseRouteSegment(d, app)
	if err != nil {
		return err
	}

	for _, declared := range app.Routes {
		if declared.ID == route.ID {
			return d.Errf("duplicate route %q", route.ID)
		}
	}
	app.Routes = append(app.Routes, route)
	return nil
}

func parseVirtualKey(d *caddyfile.Dispenser, app *App) error {
	key, err := parseVirtualKeySegment(d)
	if err != nil {
		return err
	}

	for _, declared := range app.VirtualKeys {
		if declared.Key == key.Key {
			return d.Errf("duplicate virtualkey %q", key.Key)
		}
	}
	app.VirtualKeys = append(app.VirtualKeys, key)
	return nil
}

func parseVirtualKeySegment(d *caddyfile.Dispenser) (virtualkeypkg.VirtualKey, error) {
	seg := d.NewFromNextSegment()
	if !seg.Next() {
		return virtualkeypkg.VirtualKey{}, d.Err("expected virtualkey directive")
	}

	args := seg.RemainingArgsRaw()
	if len(args) != 1 {
		return virtualkeypkg.VirtualKey{}, seg.ArgErr()
	}

	key := virtualkeypkg.VirtualKey{
		Key: strings.Trim(args[0], "\"`"),
	}

	for seg.NextBlock(0) {
		name := seg.Val()
		args := seg.RemainingArgsRaw()
		switch name {
		case "tag":
			if len(args) != 1 {
				return virtualkeypkg.VirtualKey{}, seg.ArgErr()
			}
			key.Tag = strings.Trim(args[0], "\"`")
		case "name":
			if len(args) != 1 {
				return virtualkeypkg.VirtualKey{}, seg.ArgErr()
			}
			key.Name = strings.Trim(args[0], "\"`")
		case "description":
			if len(args) != 1 {
				return virtualkeypkg.VirtualKey{}, seg.ArgErr()
			}
			key.Description = strings.Trim(args[0], "\"`")
		case "disabled":
			if len(args) == 0 {
				key.Disabled = true
				continue
			}
			if len(args) != 1 {
				return virtualkeypkg.VirtualKey{}, seg.ArgErr()
			}
			v, err := strconv.ParseBool(strings.Trim(args[0], "\"`"))
			if err != nil {
				return virtualkeypkg.VirtualKey{}, seg.Errf("invalid disabled value: %s", args[0])
			}
			key.Disabled = v
		case "allowed_route":
			if len(args) == 0 {
				return virtualkeypkg.VirtualKey{}, seg.ArgErr()
			}
			for _, arg := range args {
				key.AllowedRouteIDs = append(key.AllowedRouteIDs, strings.Trim(arg, "\"`"))
			}
		case "status_message":
			if len(args) != 1 {
				return virtualkeypkg.VirtualKey{}, seg.ArgErr()
			}
			key.StatusMessage = strings.Trim(args[0], "\"`")
		case "expires_at":
			if len(args) != 1 {
				return virtualkeypkg.VirtualKey{}, seg.ArgErr()
			}
			expiresAt, err := time.Parse(time.RFC3339, strings.Trim(args[0], "\"`"))
			if err != nil {
				return virtualkeypkg.VirtualKey{}, seg.Errf("invalid expires_at value: %s", args[0])
			}
			key.ExpiresAt = expiresAt
		default:
			return virtualkeypkg.VirtualKey{}, seg.Errf("unknown subdirective: %s", name)
		}
	}

	return key, nil
}

// ParseRouteSegment parses a route declaration from the current directive or subdirective.
// The dispenser must already be positioned on the route directive token.
func parseRouteSegment(d *caddyfile.Dispenser, app *App) (routepkg.AgentRoute, error) {
	seg := d.NewFromNextSegment()
	if !seg.Next() {
		return routepkg.AgentRoute{}, d.Err("expected route directive")
	}

	args := seg.RemainingArgsRaw()
	if len(args) != 1 {
		return routepkg.AgentRoute{}, seg.ArgErr()
	}

	routeID := strings.Trim(args[0], "\"`")
	route := routepkg.AgentRoute{
		ID: routeID,
	}

	for seg.NextBlock(0) {
		name := seg.Val()
		args := seg.RemainingArgsRaw()
		switch name {
		case "llm_api":
			if len(args) != 1 {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			route.LLMAPI = strings.Trim(args[0], "\"`")
		case "host":
			if len(args) != 1 {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			route.Match.Host = strings.Trim(args[0], "\"`")
		case "path_prefix":
			if len(args) != 1 {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			route.Match.PathPrefix = strings.Trim(args[0], "\"`")
		case "method":
			if len(args) == 0 {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			for _, arg := range args {
				route.Match.Methods = append(route.Match.Methods, strings.Trim(arg, "\"`"))
			}
		case "require_virtual_key":
			if len(args) == 0 {
				route.AuthPolicy.RequireVirtualKey = true
				continue
			}
			if len(args) != 1 {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			v, err := strconv.ParseBool(strings.Trim(args[0], "\"`"))
			if err != nil {
				return routepkg.AgentRoute{}, seg.Errf("invalid require_virtual_key value: %s", args[0])
			}
			route.AuthPolicy.RequireVirtualKey = v
		case "target":
			if len(args) < 2 {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			switch strings.Trim(args[0], "\"`") {
			case "provider":
				if len(args) != 2 {
					return routepkg.AgentRoute{}, seg.ArgErr()
				}
				if route.TargetPolicy.ProviderTarget.ProviderID != "" {
					return routepkg.AgentRoute{}, seg.Err("target provider may appear at most once")
				}
				route.TargetPolicy.ProviderTarget = routepkg.DirectProviderTarget{ProviderID: strings.Trim(args[1], "\"`")}
			case "model":
				if len(args) < 4 {
					return routepkg.AgentRoute{}, seg.ArgErr()
				}
				modelName := strings.Trim(args[1], "\"`")
				candidate := routepkg.RouteModelCandidate{
					ProviderID:    strings.Trim(args[2], "\"`"),
					UpstreamModel: strings.Trim(args[3], "\"`"),
				}
				target := routeModelTargetByName(&route, modelName)
				if target == nil {
					route.TargetPolicy.ModelTargets = append(route.TargetPolicy.ModelTargets, routepkg.RouteModelTarget{Name: modelName})
					target = &route.TargetPolicy.ModelTargets[len(route.TargetPolicy.ModelTargets)-1]
				}
				for i := 4; i < len(args); i++ {
					token := strings.Trim(args[i], "\"`")
					switch token {
					case "default":
						route.TargetPolicy.DefaultModel = modelName
					case "weight":
						i++
						if i >= len(args) {
							return routepkg.AgentRoute{}, seg.ArgErr()
						}
						weight, err := strconv.Atoi(strings.Trim(args[i], "\"`"))
						if err != nil {
							return routepkg.AgentRoute{}, seg.Errf("invalid target model weight: %s", args[i])
						}
						candidate.Weight = weight
					case "priority":
						i++
						if i >= len(args) {
							return routepkg.AgentRoute{}, seg.ArgErr()
						}
						priority, err := strconv.Atoi(strings.Trim(args[i], "\"`"))
						if err != nil {
							return routepkg.AgentRoute{}, seg.Errf("invalid target model priority: %s", args[i])
						}
						candidate.Priority = priority
					case "strategy":
						i++
						if i >= len(args) {
							return routepkg.AgentRoute{}, seg.ArgErr()
						}
						target.Strategy = routepkg.RouteSelectionStrategy(strings.Trim(args[i], "\"`"))
					default:
						return routepkg.AgentRoute{}, fmt.Errorf("unknown target model option: %s", token)
					}
				}
				target.Candidates = append(target.Candidates, candidate)
				registerStaticManagedModel(app, candidate)
			default:
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
		case "target_policy":
			if len(args) != 1 {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			if err := parseRouteTargetPolicy(seg, &route, app, strings.Trim(args[0], "\"`")); err != nil {
				return routepkg.AgentRoute{}, err
			}
		default:
			return routepkg.AgentRoute{}, seg.Errf("unknown subdirective: %s", name)
		}
	}

	route.Normalize()
	return route, nil
}

func parseRouteTargetPolicy(seg *caddyfile.Dispenser, route *routepkg.AgentRoute, app *App, kind string) error {
	policy := routepkg.RouteTargetPolicy{Type: routepkg.RouteTargetPolicyKind(kind)}
	switch policy.Type {
	case routepkg.RouteTargetPolicyKindDirectProvider, routepkg.RouteTargetPolicyKindLogicalModel:
	default:
		return seg.Errf("unknown target_policy kind: %s", kind)
	}

	for seg.NextBlock(1) {
		name := seg.Val()
		args := seg.RemainingArgsRaw()
		switch name {
		case "provider":
			if policy.Type != routepkg.RouteTargetPolicyKindDirectProvider {
				return seg.Err("provider may only be used in target_policy direct-provider")
			}
			if len(args) != 1 {
				return seg.ArgErr()
			}
			policy.ProviderID = strings.Trim(args[0], "\"`")
		case "model":
			if policy.Type != routepkg.RouteTargetPolicyKindLogicalModel {
				return seg.Err("model may only be used in target_policy logical-model")
			}
			if len(args) < 3 {
				return seg.ArgErr()
			}
			modelName := strings.Trim(args[0], "\"`")
			candidate := routepkg.LogicalModelCandidate{
				ProviderID:    strings.Trim(args[1], "\"`"),
				UpstreamModel: strings.Trim(args[2], "\"`"),
			}
			for i := 3; i < len(args); i++ {
				token := strings.Trim(args[i], "\"`")
				switch token {
				case "default":
					candidate.Default = true
					policy.DefaultModel = modelName
				case "weight":
					i++
					if i >= len(args) {
						return seg.ArgErr()
					}
					weight, err := strconv.Atoi(strings.Trim(args[i], "\"`"))
					if err != nil {
						return seg.Errf("invalid target_policy model weight: %s", args[i])
					}
					candidate.Weight = weight
				case "priority":
					i++
					if i >= len(args) {
						return seg.ArgErr()
					}
					priority, err := strconv.Atoi(strings.Trim(args[i], "\"`"))
					if err != nil {
						return seg.Errf("invalid target_policy model priority: %s", args[i])
					}
					candidate.Priority = priority
				default:
					return seg.Errf("unknown target_policy model option: %s", token)
				}
			}
			group := logicalModelGroupByName(&policy, modelName)
			if group == nil {
				policy.Models = append(policy.Models, routepkg.LogicalModelBindingGroup{Name: modelName})
				group = &policy.Models[len(policy.Models)-1]
			}
			group.Candidates = append(group.Candidates, candidate)
			registerStaticManagedModel(app, routepkg.RouteModelCandidate(candidate))
		case "model_selector_strategy":
			if len(args) != 1 {
				return seg.ArgErr()
			}
			policy.ModelSelectorStrategy = routepkg.RouteSelectionStrategy(strings.Trim(args[0], "\"`"))
		case "credential_selector":
			if len(args) != 1 {
				return seg.ArgErr()
			}
			policy.CredentialSelector = routepkg.RouteCredentialSelectStrategy(strings.Trim(args[0], "\"`"))
		case "credential_scope_order":
			if len(args) == 0 {
				return seg.ArgErr()
			}
			policy.CredentialScopeOrder = nil
			for _, arg := range args {
				policy.CredentialScopeOrder = append(policy.CredentialScopeOrder, routepkg.RouteCredentialScope(strings.Trim(arg, "\"`")))
			}
		case "credential_source_order":
			if len(args) == 0 {
				return seg.ArgErr()
			}
			policy.CredentialSourceOrder = nil
			for _, arg := range args {
				policy.CredentialSourceOrder = append(policy.CredentialSourceOrder, routepkg.RouteCredentialSource(strings.Trim(arg, "\"`")))
			}
		case "fallback":
			if len(args) != 0 {
				return seg.ArgErr()
			}
			for seg.NextBlock(2) {
				fallbackName := seg.Val()
				fallbackArgs := seg.RemainingArgsRaw()
				if len(fallbackArgs) != 1 {
					return seg.ArgErr()
				}
				switch fallbackName {
				case "enabled":
					enabled, err := strconv.ParseBool(strings.Trim(fallbackArgs[0], "\"`"))
					if err != nil {
						return seg.Errf("invalid fallback enabled value: %s", fallbackArgs[0])
					}
					policy.Fallback.Enabled = enabled
				case "max_num":
					maxNum, err := strconv.Atoi(strings.Trim(fallbackArgs[0], "\"`"))
					if err != nil {
						return seg.Errf("invalid fallback max_num value: %s", fallbackArgs[0])
					}
					policy.Fallback.MaxNum = maxNum
				default:
					return seg.Errf("unknown fallback subdirective: %s", fallbackName)
				}
			}
		default:
			return seg.Errf("unknown target_policy subdirective: %s", name)
		}
	}
	route.TargetPolicy = policy
	return nil
}

func logicalModelGroupByName(policy *routepkg.RouteTargetPolicy, name string) *routepkg.LogicalModelBindingGroup {
	for i := range policy.Models {
		if policy.Models[i].Name == name {
			return &policy.Models[i]
		}
	}
	return nil
}

func routeModelTargetByName(route *routepkg.AgentRoute, name string) *routepkg.RouteModelTarget {
	for i := range route.TargetPolicy.ModelTargets {
		if route.TargetPolicy.ModelTargets[i].Name == name {
			return &route.TargetPolicy.ModelTargets[i]
		}
	}
	return nil
}

func registerStaticManagedModel(app *App, candidate routepkg.RouteModelCandidate) {
	for i := range app.Models {
		if app.Models[i].ProviderID == candidate.ProviderID && app.Models[i].UpstreamModel == candidate.UpstreamModel {
			return
		}
	}
	model := modelcatalog.ManagedModel{
		ProviderID:      candidate.ProviderID,
		UpstreamModel:   candidate.UpstreamModel,
		CredentialScope: credentialmgr.ProviderIDCredentialScope(candidate.ProviderID),
		Enabled:         true,
	}
	model.Normalize()
	app.Models = append(app.Models, model)
}
