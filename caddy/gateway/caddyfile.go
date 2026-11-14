package gateway

import (
	"github.com/agent-guide/agent-gateway/internal/serverassembly"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"strconv"
	"strings"

	llmroutepkg "github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
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
		case "provider_types":
			if err := parseProviderTypes(d, app); err != nil {
				return nil, err
			}
		case "config_store":
			if err := parseConfigStore(d, app); err != nil {
				return nil, err
			}
		case "credential_refresh_command":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			app.CredentialRefreshCommand = strings.TrimSpace(d.Val())
			if app.CredentialRefreshCommand == "" {
				return nil, d.ArgErr()
			}
			app.CredentialRefreshArgs = nil
			for d.NextArg() {
				app.CredentialRefreshArgs = append(app.CredentialRefreshArgs, d.Val())
			}
		case "route":
			if err := parseRoute(d, app); err != nil {
				return nil, err
			}
		case "http_api_policy_file":
			if !d.AllArgs(&app.HTTPAPIPolicyFile) {
				return nil, d.ArgErr()
			}
		case "metrics":
			if err := parseMetrics(d, app); err != nil {
				return nil, err
			}
		case "provider_model":
			if err := parseProviderModel(d, app); err != nil {
				return nil, err
			}
		case "logical_model":
			if err := parseLogicalModel(d, app); err != nil {
				return nil, err
			}
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

func parseMetrics(d *caddyfile.Dispenser, app *App) error {
	seg := d.NewFromNextSegment()
	if !seg.Next() {
		return d.Err("expected metrics directive")
	}
	if seg.NextArg() {
		return seg.ArgErr()
	}
	for seg.NextBlock(0) {
		switch seg.Val() {
		case "retention_days":
			if !seg.NextArg() {
				return seg.ArgErr()
			}
			v, err := strconv.Atoi(seg.Val())
			if err != nil || v < 0 {
				return seg.Err("retention_days must be a non-negative integer")
			}
			if seg.NextArg() {
				return seg.ArgErr()
			}
			app.Metrics.RetentionDays = v
		case "max_agent_depth":
			if !seg.NextArg() {
				return seg.ArgErr()
			}
			v, err := strconv.Atoi(seg.Val())
			if err != nil || v < 0 {
				return seg.Err("max_agent_depth must be a non-negative integer")
			}
			if seg.NextArg() {
				return seg.ArgErr()
			}
			app.Metrics.MaxAgentDepth = v
		case "otlp":
			if seg.NextArg() {
				return seg.ArgErr()
			}
			if err := parseMetricsOTLP(seg, app); err != nil {
				return err
			}
		default:
			return seg.Errf("unknown metrics subdirective: %s", seg.Val())
		}
	}
	return nil
}

func parseMetricsOTLP(seg *caddyfile.Dispenser, app *App) error {
	for seg.NextBlock(1) {
		switch seg.Val() {
		case "endpoint":
			if !seg.NextArg() {
				return seg.ArgErr()
			}
			app.Metrics.OTLP.Endpoint = seg.Val()
			if seg.NextArg() {
				return seg.ArgErr()
			}
		case "protocol":
			if !seg.NextArg() {
				return seg.ArgErr()
			}
			app.Metrics.OTLP.Protocol = seg.Val()
			if seg.NextArg() {
				return seg.ArgErr()
			}
		case "insecure":
			if seg.NextArg() {
				return seg.ArgErr()
			}
			app.Metrics.OTLP.Insecure = true
		case "http_client_spans":
			if seg.NextArg() {
				return seg.ArgErr()
			}
			app.Metrics.OTLP.HTTPClientSpans = true
		case "components":
			if seg.NextArg() {
				return seg.ArgErr()
			}
			app.Metrics.OTLP.Components = true
		case "header":
			if !seg.NextArg() {
				return seg.ArgErr()
			}
			name := seg.Val()
			if !seg.NextArg() {
				return seg.ArgErr()
			}
			if app.Metrics.OTLP.Headers == nil {
				app.Metrics.OTLP.Headers = make(map[string]string)
			}
			app.Metrics.OTLP.Headers[name] = seg.Val()
			if seg.NextArg() {
				return seg.ArgErr()
			}
		case "service_name":
			if !seg.NextArg() {
				return seg.ArgErr()
			}
			app.Metrics.OTLP.ServiceName = seg.Val()
			if seg.NextArg() {
				return seg.ArgErr()
			}
		case "timeout_seconds":
			if !seg.NextArg() {
				return seg.ArgErr()
			}
			v, err := strconv.Atoi(seg.Val())
			if err != nil || v <= 0 {
				return seg.Err("timeout_seconds must be a positive integer")
			}
			if seg.NextArg() {
				return seg.ArgErr()
			}
			app.Metrics.OTLP.TimeoutSeconds = v
		default:
			return seg.Errf("unknown otlp subdirective: %s", seg.Val())
		}
	}
	if err := serverassembly.UsageConfig(app.Metrics).OTLP.Validate(); err != nil {
		return seg.Err(err.Error())
	}
	return nil
}

func parseProvider(d *caddyfile.Dispenser, app *App) error {
	seg := d.NewFromNextSegment()
	if !seg.Next() {
		return d.Err("expected provider directive")
	}
	cfg, err := parseProviderConfig(seg)
	if err != nil {
		return err
	}
	if app.Providers == nil {
		app.Providers = make(map[string]provider.ProviderConfig)
	}
	if _, exists := app.Providers[cfg.Id]; exists {
		return d.Errf("duplicate provider %q", cfg.Id)
	}
	app.Providers[cfg.Id] = cfg
	return nil
}

func parseProviderConfig(d *caddyfile.Dispenser) (provider.ProviderConfig, error) {
	var cfg provider.ProviderConfig
	if !d.NextArg() {
		return provider.ProviderConfig{}, d.ArgErr()
	}
	cfg.Id = d.Val()
	if cfg.Id == "" {
		return provider.ProviderConfig{}, d.Err("provider id is required")
	}
	if d.NextArg() {
		return provider.ProviderConfig{}, d.ArgErr()
	}
	for d.NextBlock(0) {
		switch d.Val() {
		case "provider_type":
			if cfg.ProviderType != "" {
				return provider.ProviderConfig{}, d.Err("provider_type already configured")
			}
			if !d.NextArg() {
				return provider.ProviderConfig{}, d.ArgErr()
			}
			cfg.ProviderType = strings.ToLower(strings.TrimSpace(d.Val()))
			if d.NextArg() {
				return provider.ProviderConfig{}, d.ArgErr()
			}
		case "api_key":
			if !d.NextArg() {
				return provider.ProviderConfig{}, d.ArgErr()
			}
			cfg.APIKey = d.Val()
			if d.NextArg() {
				return provider.ProviderConfig{}, d.ArgErr()
			}
		case "base_url":
			if !d.NextArg() {
				return provider.ProviderConfig{}, d.ArgErr()
			}
			cfg.BaseURL = d.Val()
			if d.NextArg() {
				return provider.ProviderConfig{}, d.ArgErr()
			}
		case "default_model":
			if !d.NextArg() {
				return provider.ProviderConfig{}, d.ArgErr()
			}
			cfg.DefaultModel = d.Val()
			if d.NextArg() {
				return provider.ProviderConfig{}, d.ArgErr()
			}
		case "request_timeout_seconds":
			v, err := parseProviderIntArg(d)
			if err != nil {
				return provider.ProviderConfig{}, err
			}
			cfg.Network.RequestTimeoutSeconds = v
		case "max_retries":
			v, err := parseProviderIntArg(d)
			if err != nil {
				return provider.ProviderConfig{}, err
			}
			cfg.Network.MaxRetries = v
		case "retry_delay_seconds":
			v, err := parseProviderIntArg(d)
			if err != nil {
				return provider.ProviderConfig{}, err
			}
			cfg.Network.RetryDelaySeconds = v
		case "max_idle_connections":
			v, err := parseProviderIntArg(d)
			if err != nil {
				return provider.ProviderConfig{}, err
			}
			cfg.Network.MaxIdleConnections = v
		case "max_idle_connections_per_host":
			v, err := parseProviderIntArg(d)
			if err != nil {
				return provider.ProviderConfig{}, err
			}
			cfg.Network.MaxIdleConnectionsPerHost = v
		case "idle_keep_alive_timeout_seconds":
			v, err := parseProviderIntArg(d)
			if err != nil {
				return provider.ProviderConfig{}, err
			}
			cfg.Network.IdleKeepAliveTimeoutSeconds = v
		case "proxy_url":
			if !d.NextArg() {
				return provider.ProviderConfig{}, d.ArgErr()
			}
			cfg.Network.ProxyURL = d.Val()
			if d.NextArg() {
				return provider.ProviderConfig{}, d.ArgErr()
			}
		case "header":
			args := d.RemainingArgs()
			if len(args) != 2 {
				return provider.ProviderConfig{}, d.ArgErr()
			}
			if cfg.Network.ExtraHeaders == nil {
				cfg.Network.ExtraHeaders = make(map[string]string)
			}
			cfg.Network.ExtraHeaders[args[0]] = args[1]
		case "option":
			args := d.RemainingArgs()
			if len(args) != 2 {
				return provider.ProviderConfig{}, d.ArgErr()
			}
			if cfg.Options == nil {
				cfg.Options = make(map[string]any)
			}
			cfg.Options[args[0]] = args[1]
		default:
			return provider.ProviderConfig{}, d.Errf("unknown provider subdirective: %s", d.Val())
		}
	}
	if cfg.ProviderType == "" {
		return provider.ProviderConfig{}, d.Err("provider_type is required")
	}
	// Normalization (defaults, fallbacks) is applied authoritatively at
	// provision time, which must also handle JSON-only configs.
	return cfg, nil
}

func parseProviderIntArg(d *caddyfile.Dispenser) (int, error) {
	if !d.NextArg() {
		return 0, d.ArgErr()
	}
	v, err := strconv.Atoi(d.Val())
	if err != nil {
		return 0, err
	}
	if d.NextArg() {
		return 0, d.ArgErr()
	}
	return v, nil
}

func parseProviderTypes(d *caddyfile.Dispenser, app *App) error {
	if len(app.ProviderTypes) > 0 {
		return d.Err("provider_types already configured")
	}
	// Parse the block on an isolated segment so iterating its inner tokens does
	// not disturb the shared dispenser's block bookkeeping for the surrounding
	// agent_gateway directive loop.
	seg := d.NewFromNextSegment()
	if !seg.Next() {
		return d.Err("expected provider_types directive")
	}
	if seg.NextArg() {
		return seg.ArgErr()
	}
	seen := map[string]struct{}{}
	for seg.NextBlock(0) {
		providerType := strings.ToLower(strings.TrimSpace(seg.Val()))
		if providerType == "" {
			return seg.Err("provider type is required")
		}
		// Listing a provider type enables it. Every registered type that is
		// not listed is disabled (exclusive policy applied at startup).
		if seg.NextArg() {
			return seg.ArgErr()
		}
		if _, exists := seen[providerType]; exists {
			return seg.Errf("duplicate provider type %q", providerType)
		}
		seen[providerType] = struct{}{}
		app.ProviderTypes = append(app.ProviderTypes, provider.ProviderTypeSetting{
			ProviderType: providerType,
			Enabled:      true,
		})
	}
	if len(app.ProviderTypes) == 0 {
		return d.Err("provider_types must declare at least one provider type")
	}
	return nil
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
func parseRouteSegment(d *caddyfile.Dispenser, app *App) (routecore.RouteConfig, error) {
	seg := d.NewFromNextSegment()
	if !seg.Next() {
		return routecore.RouteConfig{}, d.Err("expected route directive")
	}

	args := seg.RemainingArgsRaw()
	if len(args) != 1 {
		return routecore.RouteConfig{}, seg.ArgErr()
	}

	routeID := strings.Trim(args[0], "\"`")
	route := llmroutepkg.LLMRoute{
		RouteMetadata: routecore.RouteMetadata{
			ID:   routeID,
			Kind: routecore.RouteKindLLM,
		},
	}
	route.TargetPolicy = &llmroutepkg.RouteModelPolicy{}

	for seg.NextBlock(0) {
		name := seg.Val()
		args := seg.RemainingArgsRaw()
		switch name {
		case "protocol":
			if len(args) != 1 {
				return routecore.RouteConfig{}, seg.ArgErr()
			}
			route.Protocol = routecore.RouteProtocol(strings.Trim(args[0], "\"`"))
		case "host":
			if len(args) != 1 {
				return routecore.RouteConfig{}, seg.ArgErr()
			}
			route.MatchPolicy.Host = strings.Trim(args[0], "\"`")
		case "path_prefix":
			if len(args) != 1 {
				return routecore.RouteConfig{}, seg.ArgErr()
			}
			route.MatchPolicy.PathPrefix = strings.Trim(args[0], "\"`")
		case "method":
			if len(args) == 0 {
				return routecore.RouteConfig{}, seg.ArgErr()
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
				return routecore.RouteConfig{}, seg.ArgErr()
			}
			v, err := strconv.ParseBool(strings.Trim(args[0], "\"`"))
			if err != nil {
				return routecore.RouteConfig{}, seg.Errf("invalid require_virtual_key value: %s", args[0])
			}
			route.AuthPolicy.RequireVirtualKey = v

		case "target":
			p := route.TargetPolicy
			if err := parseModelTarget(seg, p, args); err != nil {
				return routecore.RouteConfig{}, err
			}
		case "default_model":
			if len(args) != 1 {
				return routecore.RouteConfig{}, seg.ArgErr()
			}
			route.TargetPolicy.DefaultModel = strings.Trim(args[0], "\"`")
		case "target_policy":
			if len(args) != 1 || strings.Trim(args[0], "\"`") != "models" {
				return routecore.RouteConfig{}, seg.Err("only target_policy models is supported")
			}
			if err := parseRouteTargetPolicy(seg, route.TargetPolicy); err != nil {
				return routecore.RouteConfig{}, err
			}

		default:
			return routecore.RouteConfig{}, seg.Errf("unknown subdirective: %s", name)
		}
	}

	if err := route.ValidateStaticDefinition(); err != nil {
		return routecore.RouteConfig{}, err
	}
	return route.ToConfig()
}

func parseModelTarget(seg *caddyfile.Dispenser, p *llmroutepkg.RouteModelPolicy, args []string) error {
	for i := range args {
		args[i] = strings.Trim(args[i], "\"`")
	}
	var target llmroutepkg.RouteModelTarget
	if len(args) == 3 && args[0] == "model" {
		target = llmroutepkg.RouteModelTarget{Name: args[2], Kind: "provider-model", ProviderID: args[1], UpstreamModel: args[2]}
	} else if len(args) == 3 && args[0] == "logical" {
		target = llmroutepkg.RouteModelTarget{Name: args[1], Kind: "logical-model", LogicalModelID: args[2]}
	} else {
		return seg.Err("target requires model <provider-id> <upstream-model> or logical <name> <logical-model-id>")
	}
	p.ModelTargets = append(p.ModelTargets, target)
	if p.DefaultModel == "" {
		p.DefaultModel = target.Name
	}
	return nil
}
func parseRouteTargetPolicy(seg *caddyfile.Dispenser, p *llmroutepkg.RouteModelPolicy) error {
	for seg.NextBlock(1) {
		name := seg.Val()
		args := seg.RemainingArgs()
		switch name {
		case "target":
			if err := parseModelTarget(seg, p, args); err != nil {
				return err
			}
		case "default_model":
			if len(args) != 1 {
				return seg.ArgErr()
			}
			p.DefaultModel = strings.Trim(args[0], "\"`")
		case "credential_selector":
			if len(args) != 1 {
				return seg.ArgErr()
			}
			p.CredentialSelectorValue = llmroutepkg.RouteCredentialSelectStrategy(args[0])
		case "credential_scope_order":
			if len(args) == 0 {
				return seg.ArgErr()
			}
			p.CredentialScopeOrderValue = nil
			for _, v := range args {
				p.CredentialScopeOrderValue = append(p.CredentialScopeOrderValue, llmroutepkg.RouteCredentialScope(v))
			}
		case "credential_type_order":
			if len(args) == 0 {
				return seg.ArgErr()
			}
			p.CredentialTypeOrderValue = nil
			for _, v := range args {
				p.CredentialTypeOrderValue = append(p.CredentialTypeOrderValue, llmroutepkg.RouteCredentialType(v))
			}
		default:
			return seg.Errf("unknown models policy directive %q", name)
		}
	}
	return nil
}
func parseProviderModel(d *caddyfile.Dispenser, app *App) error {
	seg := d.NewFromNextSegment()
	seg.Next()
	args := seg.RemainingArgs()
	if len(args) != 2 {
		return seg.ArgErr()
	}
	m := modelcatalog.ProviderModel{ProviderID: strings.Trim(args[0], "\"`"), UpstreamModel: strings.Trim(args[1], "\"`"), Enabled: true}
	for seg.NextBlock(0) {
		name := seg.Val()
		args := seg.RemainingArgs()
		if len(args) != 1 {
			return seg.ArgErr()
		}
		if name == "credential_scope" {
			m.CredentialScope = args[0]
			continue
		}
		if name == "context_window" || name == "max_output_tokens" {
			v, err := strconv.Atoi(args[0])
			if err != nil || v < 0 {
				return seg.Err("model limits must be nonnegative integers")
			}
			if m.CapabilityOverrides == nil {
				m.CapabilityOverrides = &provider.ModelCapabilities{}
			}
			if name == "context_window" {
				m.CapabilityOverrides.ContextWindow = v
			} else {
				m.CapabilityOverrides.MaxOutputTokens = v
			}
			continue
		}
		value, err := strconv.ParseBool(args[0])
		if err != nil {
			return err
		}
		if name == "enabled" {
			m.Enabled = value
			continue
		}
		if m.CapabilityOverrides == nil {
			m.CapabilityOverrides = &provider.ModelCapabilities{}
		}
		switch name {
		case "streaming":
			m.CapabilityOverrides.Streaming = value
		case "tools":
			m.CapabilityOverrides.Tools = value
		case "vision":
			m.CapabilityOverrides.Vision = value
		case "embeddings":
			m.CapabilityOverrides.Embeddings = value
		default:
			return seg.Errf("unknown provider_model directive %q", name)
		}
	}
	m.Normalize()
	if err := m.Validate(); err != nil {
		return err
	}
	app.ProviderModels = append(app.ProviderModels, m)
	return nil
}
func parseLogicalModel(d *caddyfile.Dispenser, app *App) error {
	seg := d.NewFromNextSegment()
	seg.Next()
	args := seg.RemainingArgs()
	if len(args) != 1 {
		return seg.ArgErr()
	}
	m := modelcatalog.LogicalModel{ID: strings.Trim(args[0], "\"`")}
	for seg.NextBlock(0) {
		name := seg.Val()
		args := seg.RemainingArgs()
		switch name {
		case "strategy":
			if len(args) != 1 {
				return seg.ArgErr()
			}
			m.Strategy = modelcatalog.SelectionStrategy(args[0])
		case "disabled":
			if len(args) != 1 {
				return seg.ArgErr()
			}
			v, err := strconv.ParseBool(args[0])
			if err != nil {
				return err
			}
			m.Disabled = v
		case "candidate":
			if len(args) != 2 {
				return seg.ArgErr()
			}
			c := modelcatalog.ModelCandidate{ProviderID: args[0], UpstreamModel: args[1]}
			for seg.NextBlock(1) {
				name := seg.Val()
				args := seg.RemainingArgs()
				if len(args) != 1 {
					return seg.ArgErr()
				}
				v, err := strconv.Atoi(args[0])
				if err != nil {
					return err
				}
				switch name {
				case "weight":
					c.Weight = v
				case "priority":
					c.Priority = v
				default:
					return seg.Errf("unknown candidate directive %q", name)
				}
			}
			m.Candidates = append(m.Candidates, c)
		case "fallback":
			if len(args) != 0 {
				return seg.ArgErr()
			}
			for seg.NextBlock(1) {
				name := seg.Val()
				args := seg.RemainingArgs()
				if len(args) != 1 {
					return seg.ArgErr()
				}
				switch name {
				case "enabled":
					v, err := strconv.ParseBool(args[0])
					if err != nil {
						return err
					}
					m.Fallback.Enabled = v
				case "max_num":
					v, err := strconv.Atoi(args[0])
					if err != nil {
						return err
					}
					m.Fallback.MaxNum = v
				default:
					return seg.Errf("unknown fallback directive %q", name)
				}
			}
		default:
			return seg.Errf("unknown logical_model directive %q", name)
		}
	}
	m.Normalize()
	if err := m.Validate(); err != nil {
		return err
	}
	app.LogicalModels = append(app.LogicalModels, m)
	return nil
}
