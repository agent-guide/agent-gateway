package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/agent-guide/agent-gateway/pkg/cliauth/authenticator"
	_ "github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
	"go.uber.org/zap"
)

func TestLoadStaticConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")

	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	writeFile(t, path, `
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
    api_key: ${OPENAI_API_KEY}
llmRoutes:
  - id: chat-prod
    protocol: openai
    match:
      path_prefix: /
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      provider_target:
        provider_id: openai-main
`)

	cfg, err := loadStaticConfig(context.Background(), Options{StaticConfigPath: path})
	if err != nil {
		t.Fatalf("loadStaticConfig() error = %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("len(Providers) = %d, want 1", len(cfg.Providers))
	}
	if _, ok := cfg.Providers["openai-main"]; !ok {
		t.Fatalf("Providers missing openai-main: %#v", cfg.Providers)
	}
	if len(cfg.LLMRoutes) != 1 || cfg.LLMRoutes[0].ID != "chat-prod" {
		t.Fatalf("LLMRoutes = %#v", cfg.LLMRoutes)
	}
}

func TestBootstrapGatewayWithStaticConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")

	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "gateway.yaml")
	dbPath := filepath.Join(dir, "configstore.db")
	writeFile(t, bundlePath, `
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
    api_key: ${OPENAI_API_KEY}
llmRoutes:
  - id: chat-prod
    protocol: openai
    match:
      path_prefix: /
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      provider_target:
        provider_id: openai-main
`)

	gw, refresher, err := bootstrapGateway(context.Background(), Options{
		ConfigStorePath:  dbPath,
		StaticConfigPath: bundlePath,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("bootstrapGateway() error = %v", err)
	}
	if refresher == nil {
		t.Fatal("bootstrapGateway() refresher = nil")
	}
	if gw.ProviderManager() == nil || !gw.ProviderManager().IsStatic("openai-main") {
		t.Fatal("expected static provider openai-main")
	}
	if gw.AgentRouteConfigManager() == nil || !gw.AgentRouteConfigManager().IsStatic("chat-prod") {
		t.Fatal("expected static route chat-prod")
	}
}

func TestLoadStaticConfigRejectsManagedModels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	writeFile(t, path, `
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
managedModels:
  - provider_id: openai-main
    upstream_model: gpt-4.1
`)

	_, err := loadStaticConfig(context.Background(), Options{StaticConfigPath: path})
	if err == nil {
		t.Fatal("expected static config managedModels to fail")
	}
}

func TestLoadStaticConfigRejectsVirtualKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	writeFile(t, path, `
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
virtualKeys:
  - id: vk-local-test
`)

	_, err := loadStaticConfig(context.Background(), Options{StaticConfigPath: path})
	if err == nil {
		t.Fatal("expected static config virtualKeys to fail")
	}
}

func TestLoadStaticConfigRejectsLogicalModelRoutes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	writeFile(t, path, `
apiVersion: gateway.agw/v1alpha1
kind: GatewayBundle
providers:
  - id: openai-main
    provider_type: openai
llmRoutes:
  - id: chat-prod
    protocol: openai
    match:
      path_prefix: /
      methods:
        - POST
    auth_policy:
      require_virtual_key: true
    target_policy:
      default_model: chat-default
      model_targets:
        - name: chat-default
          candidates:
            - provider_id: openai-main
              upstream_model: gpt-4.1
              default: true
`)

	_, err := loadStaticConfig(context.Background(), Options{StaticConfigPath: path})
	if err == nil {
		t.Fatal("expected static config logical-model routes to fail")
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
