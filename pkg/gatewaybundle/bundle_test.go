package gatewaybundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/agent-guide/agent-gateway/pkg/cliauth/authenticator"
	_ "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
)

func TestDecodeYAML(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")

	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providerTypes:
  - provider_type: openai
    enabled: true
providers:
  - id: openai-main
    provider_type: openai
    api_key: ${OPENAI_API_KEY}
    default_model: gpt-4.1
managedModels:
  - provider_id: openai-main
    upstream_model: gpt-4.1
    enabled: true
routes:
  - id: chat-prod
    llm_api: openai
    match:
      path_prefix: /
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      provider_target:
        provider_id: openai-main
virtualKeys:
  - id: vk-local-test
    tag: local-test
    allowed_route_ids:
      - chat-prod
cliAuthAuthenticators:
  - name: codex
    enabled: true
    config:
      callback_port: 9002
      no_browser: true
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
	if len(bundle.ProviderTypes) != 1 || bundle.ProviderTypes[0].ProviderType != "openai" || !bundle.ProviderTypes[0].Enabled {
		t.Fatalf("ProviderTypes = %#v", bundle.ProviderTypes)
	}
	if len(bundle.Providers) != 1 {
		t.Fatalf("len(Providers) = %d, want 1", len(bundle.Providers))
	}
	if bundle.Providers[0].APIKey != "sk-test" {
		t.Fatalf("Providers[0].APIKey = %q, want %q", bundle.Providers[0].APIKey, "sk-test")
	}
	if len(bundle.ManagedModels) != 1 || bundle.ManagedModels[0].ProviderID != "openai-main" || bundle.ManagedModels[0].UpstreamModel != "gpt-4.1" {
		t.Fatalf("ManagedModels = %#v", bundle.ManagedModels)
	}
	if len(bundle.Routes) != 1 {
		t.Fatalf("len(Routes) = %d, want 1", len(bundle.Routes))
	}
	if bundle.Routes[0].TargetPolicy.ProviderTarget.ProviderID != "openai-main" {
		t.Fatalf("Routes[0].TargetPolicy.ProviderTarget.ProviderID = %q, want %q", bundle.Routes[0].TargetPolicy.ProviderTarget.ProviderID, "openai-main")
	}
	if len(bundle.VirtualKeys) != 1 || bundle.VirtualKeys[0].ID != "vk-local-test" || len(bundle.VirtualKeys[0].AllowedRouteIDs) != 1 || bundle.VirtualKeys[0].AllowedRouteIDs[0] != "chat-prod" {
		t.Fatalf("VirtualKeys = %#v", bundle.VirtualKeys)
	}
	if len(bundle.CLIAuthAuthenticators) != 1 || bundle.CLIAuthAuthenticators[0].Name != "codex" || !bundle.CLIAuthAuthenticators[0].Enabled || bundle.CLIAuthAuthenticators[0].Config.CallbackPort != 9002 {
		t.Fatalf("CLIAuthAuthenticators = %#v", bundle.CLIAuthAuthenticators)
	}
}

func TestDecodeYAMLMissingEnv(t *testing.T) {
	_, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
    api_key: ${OPENAI_API_KEY}
`))
	if err == nil {
		t.Fatal("DecodeYAML() error = nil, want missing env error")
	}
	if !strings.Contains(err.Error(), `environment variable "OPENAI_API_KEY" is not set`) {
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
		ProviderTypes: []ProviderTypeSetting{
			{ProviderType: "openai", Enabled: true},
		},
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
	if len(decoded.ProviderTypes) != 1 || decoded.ProviderTypes[0].ProviderType != "openai" || !decoded.ProviderTypes[0].Enabled {
		t.Fatalf("round-trip provider types = %#v", decoded.ProviderTypes)
	}
}

func TestValidate(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providerTypes:
  - provider_type: openai
    enabled: true
providers:
  - id: openai-main
    provider_type: openai
routes:
  - id: chat-prod
    llm_api: openai
    match:
      path_prefix: /
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      provider_target:
        provider_id: openai-main
virtualKeys:
  - id: vk-local-test
    allowed_route_ids:
      - chat-prod
cliAuthAuthenticators:
  - name: codex
    enabled: true
`))
	if err != nil {
		t.Fatalf("DecodeYAML() error = %v", err)
	}

	if err := bundle.ValidateForConfigStore(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAggregatesErrors(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: wrong
kind: WrongKind
providerTypes:
  - provider_type: missing-provider
providers:
  - id: dup
    provider_type: openai
  - id: dup
    provider_type: missing-provider
managedModels:
  - provider_id: openai-main
    upstream_model: gpt-4.1
  - provider_id: openai-main
    upstream_model: gpt-4.1
routes:
  - id: route-a
    llm_api: openai
    match:
      path_prefix: /
    auth_policy:
      require_virtual_key: true
    target_policy:
      provider_target:
        provider_id: dup
  - id: route-a
    llm_api: openai
    match:
      path_prefix: /v2
    auth_policy:
      require_virtual_key: true
    target_policy:
      provider_target:
        provider_id: dup
virtualKeys:
  - id: vk-a
    allowed_route_ids:
      - missing-route
  - id: vk-a
cliAuthAuthenticators:
  - name: codex
  - name: codex
  - name: missing-authenticator
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
		`providerTypes["missing-provider"]: unknown provider_type`,
		`providers["dup"]: duplicate id`,
		`managedModels["openai-main/gpt-4.1"]: duplicate provider_id/upstream_model`,
		`routes["route-a"]: duplicate id`,
		`virtualKeys["vk-a"]: allowed_route_id "missing-route" does not exist in bundle routes`,
		`virtualKeys["vk-a"]: duplicate id`,
		`cliAuthAuthenticators["codex"]: duplicate name`,
		`cliAuthAuthenticators["missing-authenticator"]: unknown authenticator`,
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

func TestValidateRejectsConflictingRouteDefaults(t *testing.T) {
	bundle, err := DecodeYAML([]byte(`
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providerTypes:
  - provider_type: zhipu
    enabled: true
  - provider_type: deepseek
    enabled: true
providers:
  - id: zhipu-main
    provider_type: zhipu
  - id: deepseek-main
    provider_type: deepseek
routes:
  - id: chat-test
    llm_api: openai
    match:
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
	if !strings.Contains(err.Error(), `routes["chat-test"]: route "chat-test" default candidates must belong to a single target model`) {
		t.Fatalf("Validate() error = %v", err)
	}
}
