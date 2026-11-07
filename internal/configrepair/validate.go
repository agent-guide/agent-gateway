package configrepair

import (
	"encoding/json"
	"fmt"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/gateway/agentroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/mcproute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/gatewaybundle"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/agent-guide/agent-gateway/pkg/mcp/service"
)

// Validate checks a complete prospective configuration, including references to
// families that are empty. Partial-bundle reference exemptions do not apply.
func Validate(records []Record) error {
	if issues := Inspect(records); len(issues) > 0 {
		i := issues[0]
		return fmt.Errorf("%s/%s %s: %s", i.Family, i.ID, i.Field, i.Action)
	}
	b := gatewaybundle.GatewayBundle{APIVersion: gatewaybundle.APIVersionV1Alpha1, Kind: gatewaybundle.KindGatewayBundle}
	staticBundle := gatewaybundle.GatewayBundle{APIVersion: gatewaybundle.APIVersionV1Alpha1, Kind: gatewaybundle.KindGatewayBundle}
	index := map[string]map[string]bool{}
	routeKinds := map[string]routecore.RouteKind{}
	staticProviders := map[string]bool{}
	for _, r := range records {
		if r.Family == "providers" && r.StaticInput {
			staticProviders[r.ID] = true
		}
	}
	for _, r := range records {
		// Validate the effective Provider view, matching runtime static
		// precedence, without removing stored records from the repair batch.
		if r.Family == "providers" && !r.StaticInput && staticProviders[r.ID] {
			continue
		}
		if index[r.Family] == nil {
			index[r.Family] = map[string]bool{}
		}
		index[r.Family][r.ID] = true
		var err error
		switch r.Family {
		case "routes":
			var cfg routecore.RouteConfig
			if err = json.Unmarshal(r.Data, &cfg); err != nil {
				break
			}
			if err = routecore.ValidateRouteID(cfg.ID); err != nil {
				break
			}
			routeKinds[cfg.ID] = cfg.Kind
			switch cfg.Kind {
			case routecore.RouteKindLLM:
				if cfg.Protocol != routecore.RouteProtocolOpenAI && cfg.Protocol != routecore.RouteProtocolAnthropic && cfg.Protocol != routecore.RouteProtocolCC {
					return fmt.Errorf("route %q: unsupported LLM protocol", cfg.ID)
				}
				b.LLMRoutes = append(b.LLMRoutes, cfg)
				if r.StaticInput {
					staticBundle.LLMRoutes = append(staticBundle.LLMRoutes, cfg)
				}
			case routecore.RouteKindMCP:
				var v mcproute.MCPRouteConfig
				if cfg.Protocol != routecore.RouteProtocolMCP {
					return fmt.Errorf("route %q: expected MCP protocol", cfg.ID)
				}
				v, err = mcproute.NewMCPRouteConfigFromConfig(cfg)
				b.MCPRoutes = append(b.MCPRoutes, v)
				if r.StaticInput {
					staticBundle.MCPRoutes = append(staticBundle.MCPRoutes, v)
				}
			case routecore.RouteKindAgent:
				var v agentroute.AgentRouteConfig
				v, err = agentroute.NewAgentRouteConfigFromConfig(cfg)
				b.AgentRoutes = append(b.AgentRoutes, v)
				if r.StaticInput {
					staticBundle.AgentRoutes = append(staticBundle.AgentRoutes, v)
				}
			default:
				err = fmt.Errorf("unsupported route kind %q", cfg.Kind)
			}
		case "agents":
			var v agent.Agent
			err = json.Unmarshal(r.Data, &v)
			b.Agents = append(b.Agents, v)
			if r.StaticInput {
				staticBundle.Agents = append(staticBundle.Agents, v)
			}
		case "providers":
			var v provider.ProviderConfig
			err = json.Unmarshal(r.Data, &v)
			b.Providers = append(b.Providers, v)
			if r.StaticInput {
				staticBundle.Providers = append(staticBundle.Providers, v)
			}
		case "mcp_services":
			var v service.MCPServiceConfig
			err = json.Unmarshal(r.Data, &v)
			b.MCPServices = append(b.MCPServices, v)
			if r.StaticInput {
				staticBundle.MCPServices = append(staticBundle.MCPServices, v)
			}
		case "virtual_keys":
			var v gatewaybundle.BundleVirtualKey
			err = json.Unmarshal(r.Data, &v)
			b.VirtualKeys = append(b.VirtualKeys, v)
			if r.StaticInput {
				staticBundle.VirtualKeys = append(staticBundle.VirtualKeys, v)
			}
		case "credentials":
			_, err = schema.CredentialSchema.Codec.Decode(r.Data)
		case "managed_models":
			var v modelcatalog.ManagedModel
			err = json.Unmarshal(r.Data, &v)
			b.ManagedModels = append(b.ManagedModels, v)
			if r.StaticInput {
				staticBundle.ManagedModels = append(staticBundle.ManagedModels, v)
			}
		case "extensions":
			if r.StaticInput {
				return fmt.Errorf("extensions are not supported in static config; use the Admin API or agwctl apply")
			}
			// Validate the stored envelope without starting linked factories or
			// resolving secrets; offline repair cannot change module configuration.
			_, err = schema.ExtensionSchema.Codec.Decode(r.Data)
		default:
			return fmt.Errorf("unsupported family %q", r.Family)
		}
		if err != nil {
			return fmt.Errorf("%s/%s: %w", r.Family, r.ID, err)
		}
	}
	if err := staticBundle.ValidateForStaticConfig(); err != nil {
		return fmt.Errorf("static config: %w", err)
	}
	// Bundle apply checks management references when their family is included.
	// Stored configurations may retain these declarations after deletion. Validate
	// the resource objects and Agent definitions in separate views to preserve
	// their shape checks (including builtin resource declarations) without applying
	// Bundle's stronger management-reference checks to persisted inventory.
	resourceView := b
	resourceView.Agents = nil
	if err := resourceView.ValidateForConfigStore(); err != nil {
		return err
	}
	agentView := b
	agentView.Providers, agentView.MCPServices, agentView.VirtualKeys = nil, nil, nil
	if err := agentView.ValidateForConfigStore(); err != nil {
		return err
	}
	check := func(owner, family, id string) error {
		if id == "" || !index[family][id] {
			return fmt.Errorf("%s references missing %s/%s", owner, family, id)
		}
		return nil
	}
	for _, cfg := range b.LLMRoutes {
		r, err := llmroute.NewLLMRouteConfigFromConfig(cfg)
		if err != nil {
			return err
		}
		for _, id := range r.TargetPolicy.ProviderIDs() {
			if err := check("route "+r.ID, "providers", id); err != nil {
				return err
			}
		}
	}
	for _, model := range b.ManagedModels {
		if err := check("managed model "+recordKey([]string{model.ProviderID, model.UpstreamModel}), "providers", model.ProviderID); err != nil {
			return err
		}
	}
	for _, r := range b.MCPRoutes {
		if err := check("route "+r.ID, "mcp_services", r.ServiceID); err != nil {
			return err
		}
	}
	for _, r := range b.AgentRoutes {
		if err := check("route "+r.ID, "agents", r.AgentID); err != nil {
			return err
		}
	}
	for _, a := range b.Agents {
		for _, id := range append(append([]string(nil), a.Routes.LLMRouteIDs...), a.Routes.MCPRouteIDs...) {
			if err := check("agent "+a.ID, "routes", id); err != nil {
				return err
			}
		}
		for _, id := range a.Routes.LLMRouteIDs {
			if routeKinds[id] != routecore.RouteKindLLM {
				return fmt.Errorf("agent %q: route %q must be llm", a.ID, id)
			}
		}
		for _, id := range a.Routes.MCPRouteIDs {
			if routeKinds[id] != routecore.RouteKindMCP {
				return fmt.Errorf("agent %q: route %q must be mcp", a.ID, id)
			}
		}
		// auth_ref is revocable: missing or rebound credentials make HTTP runtime
		// execution non-ready, without invalidating the retained Agent definition.
	}
	for _, key := range b.VirtualKeys {
		for _, id := range key.AllowedRouteIDs {
			if err := check("virtual key "+key.ID, "routes", id); err != nil {
				return err
			}
		}
	}
	return nil
}
