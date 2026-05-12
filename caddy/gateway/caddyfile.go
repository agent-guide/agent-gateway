package gateway

import (
	"encoding/json"
	"strconv"
	"strings"

	routepkg "github.com/agent-guide/agent-gateway/pkg/gateway/route"
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
			return nil, d.Err("virtualkey is no longer supported in the Caddyfile; create virtual keys through the Admin API")
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
				policy, ok := route.TargetPolicy.(*routepkg.RouteDirectProviderPolicy)
				if route.TargetPolicy == nil {
					policy = &routepkg.RouteDirectProviderPolicy{}
					route.TargetPolicy = policy
					ok = true
				}
				if !ok {
					return routepkg.AgentRoute{}, seg.Err("target provider cannot be mixed with model targets")
				}
				if policy.ProviderTarget.ProviderID != "" {
					return routepkg.AgentRoute{}, seg.Err("target provider may appear at most once")
				}
				policy.ProviderTarget = routepkg.DirectProviderTarget{ProviderID: strings.Trim(args[1], "\"`")}
			case "model":
				return routepkg.AgentRoute{}, seg.Err("target model is no longer supported in the Caddyfile; static routes must use a direct-provider target")
			default:
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
		case "target_policy":
			if len(args) != 1 {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			if err := parseRouteTargetPolicy(seg, &route, strings.Trim(args[0], "\"`")); err != nil {
				return routepkg.AgentRoute{}, err
			}
		default:
			return routepkg.AgentRoute{}, seg.Errf("unknown subdirective: %s", name)
		}
	}

	route.Normalize()
	return route, nil
}

func parseRouteTargetPolicy(seg *caddyfile.Dispenser, route *routepkg.AgentRoute, kind string) error {
	var directPolicy *routepkg.RouteDirectProviderPolicy
	switch routepkg.RouteTargetPolicyKind(kind) {
	case routepkg.RouteTargetPolicyKindDirectProvider:
		directPolicy = &routepkg.RouteDirectProviderPolicy{}
	case routepkg.RouteTargetPolicyKindLogicalModel:
		return seg.Err("target_policy logical-model is no longer supported in the Caddyfile; static routes must use direct-provider")
	default:
		return seg.Errf("unknown target_policy kind: %s", kind)
	}

	for seg.NextBlock(1) {
		name := seg.Val()
		args := seg.RemainingArgsRaw()
		switch name {
		case "provider":
			if len(args) != 1 {
				return seg.ArgErr()
			}
			directPolicy.ProviderID = strings.Trim(args[0], "\"`")
		case "model_selector_strategy":
			return seg.Err("model_selector_strategy may only be used in target_policy logical-model")
		case "credential_selector":
			if len(args) != 1 {
				return seg.ArgErr()
			}
			directPolicy.CredentialSelectorValue = routepkg.RouteCredentialSelectStrategy(strings.Trim(args[0], "\"`"))
		case "credential_scope_order":
			if len(args) == 0 {
				return seg.ArgErr()
			}
			directPolicy.CredentialScopeOrderValue = nil
			for _, arg := range args {
				directPolicy.CredentialScopeOrderValue = append(directPolicy.CredentialScopeOrderValue, routepkg.RouteCredentialScope(strings.Trim(arg, "\"`")))
			}
		case "credential_source_order":
			if len(args) == 0 {
				return seg.ArgErr()
			}
			directPolicy.CredentialSourceOrderValue = nil
			for _, arg := range args {
				directPolicy.CredentialSourceOrderValue = append(directPolicy.CredentialSourceOrderValue, routepkg.RouteCredentialSource(strings.Trim(arg, "\"`")))
			}
		case "fallback":
			return seg.Err("fallback may only be used in target_policy logical-model")
		default:
			return seg.Errf("unknown target_policy subdirective: %s", name)
		}
	}
	route.TargetPolicy = directPolicy
	return nil
}
