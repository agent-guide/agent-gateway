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
managedModels:
  - provider_id: openai-main
    upstream_model: gpt-4.1
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
	if len(cfg.Routes) != 1 || cfg.Routes[0].ID != "chat-prod" {
		t.Fatalf("Routes = %#v", cfg.Routes)
	}
	if len(cfg.ManagedModels) != 1 || cfg.ManagedModels[0].ProviderID != "openai-main" {
		t.Fatalf("ManagedModels = %#v", cfg.ManagedModels)
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
managedModels:
  - provider_id: openai-main
    upstream_model: gpt-4.1
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
	if gw.AgentRouteManager() == nil || !gw.AgentRouteManager().IsStatic("chat-prod") {
		t.Fatal("expected static route chat-prod")
	}
	model, ok, err := gw.ModelCatalog().GetManagedModel(context.Background(), "openai-main", "gpt-4.1")
	if err != nil {
		t.Fatalf("GetManagedModel() error = %v", err)
	}
	if !ok || model == nil {
		t.Fatal("expected static managed model openai-main/gpt-4.1")
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
