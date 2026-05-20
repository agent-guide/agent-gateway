package gateway

import (
	"encoding/json"
	"strconv"
	"strings"

	llmroutepkg "github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
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
	modID := "agent_gateway.config_store_backends." + name
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

	for _, declared := range app.LLMRoutes {
		if declared.ID == route.ID {
			return d.Errf("duplicate route %q", route.ID)
		}
	}
	app.LLMRoutes = append(app.LLMRoutes, route)
	return nil
}

// ParseRouteSegment parses a route declaration from the current directive or subdirective.
// The dispenser must already be positioned on the route directive token.
func parseRouteSegment(d *caddyfile.Dispenser, app *App) (routecore.AgentRouteConfig, error) {
	seg := d.NewFromNextSegment()
	if !seg.Next() {
		return routecore.AgentRouteConfig{}, d.Err("expected route directive")
	}

	args := seg.RemainingArgsRaw()
	if len(args) != 1 {
		return routecore.AgentRouteConfig{}, seg.ArgErr()
	}

	routeID := strings.Trim(args[0], "\"`")
	route := llmroutepkg.LLMRouteConfig{
		AgentRouteConfig: routecore.AgentRouteConfig{
			ID:   routeID,
			Kind: routecore.RouteKindLLM,
		},
	}
	logicalTargetPolicy := false

	for seg.NextBlock(0) {
		name := seg.Val()
		args := seg.RemainingArgsRaw()
		switch name {
		case "protocol":
			if len(args) != 1 {
				return routecore.AgentRouteConfig{}, seg.ArgErr()
			}
			route.Protocol = routecore.RouteProtocol(strings.Trim(args[0], "\"`"))
		case "host":
			if len(args) != 1 {
				return routecore.AgentRouteConfig{}, seg.ArgErr()
			}
			route.MatchPolicy.Host = strings.Trim(args[0], "\"`")
		case "path_prefix":
			if len(args) != 1 {
				return routecore.AgentRouteConfig{}, seg.ArgErr()
			}
			route.MatchPolicy.PathPrefix = strings.Trim(args[0], "\"`")
		case "method":
			if len(args) == 0 {
				return routecore.AgentRouteConfig{}, seg.ArgErr()
			}
			for _, arg := range args {
				route.MatchPolicy.Methods = append(route.MatchPolicy.Methods, strings.Trim(arg, "\"`"))
			}
		case "require_virtual_key":
			if len(args) == 0 {
				route.AuthPolicy.RequireVirtualKey = true
				continue
			}
			if len(args) != 1 {
				return routecore.AgentRouteConfig{}, seg.ArgErr()
			}
			v, err := strconv.ParseBool(strings.Trim(args[0], "\"`"))
			if err != nil {
				return routecore.AgentRouteConfig{}, seg.Errf("invalid require_virtual_key value: %s", args[0])
			}
			route.AuthPolicy.RequireVirtualKey = v
		case "target":
			if len(args) < 2 {
				return routecore.AgentRouteConfig{}, seg.ArgErr()
			}
			switch strings.Trim(args[0], "\"`") {
			case "provider":
				if len(args) != 2 {
					return routecore.AgentRouteConfig{}, seg.ArgErr()
				}
				if logicalTargetPolicy {
					return routecore.AgentRouteConfig{}, seg.Err("target provider cannot be mixed with model targets")
				}
				directPolicy, ok := llmroutepkg.DirectProviderPolicyOf(route.TargetPolicy)
				if route.TargetPolicy != nil && !ok {
					return routecore.AgentRouteConfig{}, seg.Err("target provider cannot be mixed with model targets")
				}
				if !ok {
					directPolicy = &llmroutepkg.RouteDirectProviderPolicy{}
					route.TargetPolicy = directPolicy
				}
				if directPolicy.ProviderTarget.ProviderID != "" {
					return routecore.AgentRouteConfig{}, seg.Err("target provider may appear at most once")
				}
				directPolicy.ProviderTarget = llmroutepkg.DirectProviderTarget{ProviderID: strings.Trim(args[1], "\"`")}
			case "model":
				return routecore.AgentRouteConfig{}, seg.Err("target model is no longer supported in the Caddyfile; static routes must use a direct-provider target")
			default:
				return routecore.AgentRouteConfig{}, seg.ArgErr()
			}
		case "target_policy":
			if len(args) != 1 {
				return routecore.AgentRouteConfig{}, seg.ArgErr()
			}
			if err := parseRouteTargetPolicy(seg, &route, strings.Trim(args[0], "\"`"), &logicalTargetPolicy); err != nil {
				return routecore.AgentRouteConfig{}, err
			}
		default:
			return routecore.AgentRouteConfig{}, seg.Errf("unknown subdirective: %s", name)
		}
	}

	if err := route.ValidateStaticDefinition(); err != nil {
		return routecore.AgentRouteConfig{}, err
	}
	return route.ToConfig()
}

func parseRouteTargetPolicy(seg *caddyfile.Dispenser, route *llmroutepkg.LLMRouteConfig, kind string, logicalTargetPolicy *bool) error {
	var directPolicy *llmroutepkg.RouteDirectProviderPolicy
	switch llmroutepkg.RouteTargetPolicyKind(kind) {
	case llmroutepkg.RouteTargetPolicyKindDirectProvider:
		directPolicy = &llmroutepkg.RouteDirectProviderPolicy{}
	case llmroutepkg.RouteTargetPolicyKindLogicalModel:
		*logicalTargetPolicy = true
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
			directPolicy.CredentialSelectorValue = llmroutepkg.RouteCredentialSelectStrategy(strings.Trim(args[0], "\"`"))
		case "credential_scope_order":
			if len(args) == 0 {
				return seg.ArgErr()
			}
			directPolicy.CredentialScopeOrderValue = nil
			for _, arg := range args {
				directPolicy.CredentialScopeOrderValue = append(directPolicy.CredentialScopeOrderValue, llmroutepkg.RouteCredentialScope(strings.Trim(arg, "\"`")))
			}
		case "credential_type_order":
			if len(args) == 0 {
				return seg.ArgErr()
			}
			directPolicy.CredentialTypeOrderValue = nil
			for _, arg := range args {
				directPolicy.CredentialTypeOrderValue = append(directPolicy.CredentialTypeOrderValue, llmroutepkg.RouteCredentialType(strings.Trim(arg, "\"`")))
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
