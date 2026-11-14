package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	_ "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
	"github.com/agent-guide/agent-gateway/pkg/gateway/agentroute"
	llmroutepkg "github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	virtualkeypkg "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/deepseek"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/zhipu"
)

func TestDecodeYAML(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")

	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
    api_key: ${OPENAI_API_KEY}
    default_model: gpt-4.1
providerModels:
  - provider_id: openai-main
    upstream_model: gpt-4.1
    enabled: true
llmRoutes:
  - id: chat-prod
    protocol: openai
    match_policy:
      path_prefix: /
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      type: models
      default_model: gpt-4.1
      model_targets:
        - name: gpt-4.1
          kind: provider-model
          provider_id: openai-main
          upstream_model: gpt-4.1
virtualKeys:
  - id: vk-local-test
    tag: local-test
    allowed_route_ids:
      - chat-prod
    rate_limits:
      llm:
        requests_per_minute: 60
        burst: 10
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	if bundle.APIVersion != APIVersionV1Alpha1 {
		t.Fatalf("APIVersion = %q, want %q", bundle.APIVersion, APIVersionV1Alpha1)
	}
	if bundle.Kind != KindGatewayBundle {
		t.Fatalf("Kind = %q, want %q", bundle.Kind, KindGatewayBundle)
	}
	if len(bundle.Providers) != 1 {
		t.Fatalf("len(Providers) = %d, want 1", len(bundle.Providers))
	}
	if bundle.Providers[0].APIKey != "sk-test" {
		t.Fatalf("Providers[0].APIKey = %q, want %q", bundle.Providers[0].APIKey, "sk-test")
	}
	if len(bundle.ProviderModels) != 1 || bundle.ProviderModels[0].ProviderID != "openai-main" || bundle.ProviderModels[0].UpstreamModel != "gpt-4.1" {
		t.Fatalf("ProviderModels = %#v", bundle.ProviderModels)
	}
	if len(bundle.LLMRoutes) != 1 {
		t.Fatalf("len(LLMRoutes) = %d, want 1", len(bundle.LLMRoutes))
	}
	route, err := llmroutepkg.NewLLMRouteFromConfig(bundle.LLMRoutes[0])
	if err != nil {
		t.Fatalf("NewLLMRouteFromConfig() error = %v", err)
	}
	directPolicy := route.TargetPolicy
	if directPolicy == nil || directPolicy.ModelTargets[0].ProviderID != "openai-main" {
		t.Fatalf("LLMRoutes[0].TargetPolicy = %#v, want direct provider openai-main", route.TargetPolicy)
	}
	if len(bundle.VirtualKeys) != 1 || bundle.VirtualKeys[0].ID != "vk-local-test" || len(bundle.VirtualKeys[0].AllowedRouteIDs) != 1 || bundle.VirtualKeys[0].AllowedRouteIDs[0] != "chat-prod" {
		t.Fatalf("VirtualKeys = %#v", bundle.VirtualKeys)
	}
	if bundle.VirtualKeys[0].RateLimits == nil || bundle.VirtualKeys[0].RateLimits.LLM == nil ||
		bundle.VirtualKeys[0].RateLimits.LLM.RequestsPerMinute != 60 || bundle.VirtualKeys[0].RateLimits.LLM.Burst != 10 {
		t.Fatalf("VirtualKeys[0].RateLimits = %#v", bundle.VirtualKeys[0].RateLimits)
	}
}

func TestVirtualKeyRateLimitsStrictDecodeAndValidation(t *testing.T) {
	for _, input := range []string{
		`apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
virtualKeys:
  - id: demo
    rate_limits:
      lmm:
        requests_per_minute: 60
        burst: 1
`,
		`apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
virtualKeys:
  - id: demo
    rate_limits:
      llm:
        requests_per_minute: 60
        brust: 1
`,
	} {
		if _, err := DecodeYAML([]byte(input)); err == nil {
			t.Fatalf("DecodeYAML(%q) returned nil error", input)
		}
	}

	bundle, err := DecodeYAML([]byte(`apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
virtualKeys:
  - id: demo
    rate_limits:
      agent:
        requests_per_minute: 0
        burst: 1
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.ValidateForConfigStore(); err == nil || !strings.Contains(err.Error(), "requests_per_minute must be greater than zero") {
		t.Fatalf("ValidateForConfigStore() error = %v", err)
	}
}

func TestGatewayBundleOmitsEmptyVirtualKeyRateLimits(t *testing.T) {
	bundle := &GatewayBundle{
		APIVersion:  APIVersionV1Alpha1,
		Kind:        KindGatewayBundle,
		VirtualKeys: []BundleVirtualKey{{ID: "demo"}},
	}
	encoded, err := EncodeYAML(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "rate_limits") {
		t.Fatalf("encoded bundle unexpectedly contains rate_limits:\n%s", encoded)
	}
}

// TestVirtualKeyRateLimitsRoundTripPreservesCompletePolicy covers the §14
// requirement that the bundle <-> runtime conversion and YAML round trip
// preserve the complete rate_limits policy across all dimensions, and that the
// converted policy is isolated (mutating the runtime copy does not touch the
// bundle copy or vice versa).
func TestVirtualKeyRateLimitsRoundTripPreservesCompletePolicy(t *testing.T) {
	bundleKey := BundleVirtualKey{
		ID: "demo",
		RateLimits: &virtualkeypkg.VirtualKeyRateLimits{
			LLM:   &virtualkeypkg.RateLimit{RequestsPerMinute: 60, Burst: 10},
			MCP:   &virtualkeypkg.RateLimit{RequestsPerMinute: 120, Burst: 20},
			Agent: &virtualkeypkg.RateLimit{RequestsPerMinute: 20, Burst: 5},
		},
	}

	// Bundle -> runtime, then mutate the runtime copy: the bundle's policy
	// pointers must not move.
	runtimeKey := bundleKey.ToRuntimeVirtualKey("generated-secret")
	runtimeKey.RateLimits.LLM.RequestsPerMinute = 999
	if bundleKey.RateLimits.LLM.RequestsPerMinute != 60 {
		t.Fatalf("bundle policy aliased runtime mutation: llm = %d", bundleKey.RateLimits.LLM.RequestsPerMinute)
	}

	// Runtime -> bundle, mutate the bundle copy: the runtime policy must not move.
	back := BundleVirtualKeyFromRuntime(runtimeKey)
	if back.RateLimits == nil {
		t.Fatal("BundleVirtualKeyFromRuntime dropped rate_limits")
	}
	back.RateLimits.MCP.Burst = 888
	if runtimeKey.RateLimits.MCP.Burst != 20 {
		t.Fatalf("runtime policy aliased bundle mutation: mcp burst = %d", runtimeKey.RateLimits.MCP.Burst)
	}

	// Full YAML round trip preserves every dimension and value.
	full := &GatewayBundle{
		APIVersion: APIVersionV1Alpha1,
		Kind:       KindGatewayBundle,
		VirtualKeys: []BundleVirtualKey{{
			ID: "demo",
			RateLimits: &virtualkeypkg.VirtualKeyRateLimits{
				LLM:   &virtualkeypkg.RateLimit{RequestsPerMinute: 60, Burst: 10},
				MCP:   &virtualkeypkg.RateLimit{RequestsPerMinute: 120, Burst: 20},
				Agent: &virtualkeypkg.RateLimit{RequestsPerMinute: 20, Burst: 5},
			},
		}},
	}
	encoded, err := EncodeYAML(full)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeYAML(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decodedRT := decoded.VirtualKeys[0].ToRuntimeVirtualKey("")
	checkRateLimit := func(name string, got *virtualkeypkg.RateLimit, wantRPM, wantBurst int) {
		if got == nil {
			t.Fatalf("%s rate limit missing after round trip", name)
		}
		if got.RequestsPerMinute != wantRPM || got.Burst != wantBurst {
			t.Fatalf("%s rate limit = {rpm:%d burst:%d}, want {rpm:%d burst:%d}", name, got.RequestsPerMinute, got.Burst, wantRPM, wantBurst)
		}
	}
	checkRateLimit("llm", decodedRT.RateLimits.LLM, 60, 10)
	checkRateLimit("mcp", decodedRT.RateLimits.MCP, 120, 20)
	checkRateLimit("agent", decodedRT.RateLimits.Agent, 20, 5)

	// A bundle carrying a complete policy must validate cleanly.
	if err := decoded.ValidateForConfigStore(); err != nil {
		t.Fatalf("round-tripped bundle failed validation: %v", err)
	}
}

func TestDecodeYAMLMissingEnv(t *testing.T) {
	const envName = "AGW_GATEWAYBUNDLE_TEST_MISSING_ENV"
	if value, ok := os.LookupEnv(envName); ok {
		t.Cleanup(func() {
			_ = os.Setenv(envName, value)
		})
	} else {
		t.Cleanup(func() {
			_ = os.Unsetenv(envName)
		})
	}
	_ = os.Unsetenv(envName)

	_, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
    api_key: ${AGW_GATEWAYBUNDLE_TEST_MISSING_ENV}
`))
	if err == nil {
		t.Fatal("DecodeYAML() error = nil, want missing env error")
	}
	if !strings.Contains(err.Error(), `environment variable "AGW_GATEWAYBUNDLE_TEST_MISSING_ENV" is not set`) {
		t.Fatalf("DecodeYAML() error = %v", err)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	if err := os.WriteFile(path, []byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bundle, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if bundle.APIVersion != APIVersionV1Alpha1 || bundle.Kind != KindGatewayBundle {
		t.Fatalf("LoadFile() bundle = %#v", bundle)
	}
}

func TestEncodeYAMLRoundTrip(t *testing.T) {
	original := &GatewayBundle{
		APIVersion: APIVersionV1Alpha1,
		Kind:       KindGatewayBundle,
		Providers:  []provider.ProviderConfig{{Id: "openai-main", ProviderType: "openai"}},
	}

	data, err := EncodeYAML(original)
	if err != nil {
		t.Fatalf("EncodeYAML() error = %v", err)
	}
	decoded, err := DecodeYAML(data)
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}
	if decoded.APIVersion != original.APIVersion || decoded.Kind != original.Kind {
		t.Fatalf("round-trip bundle = %#v", decoded)
	}
	if len(decoded.Providers) != 1 || decoded.Providers[0].Id != "openai-main" || decoded.Providers[0].ProviderType != "openai" {
		t.Fatalf("round-trip providers = %#v", decoded.Providers)
	}
}

func TestEncodeYAMLOmitsServerManagedTimestamps(t *testing.T) {
	now := time.Now().UTC()
	bundle := &GatewayBundle{
		APIVersion: APIVersionV1Alpha1,
		Kind:       KindGatewayBundle,
		AgentRoutes: []agentroute.AgentRouteConfig{{
			RouteMetadata: agentroute.RouteMetadata{
				ID: "agent:a:a", MatchPolicy: agentroute.RouteMatch{PathPrefix: "/a"},
				CreatedAt: now, UpdatedAt: now,
			},
			AgentID: "a",
		}},
	}
	data, err := EncodeYAML(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "created_at") || strings.Contains(string(data), "updated_at") {
		t.Fatalf("encoded declarative bundle retained managed timestamps:\n%s", data)
	}
}

func TestEncodeYAMLPreservesNestedTimestampNamedConfig(t *testing.T) {
	bundle := &GatewayBundle{
		APIVersion: APIVersionV1Alpha1,
		Kind:       KindGatewayBundle,
		Agents: []agentpkg.Agent{{
			ID: "a", Name: "A",
			Runtime: agentpkg.Runtime{Type: agentpkg.RuntimeTypeACP, ACP: &agentpkg.ACPRuntime{
				AgentType: "codex", CWD: "/work",
				ConfigOverrides: map[string]string{"updated_at": "runtime-value"},
			}},
		}},
	}
	data, err := EncodeYAML(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "updated_at: runtime-value") {
		t.Fatalf("nested runtime config was stripped:\n%s", data)
	}
}

func TestDecodeYAMLRejectsProviderTypes(t *testing.T) {
	_, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providerTypes:
  - provider_type: openai
    enabled: true
`))
	if err == nil {
		t.Fatal("DecodeYAML() error = nil, want providerTypes rejection")
	}
	if !strings.Contains(err.Error(), "providerTypes is not supported in GatewayBundle") {
		t.Fatalf("DecodeYAML() error = %v", err)
	}
}

func TestDecodeYAMLRejectsCLIAuthAuthenticators(t *testing.T) {
	_, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
cliAuthAuthenticators:
  - name: codex
    enabled: true
`))
	if err == nil || !strings.Contains(err.Error(), "use the external agw-auth tool") {
		t.Fatalf("DecodeYAML() error = %v, want agw-auth migration guidance", err)
	}
}

func TestValidate(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
llmRoutes:
  - id: chat-prod
    protocol: openai
    match_policy:
      path_prefix: /
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      type: models
      default_model: gpt-4.1
      model_targets:
        - name: gpt-4.1
          kind: provider-model
          provider_id: openai-main
          upstream_model: gpt-4.1
virtualKeys:
  - id: vk-local-test
    allowed_route_ids:
      - chat-prod
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	if err := bundle.ValidateForConfigStore(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAgentsRejectsDuplicateMCPRouteBinding(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
agents:
  - id: agent-a
    name: Agent A
    runtime:
      type: http
      http:
        card_url: https://agent-a.example/.well-known/agent-card.json
        protocol: a2a
    routes:
      mcp_route_ids:
        - shared-route
  - id: agent-b
    name: Agent B
    runtime:
      type: http
      http:
        card_url: https://agent-b.example/.well-known/agent-card.json
        protocol: a2a
    routes:
      mcp_route_ids:
        - shared-route
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}
	err = bundle.ValidateForConfigStore()
	if err == nil {
		t.Fatalf("expected duplicate route binding to be rejected")
	}
	if !strings.Contains(err.Error(), "shared-route") {
		t.Fatalf("error should name the duplicate route, got: %v", err)
	}
}

func TestValidateAgentsRejectsDanglingResourceRefs(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
agents:
  - id: agent-a
    name: Agent A
    runtime:
      type: http
      http:
        card_url: https://agent-a.example/.well-known/agent-card.json
        protocol: a2a
    resources:
      provider_ids:
        - openai-main
        - ghost-provider
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}
	err = bundle.ValidateForConfigStore()
	if err == nil {
		t.Fatalf("expected dangling provider reference to be rejected")
	}
	if !strings.Contains(err.Error(), "ghost-provider") {
		t.Fatalf("error should name the dangling provider, got: %v", err)
	}
}

func TestValidateAggregatesErrors(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: wrong
kind: WrongKind
providers:
  - id: dup
    provider_type: openai
  - id: dup
    provider_type: missing-provider
providerModels:
  - provider_id: openai-main
    upstream_model: gpt-4.1
  - provider_id: openai-main
    upstream_model: gpt-4.1
llmRoutes:
  - id: route-a
    protocol: openai
    match_policy:
      path_prefix: /
    auth_policy:
      require_virtual_key: true
    target_policy:
      type: models
      default_model: gpt-4.1
      model_targets:
        - name: gpt-4.1
          kind: provider-model
          provider_id: dup
          upstream_model: gpt-4.1
  - id: route-a
    protocol: openai
    match_policy:
      path_prefix: /v2
    auth_policy:
      require_virtual_key: true
    target_policy:
      type: models
      default_model: gpt-4.1
      model_targets:
        - name: gpt-4.1
          kind: provider-model
          provider_id: dup
          upstream_model: gpt-4.1
virtualKeys:
  - id: vk-a
    allowed_route_ids:
      - missing-route
  - id: vk-a
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	err = bundle.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want aggregated validation errors")
	}
	validationErrs, ok := err.(*ValidationErrors)
	if !ok {
		t.Fatalf("Validate() error type = %T, want *ValidationErrors", err)
	}
	if len(validationErrs.Errors) < 6 {
		t.Fatalf("len(ValidationErrors.Errors) = %d, want >= 6; err = %v", len(validationErrs.Errors), err)
	}
	for _, want := range []string{
		`apiVersion must be "gateway.agw/v1alpha1"`,
		`kind must be "GatewayBundle"`,
		`providers["dup"]: duplicate id`,
		`providerModels["openai-main/gpt-4.1"]: duplicate provider_id/upstream_model`,
		`llmRoutes["route-a"]: duplicate id`,
		`virtualKeys["vk-a"]: allowed_route_id "missing-route" does not exist in bundle routes`,
		`virtualKeys["vk-a"]: duplicate id`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Validate() error = %v, want substring %q", err, want)
		}
	}
}

func TestValidateForConfigStoreAcceptsGeneratedVirtualKeys(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
virtualKeys:
  - id: demo
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	err = bundle.ValidateForConfigStore()
	if err != nil {
		t.Fatalf("ValidateForConfigStore() error = %v", err)
	}
}

func TestValidateForStaticConfigRejectsVirtualKeys(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
virtualKeys:
  - id: demo
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	err = bundle.ValidateForStaticConfig()
	if err == nil {
		t.Fatal("ValidateForStaticConfig() error = nil, want virtual key rejection")
	}
	if !strings.Contains(err.Error(), "virtualKeys are not supported in static config") {
		t.Fatalf("ValidateForStaticConfig() error = %v", err)
	}
}

func TestValidateForStaticConfigAcceptsProviderModels(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providerModels:
  - provider_id: openai-main
    upstream_model: gpt-4.1
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	if err := bundle.ValidateForStaticConfig(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsCrossKindMatchTies(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
llmRoutes:
  - id: chat
    kind: llm
    protocol: openai
    match_policy: {path_prefix: /api, methods: [GET, POST]}
    target_policy: {type: models, model_targets: [{name: m, kind: provider-model, provider_id: p, upstream_model: m}]}
mcpRoutes:
  - id: tools
    kind: mcp
    disabled: true
    connector_id: s
    match_policy: {path_prefix: /api, methods: [POST]}
`))
	if err != nil {
		t.Fatal(err)
	}
	err = bundle.Validate()
	if err == nil || !strings.Contains(err.Error(), `ambiguous routes "chat" and "tools"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestBundleRejectsUnsupportedBudgetForDynamicAndStatic(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
agents:
  - id: budget-agent
    name: Budget Agent
    disabled: true
    runtime:
      type: http
      http: {card_url: 'https://example.org/card', protocol: a2a}
    policy: {budget: {}}
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, validate := range []func() error{bundle.ValidateForConfigStore, bundle.ValidateForStaticConfig} {
		if err := validate(); err == nil || !strings.Contains(err.Error(), "policy.budget is unsupported") {
			t.Fatalf("validation = %v", err)
		}
	}
}

func TestValidateRejectsConflictingRouteDefaults(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: zhipu-main
    provider_type: zhipu
  - id: deepseek-main
    provider_type: deepseek
llmRoutes:
  - id: chat-test
    protocol: openai
    match_policy:
      path_prefix: /chat
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      default_model: chat-default
      model_targets:
        - name: code-default
          candidates:
            - provider_id: zhipu-main
              upstream_model: glm-4.7
              default: true
        - name: chat-default
          candidates:
            - provider_id: deepseek-main
              upstream_model: deepseek-v4-pro
              default: true
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	err = bundle.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want conflicting route defaults rejection")
	}
	if !strings.Contains(err.Error(), `unsupported model target field "candidates"`) {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDecodeYAMLWithConnectorsAndRoutes(t *testing.T) {
	t.Setenv("MY_MCP_API_KEY", "secret-key")

	bundle, err := DecodeYAML([]byte(`apiVersion: gateway.agw/v1alpha1
connectors:
    - id: my-mcp-svc
      mcp:
        auth:
            api_key: ${MY_MCP_API_KEY}
            type: api_key
        transport: streamable_http
        url: https://example.com/mcp
      name: My MCP Service
      type: mcp
kind: GatewayBundle
mcpRoutes:
    - auth_policy:
        require_virtual_key: true
      connector_id: my-mcp-svc
      match_policy:
        path_prefix: /mcp
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	if len(bundle.Connectors) != 1 {
		t.Fatalf("len(Connectors) = %d, want 1", len(bundle.Connectors))
	}
	if bundle.Connectors[0].ID != "my-mcp-svc" {
		t.Fatalf("Connectors[0].ID = %q, want my-mcp-svc", bundle.Connectors[0].ID)
	}
	if bundle.Connectors[0].MCP.AuthConfig == nil || bundle.Connectors[0].MCP.AuthConfig.APIKey != "secret-key" {
		t.Fatalf("Connectors[0].AuthConfig = %#v, want api_key=secret-key", bundle.Connectors[0].MCP.AuthConfig)
	}
	if len(bundle.MCPRoutes) != 1 {
		t.Fatalf("len(MCPRoutes) = %d, want 1", len(bundle.MCPRoutes))
	}
	if bundle.MCPRoutes[0].ConnectorID != "my-mcp-svc" {
		t.Fatalf("MCPRoutes[0].ConnectorID = %q, want my-mcp-svc", bundle.MCPRoutes[0].ConnectorID)
	}
	if err := bundle.ValidateForConfigStore(); err != nil {
		t.Fatalf("ValidateForConfigStore() error = %v", err)
	}
}

func TestValidateConnectorRequiresID(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`apiVersion: gateway.agw/v1alpha1
connectors:
    - mcp:
        transport: streamable_http
        url: https://example.com/mcp
      name: Missing ID Service
      type: mcp
kind: GatewayBundle
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	err = bundle.ValidateForConfigStore()
	if err == nil {
		t.Fatal("ValidateForConfigStore() error = nil, want missing id error")
	}
	if !strings.Contains(err.Error(), "connectors[0].id is required") {
		t.Fatalf("ValidateForConfigStore() error = %v, want id required", err)
	}
}

func TestValidateMCPRouteRequiresServiceID(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
mcpRoutes:
  - id: mcp-svc-root
    kind: mcp
    match_policy:
      path_prefix: /
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	err = bundle.ValidateForConfigStore()
	if err == nil {
		t.Fatal("ValidateForConfigStore() error = nil, want connector_id required error")
	}
	if !strings.Contains(err.Error(), "connector_id is required") {
		t.Fatalf("ValidateForConfigStore() error = %v, want connector_id required", err)
	}
}

func TestValidateForStaticConfigAcceptsModelRoutes(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
llmRoutes:
  - id: chat-test
    protocol: openai
    match_policy:
      path_prefix: /chat
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      default_model: chat-default
      model_targets:
        - name: chat-default
          kind: logical-model
          logical_model_id: chat-default
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	if err := bundle.ValidateForStaticConfig(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateUnifiedAgentRouteAndInlineACPConfig(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
agents:
  - id: reviewer
    name: Reviewer
    runtime:
      type: acp
      acp:
        agent_type: codex
        cwd: /workspace
        allowed_roots: [/workspace]
        permission_mode: deny
    routes: {}
    resources: {}
    policy: {}
    disabled: false
agentRoutes:
  - agent_id: reviewer
    match_policy: {path_prefix: /agents/reviewer}
    auth_policy: {require_virtual_key: true}
virtualKeys:
  - id: reviewer-key
    allowed_route_ids: [agent:reviewer:agents-reviewer]
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.ValidateForConfigStore(); err != nil {
		t.Fatal(err)
	}
	if got := bundle.AgentRoutes[0].ID; got != "agent:reviewer:agents-reviewer" {
		t.Fatalf("route id = %q", got)
	}
}

func TestValidateNativeA2AAgentRoute(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
agents:
  - id: remote
    name: Remote
    runtime:
      type: http
      http:
        card_url: https://remote.example/.well-known/agent-card.json
        protocol: a2a
    routes: {}
    resources: {}
    policy: {}
agentRoutes:
  - protocol: a2a
    agent_id: remote
    match_policy:
      host: gateway.example
      path_prefix: /agents/remote
      methods: [GET, POST]
    auth_policy: {require_virtual_key: false}
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.ValidateForConfigStore(); err != nil {
		t.Fatal(err)
	}
	if bundle.AgentRoutes[0].Protocol != agentroute.RouteProtocolA2A {
		t.Fatalf("protocol = %q", bundle.AgentRoutes[0].Protocol)
	}

	bundle.AgentRoutes[0].MatchPolicy.Host = ""
	if err := bundle.ValidateForConfigStore(); err == nil || !strings.Contains(err.Error(), "requires match_policy.host") {
		t.Fatalf("missing-host validation error = %v", err)
	}
}

func TestDecodeRejectsLegacyAgentRuntimeBundle(t *testing.T) {
	_, err := DecodeYAML([]byte("apiVersion: gateway.agw/v1alpha1\nkind: GatewayBundle\nacpServices: []\n"))
	if err == nil || !strings.Contains(err.Error(), "legacy_agent_runtime_config") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateAgentsAllowsSharedLLMRoutes(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
agents:
  - id: agent-a
    name: Agent A
    runtime:
      type: builtin
      builtin:
        model:
          llm_route_id: shared-route
    routes:
      llm_route_ids: [shared-route]
  - id: agent-b
    name: Agent B
    runtime:
      type: builtin
      builtin:
        model:
          llm_route_id: shared-route
    routes:
      llm_route_ids: [shared-route]
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.ValidateForConfigStore(); err != nil {
		t.Fatal(err)
	}
}
