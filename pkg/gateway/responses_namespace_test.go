package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/credential"
	credentialscheduler "github.com/agent-guide/agent-gateway/pkg/credential/scheduler"
	llmroutepkg "github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	upstream "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
)

func TestResponsesRestorationFailureStopsCredentialFallback(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"id":"r","output":[{"type":"function_call","name":"unknown_alias","call_id":"c","arguments":"{}"}]}`)
	}))
	defer server.Close()
	cfg := provider.ProviderConfig{Id: "main", ProviderType: "openai", BaseURL: server.URL, Options: map[string]any{"responses_tool_namespaces": "flatten"}, Network: provider.NetworkConfig{MaxRetries: -1}}
	prov, err := upstream.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mgr := credential.NewManager(nil)
	scheduler := credentialscheduler.NewScheduler(nil)
	if listener, ok := scheduler.(credential.CredentialLifecycleListener); ok {
		mgr.AddListener(listener)
	}
	for _, id := range []string{"first", "second"} {
		if err := mgr.RegisterCredential(t.Context(), &credential.Credential{ID: id, ProviderType: "openai", ProviderID: "main", Scope: credential.ProviderIDCredentialScope("main"), Type: credential.TypeAPIKey, Attributes: map[string]string{"api_key": id}}); err != nil {
			t.Fatal(err)
		}
	}
	routed := &RoutedProvider{
		route:            &llmroutepkg.LLMRoute{RouteMetadata: llmroutepkg.RouteMetadata{ID: "r", Protocol: llmroutepkg.RouteProtocolOpenAI}, TargetPolicy: &llmroutepkg.RouteModelPolicy{DefaultModel: "fixture", ModelTargets: []llmroutepkg.RouteModelTarget{{Name: "fixture", Kind: "provider-model", ProviderID: "main", UpstreamModel: "fixture"}}}},
		providerResolver: ProviderResolverFunc(func(context.Context, string) (provider.Provider, error) { return prov, nil }),
		providerConfigs:  testGatewayProviderConfigResolver{configs: map[string]provider.ProviderConfig{"main": cfg}},
		modelCatalog:     testGatewayModelCatalogResolver{models: map[string]modelcatalog.ResolvedProviderModel{"main/fixture": {ProviderModel: modelcatalog.ProviderModel{ProviderID: "main", UpstreamModel: "fixture", Enabled: true}}}}, credentialMgr: mgr, scheduler: scheduler,
	}
	var req provider.ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"fixture","input":"inspect","tools":[{"type":"namespace","name":"browser","tools":[{"type":"function","name":"status"}]}]}`), &req); err != nil {
		t.Fatal(err)
	}
	_, err = routed.CreateResponses(t.Context(), &req)
	var restoration *provider.ResponsesRestorationError
	if !errors.As(err, &restoration) {
		t.Fatalf("expected restoration error, got %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("successful generation repeated %d times", hits.Load())
	}
}
