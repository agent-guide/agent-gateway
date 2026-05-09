package gatewaybundle

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	dispatcherpkg "github.com/agent-guide/caddy-agent-gateway/pkg/dispatcher"
	"github.com/agent-guide/caddy-agent-gateway/pkg/gateway/modelcatalog"
	routepkg "github.com/agent-guide/caddy-agent-gateway/pkg/gateway/route"
	virtualkeypkg "github.com/agent-guide/caddy-agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider"
	"gopkg.in/yaml.v3"
)

const (
	APIVersionV1Alpha1 = "gateway.agw/v1alpha1"
	KindGatewayBundle  = "GatewayBundle"
)

var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type GatewayBundle struct {
	APIVersion         string                      `json:"apiVersion"`
	Kind               string                      `json:"kind"`
	ProviderTypes      []ProviderTypeSetting       `json:"providerTypes,omitempty"`
	LLMAPIHandlerTypes []LLMAPIHandlerSetting      `json:"llmApiHandlerTypes,omitempty"`
	Providers          []provider.ProviderConfig   `json:"providers,omitempty"`
	ManagedModels      []modelcatalog.ManagedModel `json:"managedModels,omitempty"`
	Routes             []routepkg.AgentRoute       `json:"routes,omitempty"`
	VirtualKeys        []virtualkeypkg.VirtualKey  `json:"virtualKeys,omitempty"`
}

type ProviderTypeSetting struct {
	ProviderType string `json:"provider_type"`
	Enabled      bool   `json:"enabled"`
}

type LLMAPIHandlerSetting struct {
	LLMAPIHandlerType string `json:"llm_api_handler_type"`
	Enabled           bool   `json:"enabled"`
}

type ValidationErrors struct {
	Errors []error
}

func LoadFile(path string) (*GatewayBundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read gateway bundle file %q: %w", path, err)
	}
	return DecodeYAML(data)
}

func DecodeYAML(data []byte) (*GatewayBundle, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode gateway bundle yaml: %w", err)
	}

	expanded, err := expandEnvValue(normalizeYAMLValue(raw))
	if err != nil {
		return nil, err
	}

	jsonBytes, err := json.Marshal(expanded)
	if err != nil {
		return nil, fmt.Errorf("encode gateway bundle intermediate json: %w", err)
	}

	var bundle GatewayBundle
	if err := json.Unmarshal(jsonBytes, &bundle); err != nil {
		return nil, fmt.Errorf("decode gateway bundle: %w", err)
	}
	return &bundle, nil
}

func EncodeYAML(bundle *GatewayBundle) ([]byte, error) {
	if bundle == nil {
		return nil, fmt.Errorf("gateway bundle is required")
	}
	jsonBytes, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("encode gateway bundle json: %w", err)
	}
	var raw any
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("decode gateway bundle json: %w", err)
	}
	yamlBytes, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode gateway bundle yaml: %w", err)
	}
	return yamlBytes, nil
}

func (b *GatewayBundle) Validate() error {
	if b == nil {
		return fmt.Errorf("gateway bundle is required")
	}

	errs := &ValidationErrors{}
	if strings.TrimSpace(b.APIVersion) == "" {
		errs.Append(fmt.Errorf("apiVersion is required"))
	} else if b.APIVersion != APIVersionV1Alpha1 {
		errs.Append(fmt.Errorf("apiVersion must be %q", APIVersionV1Alpha1))
	}
	if strings.TrimSpace(b.Kind) == "" {
		errs.Append(fmt.Errorf("kind is required"))
	} else if b.Kind != KindGatewayBundle {
		errs.Append(fmt.Errorf("kind must be %q", KindGatewayBundle))
	}

	providerIDs := map[string]struct{}{}
	routeIDs := map[string]struct{}{}
	for _, item := range b.ProviderTypes {
		item.ProviderType = strings.ToLower(strings.TrimSpace(item.ProviderType))
		if item.ProviderType == "" {
			errs.Append(fmt.Errorf("providerTypes[].provider_type is required"))
			continue
		}
		if _, ok := provider.IsProviderTypeEnabled(item.ProviderType); !ok {
			errs.Append(fmt.Errorf("providerTypes[%q]: unknown provider_type", item.ProviderType))
		}
	}
	for _, item := range b.LLMAPIHandlerTypes {
		item.LLMAPIHandlerType = strings.ToLower(strings.TrimSpace(item.LLMAPIHandlerType))
		if item.LLMAPIHandlerType == "" {
			errs.Append(fmt.Errorf("llmApiHandlerTypes[].llm_api_handler_type is required"))
			continue
		}
		if _, ok := dispatcherpkg.IsLLMApiHandlerTypeEnabled(item.LLMAPIHandlerType); !ok {
			errs.Append(fmt.Errorf("llmApiHandlerTypes[%q]: unknown llm_api_handler_type", item.LLMAPIHandlerType))
		}
	}
	for i := range b.Providers {
		b.Providers[i] = provider.NormalizeConfig(b.Providers[i], b.Providers[i].Id, b.Providers[i].ProviderType)
		id := strings.TrimSpace(b.Providers[i].Id)
		if id == "" {
			errs.Append(fmt.Errorf("providers[%d].id is required", i))
			continue
		}
		if _, exists := providerIDs[id]; exists {
			errs.Append(fmt.Errorf("providers[%q]: duplicate id", id))
		} else {
			providerIDs[id] = struct{}{}
		}
		if strings.TrimSpace(b.Providers[i].ProviderType) == "" {
			errs.Append(fmt.Errorf("providers[%q].provider_type is required", id))
			continue
		}
		if _, ok := provider.IsProviderTypeEnabled(b.Providers[i].ProviderType); !ok {
			errs.Append(fmt.Errorf("providers[%q]: unknown provider_type %q", id, b.Providers[i].ProviderType))
		}
	}
	managedKeys := map[string]struct{}{}
	for i := range b.ManagedModels {
		b.ManagedModels[i].Normalize()
		providerID := strings.TrimSpace(b.ManagedModels[i].ProviderID)
		upstreamModel := strings.TrimSpace(b.ManagedModels[i].UpstreamModel)
		if providerID == "" || upstreamModel == "" {
			errs.Append(fmt.Errorf("managedModels[%d]: provider_id and upstream_model are required", i))
			continue
		}
		key := providerID + "/" + upstreamModel
		if _, exists := managedKeys[key]; exists {
			errs.Append(fmt.Errorf("managedModels[%q]: duplicate provider_id/upstream_model", key))
		} else {
			managedKeys[key] = struct{}{}
		}
	}
	for i := range b.Routes {
		b.Routes[i].Normalize()
		routeID := strings.TrimSpace(b.Routes[i].ID)
		if routeID == "" {
			errs.Append(fmt.Errorf("routes[%d].id is required", i))
			continue
		}
		if _, exists := routeIDs[routeID]; exists {
			errs.Append(fmt.Errorf("routes[%q]: duplicate id", routeID))
		} else {
			routeIDs[routeID] = struct{}{}
		}
		if err := b.Routes[i].ValidateDefinition(); err != nil {
			errs.Append(fmt.Errorf("routes[%q]: %w", routeID, err))
		}
	}
	virtualKeys := map[string]struct{}{}
	for i := range b.VirtualKeys {
		key := strings.TrimSpace(b.VirtualKeys[i].Key)
		if key == "" {
			errs.Append(fmt.Errorf("virtualKeys[%d].key is required", i))
			continue
		}
		if _, exists := virtualKeys[key]; exists {
			errs.Append(fmt.Errorf("virtualKeys[%q]: duplicate key", key))
		} else {
			virtualKeys[key] = struct{}{}
		}
		for _, routeID := range b.VirtualKeys[i].AllowedRouteIDs {
			trimmedRouteID := strings.TrimSpace(routeID)
			if trimmedRouteID == "" {
				errs.Append(fmt.Errorf("virtualKeys[%q]: allowed_route_ids entries must not be empty", key))
				continue
			}
			if len(routeIDs) > 0 {
				if _, ok := routeIDs[trimmedRouteID]; !ok {
					errs.Append(fmt.Errorf("virtualKeys[%q]: allowed_route_id %q does not exist in bundle routes", key, trimmedRouteID))
				}
			}
		}
	}

	if errs.HasErrors() {
		return errs
	}
	return nil
}

func (e *ValidationErrors) Append(err error) {
	if e == nil || err == nil {
		return
	}
	e.Errors = append(e.Errors, err)
}

func (e *ValidationErrors) HasErrors() bool {
	return e != nil && len(e.Errors) > 0
}

func (e *ValidationErrors) Error() string {
	if e == nil || len(e.Errors) == 0 {
		return ""
	}
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}
	parts := make([]string, 0, len(e.Errors))
	for _, err := range e.Errors {
		parts = append(parts, err.Error())
	}
	sort.Strings(parts)
	return fmt.Sprintf("gateway bundle validation failed (%d errors): %s", len(parts), strings.Join(parts, "; "))
}

func normalizeYAMLValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			out[key] = normalizeYAMLValue(child)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			out[fmt.Sprint(key)] = normalizeYAMLValue(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(value))
		for _, child := range value {
			out = append(out, normalizeYAMLValue(child))
		}
		return out
	default:
		return value
	}
}

func expandEnvValue(v any) (any, error) {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			expanded, err := expandEnvValue(child)
			if err != nil {
				return nil, err
			}
			out[key] = expanded
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(value))
		for _, child := range value {
			expanded, err := expandEnvValue(child)
			if err != nil {
				return nil, err
			}
			out = append(out, expanded)
		}
		return out, nil
	case string:
		return expandEnvString(value)
	default:
		return value, nil
	}
}

func expandEnvString(s string) (string, error) {
	matches := envVarPattern.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return s, nil
	}

	values := map[string]string{}
	for _, match := range matches {
		name := match[1]
		if _, ok := values[name]; ok {
			continue
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("expand env in gateway bundle: environment variable %q is not set", name)
		}
		values[name] = value
	}

	return envVarPattern.ReplaceAllStringFunc(s, func(token string) string {
		match := envVarPattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		return values[match[1]]
	}), nil
}
