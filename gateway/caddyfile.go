package gateway

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	localapikeypkg "github.com/agent-guide/caddy-agent-gateway/gateway/localapikey"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
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
		case "authenticator":
			if err := parseAuthenticator(d, app); err != nil {
				return nil, err
			}
		case "route":
			if err := parseRoute(d, app); err != nil {
				return nil, err
			}
		case "localapikey":
			if err := parseLocalAPIKey(d, app); err != nil {
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
	providerName, err := providerNameFromSegment(scan)
	if err != nil {
		return err
	}

	unmarshal := caddyfile.NewDispenser(segment)
	if !unmarshal.Next() || !unmarshal.NextArg() {
		return d.ArgErr()
	}
	modID := "llm.providers." + providerName
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

func providerNameFromSegment(d *caddyfile.Dispenser) (string, error) {
	var providerName string
	for d.NextBlock(0) {
		if d.Val() != "provider_name" {
			continue
		}
		if providerName != "" {
			return "", d.Err("provider_name already configured")
		}
		if !d.NextArg() {
			return "", d.ArgErr()
		}
		providerName = d.Val()
		if d.NextArg() {
			return "", d.ArgErr()
		}
	}
	if providerName == "" {
		return "", d.Err("provider_name is required")
	}
	return providerName, nil
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

func parseAuthenticator(d *caddyfile.Dispenser, app *App) error {
	if !d.NextArg() {
		return d.ArgErr()
	}
	name := d.Val()
	modID := "llm.authenticators." + name
	unm, err := caddyfile.UnmarshalModule(d, modID)
	if err != nil {
		return err
	}

	if app.AuthenticatorsRaw == nil {
		app.AuthenticatorsRaw = make(map[string]json.RawMessage)
	}
	app.AuthenticatorsRaw[name] = caddyconfig.JSON(unm, nil)
	return nil
}

func parseRoute(d *caddyfile.Dispenser, app *App) error {
	route, err := parseRouteSegment(d)
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

func parseLocalAPIKey(d *caddyfile.Dispenser, app *App) error {
	key, err := parseLocalAPIKeySegment(d)
	if err != nil {
		return err
	}

	for _, declared := range app.LocalAPIKeys {
		if declared.Key == key.Key {
			return d.Errf("duplicate localapikey %q", key.Key)
		}
	}
	app.LocalAPIKeys = append(app.LocalAPIKeys, key)
	return nil
}

func parseLocalAPIKeySegment(d *caddyfile.Dispenser) (localapikeypkg.LocalAPIKey, error) {
	seg := d.NewFromNextSegment()
	if !seg.Next() {
		return localapikeypkg.LocalAPIKey{}, d.Err("expected localapikey directive")
	}

	args := seg.RemainingArgsRaw()
	if len(args) != 1 {
		return localapikeypkg.LocalAPIKey{}, seg.ArgErr()
	}

	key := localapikeypkg.LocalAPIKey{
		Key: strings.Trim(args[0], "\"`"),
	}

	for seg.NextBlock(0) {
		name := seg.Val()
		args := seg.RemainingArgsRaw()
		switch name {
		case "user_id":
			if len(args) != 1 {
				return localapikeypkg.LocalAPIKey{}, seg.ArgErr()
			}
			key.UserID = strings.Trim(args[0], "\"`")
		case "name":
			if len(args) != 1 {
				return localapikeypkg.LocalAPIKey{}, seg.ArgErr()
			}
			key.Name = strings.Trim(args[0], "\"`")
		case "description":
			if len(args) != 1 {
				return localapikeypkg.LocalAPIKey{}, seg.ArgErr()
			}
			key.Description = strings.Trim(args[0], "\"`")
		case "disabled":
			if len(args) == 0 {
				key.Disabled = true
				continue
			}
			if len(args) != 1 {
				return localapikeypkg.LocalAPIKey{}, seg.ArgErr()
			}
			v, err := strconv.ParseBool(strings.Trim(args[0], "\"`"))
			if err != nil {
				return localapikeypkg.LocalAPIKey{}, seg.Errf("invalid disabled value: %s", args[0])
			}
			key.Disabled = v
		case "allowed_route":
			if len(args) == 0 {
				return localapikeypkg.LocalAPIKey{}, seg.ArgErr()
			}
			for _, arg := range args {
				key.AllowedRouteIDs = append(key.AllowedRouteIDs, strings.Trim(arg, "\"`"))
			}
		case "status_message":
			if len(args) != 1 {
				return localapikeypkg.LocalAPIKey{}, seg.ArgErr()
			}
			key.StatusMessage = strings.Trim(args[0], "\"`")
		case "expires_at":
			if len(args) != 1 {
				return localapikeypkg.LocalAPIKey{}, seg.ArgErr()
			}
			expiresAt, err := time.Parse(time.RFC3339, strings.Trim(args[0], "\"`"))
			if err != nil {
				return localapikeypkg.LocalAPIKey{}, seg.Errf("invalid expires_at value: %s", args[0])
			}
			key.ExpiresAt = expiresAt
		default:
			return localapikeypkg.LocalAPIKey{}, seg.Errf("unknown subdirective: %s", name)
		}
	}

	return key, nil
}

// ParseRouteSegment parses a route declaration from the current directive or subdirective.
// The dispenser must already be positioned on the route directive token.
func parseRouteSegment(d *caddyfile.Dispenser) (routepkg.AgentRoute, error) {
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
		Policy: routepkg.RoutePolicy{
			Selection: routepkg.SelectionPolicy{Strategy: routepkg.RouteSelectionStrategyAuto},
		},
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
		case "require_local_api_key":
			if len(args) == 0 {
				route.Policy.Auth.RequireLocalAPIKey = true
				continue
			}
			if len(args) != 1 {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			v, err := strconv.ParseBool(strings.Trim(args[0], "\"`"))
			if err != nil {
				return routepkg.AgentRoute{}, seg.Errf("invalid require_local_api_key value: %s", args[0])
			}
			route.Policy.Auth.RequireLocalAPIKey = v
		case "allowed_model":
			if len(args) == 0 {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			for _, arg := range args {
				route.Policy.AllowedModels = append(route.Policy.AllowedModels, strings.Trim(arg, "\"`"))
			}
		case "target":
			if len(args) < 2 || len(args) > 3 || strings.Trim(args[0], "\"`") != "provider" {
				return routepkg.AgentRoute{}, seg.ArgErr()
			}
			target := routepkg.RouteTarget{
				ProviderRef: strings.Trim(args[1], "\"`"),
				Mode:        routepkg.TargetModeWeighted,
				Weight:      1,
			}
			if len(args) == 3 {
				weight, err := strconv.Atoi(strings.Trim(args[2], "\"`"))
				if err != nil {
					return routepkg.AgentRoute{}, seg.Errf("invalid target weight: %s", args[2])
				}
				target.Weight = weight
			}
			route.Targets = append(route.Targets, target)
		default:
			return routepkg.AgentRoute{}, seg.Errf("unknown subdirective: %s", name)
		}
	}

	route.Policy.Defaults()
	return route, nil
}
