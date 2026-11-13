package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"sync"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func TestIndependentModelsAcrossRoutes(t *testing.T) {
	g, backend, _ := controlPlaneGateway(t)
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.ProviderManager().CreateConfig(ctx, provider.ProviderConfig{Id: "p", ProviderType: "openai"}))
	catalog := g.ModelCatalog()
	for _, name := range []string{"a", "b"} {
		must(catalog.CreateProviderModel(ctx, modelcatalog.ProviderModel{ProviderID: "p", UpstreamModel: name, Enabled: true, CapabilityOverrides: &provider.ModelCapabilities{Streaming: true, Tools: true}}))
	}
	logical := modelcatalog.LogicalModel{ID: "chat", Strategy: modelcatalog.SelectionPriority, Fallback: modelcatalog.FallbackPolicy{Enabled: true, MaxNum: 1}, Candidates: []modelcatalog.ModelCandidate{{ProviderID: "p", UpstreamModel: "a", Priority: 2}, {ProviderID: "p", UpstreamModel: "b", Priority: 1}}}
	must(catalog.CreateLogicalModel(ctx, logical))
	for _, protocol := range []routecore.RouteProtocol{routecore.RouteProtocolOpenAI, routecore.RouteProtocolAnthropic, routecore.RouteProtocolCC} {
		r := llmroute.LLMRoute{RouteMetadata: routecore.RouteMetadata{ID: string(protocol), Protocol: protocol, MatchPolicy: routecore.RouteMatchPolicy{PathPrefix: "/" + string(protocol)}}, TargetPolicy: &llmroute.RouteModelPolicy{DefaultModel: "default", ModelTargets: []llmroute.RouteModelTarget{
			{Name: "default", Kind: "logical-model", LogicalModelID: "chat"},
		}}}
		cfg, err := r.ToConfig()
		must(err)
		must(g.RouteConfigManager().Create(ctx, cfg, ""))
	}
	// Caller changes must not edit the published model generation.
	logical.Candidates[0].UpstreamModel = "caller-mutated"
	m, ok, err := catalog.GetLogicalModel(ctx, "chat")
	must(err)
	if !ok || m.Candidates[0].UpstreamModel != "a" {
		t.Fatalf("model escaped snapshot: %+v", m)
	}
	m.Candidates[0].Priority = 0
	must(catalog.UpdateLogicalModel(ctx, *m))
	for _, id := range []string{"openai", "anthropic", "cc"} {
		cfg, err := g.RouteConfigManager().Get(ctx, id)
		must(err)
		r, err := llmroute.NewLLMRouteFromConfig(cfg)
		must(err)
		target, err := r.ResolveTarget(ctx, catalog, g.ProviderManager(), llmroute.RequestRequirements{RequireTools: true})
		must(err)
		if target.UpstreamModel != "b" || target.LogicalModel != "chat" || target.ClientModel != "default" || !target.Fallback.Enabled {
			t.Fatalf("shared definition was not applied: %+v", target)
		}
		if _, err = r.ResolveTarget(ctx, catalog, g.ProviderManager(), llmroute.RequestRequirements{Model: "chat"}); err == nil {
			t.Fatal("unexposed catalog ID was accepted")
		}
	}
	// Disabled references retain deletion protection.
	cfg, err := g.RouteConfigManager().Get(ctx, "cc")
	must(err)
	cfg.Disabled = true
	must(g.RouteConfigManager().Update(ctx, "cc", cfg))
	if err := catalog.DeleteLogicalModel(ctx, "chat"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("logical delete = %v", err)
	}
	if err := catalog.DeleteProviderModel(ctx, "p", "a"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("candidate delete = %v", err)
	}
	if err := g.ProviderManager().DeleteConfig(ctx, "p"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("provider delete = %v", err)
	}
	// A failed refresh cannot retire the last valid generation.
	raw, _ := backend.Get(schema.StoreLogicalModels)
	must(raw.Delete(ctx, "chat"))
	if err := catalog.RefreshModels(ctx); err == nil {
		t.Fatal("refresh accepted a dangling route")
	}
	if _, ok, err := catalog.GetLogicalModel(ctx, "chat"); err != nil || !ok {
		t.Fatalf("refresh destroyed last generation: %v", err)
	}
	must(raw.Create(ctx, m))
	// The same graph can be reconstructed from persisted independent resources.
	restored := NewAgentGateway()
	must(restored.Bootstrap(ctx, BootstrapOptions{ConfigStoreBackend: backend}))
	t.Cleanup(restored.Close)
	if _, ok, err := restored.ModelCatalog().GetLogicalModel(ctx, "chat"); err != nil || !ok {
		t.Fatalf("restart: %v", err)
	}
	for _, id := range []string{"openai", "anthropic", "cc"} {
		must(g.RouteConfigManager().Delete(ctx, id))
	}
	must(catalog.DeleteLogicalModel(ctx, "chat"))
	must(catalog.DeleteProviderModel(ctx, "p", "a"))
	must(g.ProviderManager().DeleteConfig(ctx, "p"))
	if _, ok, err := catalog.GetProviderModel(ctx, "p", "b"); err != nil || ok {
		t.Fatalf("provider cleanup retained cached model: %v", err)
	}
}

func TestModelReferencesAreRequiredAtWrite(t *testing.T) {
	g, _, _ := controlPlaneGateway(t)
	p := llmroute.RouteModelPolicy{ModelTargets: []llmroute.RouteModelTarget{{Name: "m", Kind: "logical-model", LogicalModelID: "missing"}}}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	r := providerRoute("r", "unused")
	r.TargetPolicy = data
	if err := g.RouteConfigManager().Create(t.Context(), r, ""); !errors.Is(err, ErrDependencyNotConfigured) {
		t.Fatalf("route create = %v", err)
	}
	if err := g.ModelCatalog().CreateLogicalModel(t.Context(), modelcatalog.LogicalModel{ID: "m", Candidates: []modelcatalog.ModelCandidate{{ProviderID: "missing", UpstreamModel: "m"}}}); !errors.Is(err, ErrDependencyNotConfigured) {
		t.Fatalf("logical create = %v", err)
	}
}

// The store spy fails any read after publication, including an implicit discovery
// refresh reaching Provider config. Both concrete and logical resolution use snapshots.
type modelStoreReadFence struct {
	configstore.ConfigStore
	closed bool
}

func (s *modelStoreReadFence) List(ctx context.Context) ([]any, error) {
	if s.closed {
		return nil, errors.New("hot-path List")
	}
	return s.ConfigStore.List(ctx)
}
func (s *modelStoreReadFence) Get(ctx context.Context, keys ...any) (any, error) {
	if s.closed {
		return nil, errors.New("hot-path Get")
	}
	return s.ConfigStore.Get(ctx, keys...)
}

func TestModelResolutionUsesPublishedSnapshots(t *testing.T) {
	g, backend, _ := controlPlaneGateway(t)
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.ProviderManager().CreateConfig(ctx, provider.ProviderConfig{Id: "p", ProviderType: "openai"}))
	modelStore, _ := backend.Get(schema.StoreProviderModels)
	logicalStore, _ := backend.Get(schema.StoreLogicalModels)
	pm := &modelStoreReadFence{ConfigStore: modelStore}
	lm := &modelStoreReadFence{ConfigStore: logicalStore}
	svc := modelcatalog.NewService(pm, g.ProviderManager(), nil)
	must(modelcatalog.ConfigureLogicalModels(svc, lm, nil, nil))
	must(svc.CreateProviderModel(ctx, modelcatalog.ProviderModel{ProviderID: "p", UpstreamModel: "m", Enabled: true, CapabilityOverrides: &provider.ModelCapabilities{Tools: true}}))
	must(svc.CreateLogicalModel(ctx, modelcatalog.LogicalModel{ID: "chat", Candidates: []modelcatalog.ModelCandidate{{ProviderID: "p", UpstreamModel: "m"}}}))
	pm.closed = true
	lm.closed = true
	r := llmroute.LLMRoute{RouteMetadata: routecore.RouteMetadata{ID: "r"}, TargetPolicy: &llmroute.RouteModelPolicy{ModelTargets: []llmroute.RouteModelTarget{{Name: "m", Kind: "provider-model", ProviderID: "p", UpstreamModel: "m"}}}}
	for _, ref := range []llmroute.RouteModelTarget{{Name: "m", Kind: "provider-model", ProviderID: "p", UpstreamModel: "m"}, {Name: "l", Kind: "logical-model", LogicalModelID: "chat"}} {
		r.TargetPolicy = &llmroute.RouteModelPolicy{ModelTargets: []llmroute.RouteModelTarget{ref}}
		name := ref.Name
		target, err := r.ResolveTarget(ctx, svc, g.ProviderManager(), llmroute.RequestRequirements{Model: name, RequireTools: true})
		must(err)
		if target.UpstreamModel != "m" {
			t.Fatalf("wrong target: %+v", target)
		}
	}
}

func TestFailedRouteRefreshRetainsModelReferences(t *testing.T) {
	g, backend, _ := controlPlaneGateway(t)
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.ProviderManager().CreateConfig(ctx, provider.ProviderConfig{Id: "p", ProviderType: "openai"}))
	must(g.ModelCatalog().CreateProviderModel(ctx, modelcatalog.ProviderModel{ProviderID: "p", UpstreamModel: "m", Enabled: true}))
	r := llmroute.LLMRoute{RouteMetadata: routecore.RouteMetadata{ID: "r", Protocol: routecore.RouteProtocolOpenAI, MatchPolicy: routecore.RouteMatchPolicy{PathPrefix: "/r"}}, TargetPolicy: &llmroute.RouteModelPolicy{ModelTargets: []llmroute.RouteModelTarget{{Name: "m", Kind: "provider-model", ProviderID: "p", UpstreamModel: "m"}}}}
	cfg, err := r.ToConfig()
	must(err)
	must(g.RouteConfigManager().Create(ctx, cfg, ""))
	store, _ := backend.Get(schema.StoreRoutes)
	changed := cfg.Clone()
	changed.TargetPolicy = json.RawMessage(`{"type":"models","model_targets":[{"name":"missing","kind":"logical-model","logical_model_id":"absent"}]}`)
	must(store.Update(ctx, &changed))
	if err := g.RouteConfigManager().Refresh(ctx); err == nil {
		t.Fatal("refresh published a dangling model reference")
	}
	serving, err := g.RouteConfigManager().Get(ctx, "r")
	must(err)
	if string(serving.TargetPolicy) != string(cfg.TargetPolicy) {
		t.Fatal("failed refresh replaced serving route")
	}
	if err := g.ModelCatalog().DeleteProviderModel(ctx, "p", "m"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("retained route did not protect concrete model: %v", err)
	}
	if err := g.ProviderManager().DeleteConfig(ctx, "p"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("retained route did not protect provider: %v", err)
	}
}

func TestLogicalCreateAndConcreteDeleteAreSerializable(t *testing.T) {
	g, _, _ := controlPlaneGateway(t)
	ctx := t.Context()
	if err := g.ProviderManager().CreateConfig(ctx, provider.ProviderConfig{Id: "p", ProviderType: "openai"}); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		name := fmt.Sprintf("m%d", i)
		if err := g.ModelCatalog().CreateProviderModel(ctx, modelcatalog.ProviderModel{ProviderID: "p", UpstreamModel: name, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		var createErr, deleteErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			createErr = g.ModelCatalog().CreateLogicalModel(ctx, modelcatalog.LogicalModel{ID: name, Candidates: []modelcatalog.ModelCandidate{{ProviderID: "p", UpstreamModel: name}}})
		}()
		go func() { defer wg.Done(); deleteErr = g.ModelCatalog().DeleteProviderModel(ctx, "p", name) }()
		wg.Wait()
		_, exists, err := g.ModelCatalog().GetProviderModel(ctx, "p", name)
		if err != nil {
			t.Fatal(err)
		}
		if createErr == nil {
			if !errors.Is(deleteErr, ErrResourceReferenced) || !exists {
				t.Fatalf("created dangling logical model: create=%v delete=%v exists=%v", createErr, deleteErr, exists)
			}
		} else if deleteErr != nil || exists || !errors.Is(createErr, ErrDependencyNotConfigured) {
			t.Fatalf("unexpected race result: create=%v delete=%v exists=%v", createErr, deleteErr, exists)
		}
	}
}

func TestStaticModelsUseTheSameExplicitRouteContract(t *testing.T) {
	creator, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = creator.Close() })
	backend := configstore.NewBackend(creator)
	if err := schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	concrete := modelcatalog.ProviderModel{ProviderID: "p", UpstreamModel: "fixed", Enabled: true, CapabilityOverrides: &provider.ModelCapabilities{Streaming: true, Tools: true}}
	second := concrete
	second.UpstreamModel = "small"
	logical := modelcatalog.LogicalModel{ID: "shared", Candidates: []modelcatalog.ModelCandidate{{ProviderID: "p", UpstreamModel: "fixed"}}}
	policy := llmroute.RouteModelPolicy{DefaultModel: "fixed", ModelTargets: []llmroute.RouteModelTarget{{Name: "fixed", Kind: "provider-model", ProviderID: "p", UpstreamModel: "fixed"}, {Kind: "provider-model", ProviderID: "p", UpstreamModel: "small"}}}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	g := NewAgentGateway()
	err = g.Bootstrap(t.Context(), BootstrapOptions{ConfigStoreBackend: backend, StaticProviderModels: []modelcatalog.ProviderModel{concrete, second}, StaticLogicalModels: []modelcatalog.LogicalModel{logical}, StaticProviders: map[string]provider.Provider{"p": testProvider{cfg: provider.ProviderConfig{Id: "p", ProviderType: "openai", DefaultModel: "unexposed"}}}, StaticLLMRoutes: []routecore.RouteConfig{{ID: "r", Protocol: routecore.RouteProtocolOpenAI, TargetPolicy: data}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	cfg, err := g.RouteConfigManager().Get(t.Context(), "r")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Kind != routecore.RouteKindLLM {
		t.Fatal("static LLM kind was not materialized")
	}
	r, err := llmroute.NewLLMRouteFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "fixed", "small"} {
		target, err := r.ResolveTarget(t.Context(), g.ModelCatalog(), g.ProviderManager(), llmroute.RequestRequirements{Model: name, RequireTools: true})
		expected := name
		if expected == "" {
			expected = "fixed"
		}
		if err != nil || target.UpstreamModel != expected || target.ProviderID != "p" || target.LogicalModel != "" || target.Fallback.Enabled {
			t.Fatalf("name %q: target=%+v error=%v", name, target, err)
		}
	}
	for _, name := range []string{"public", "logical", "unexposed", "arbitrary-upstream"} {
		if _, err := r.ResolveTarget(t.Context(), g.ModelCatalog(), g.ProviderManager(), llmroute.RequestRequirements{Model: name}); err == nil {
			t.Fatalf("unexposed upstream %q was accepted", name)
		}
	}
	pm, ok, err := g.ModelCatalog().GetProviderModel(t.Context(), "p", "fixed")
	if err != nil || !ok || !pm.ReadOnly {
		t.Fatalf("static concrete metadata: %+v %v", pm, err)
	}
	lm, ok, err := g.ModelCatalog().GetLogicalModel(t.Context(), "shared")
	if err != nil || !ok || !lm.ReadOnly {
		t.Fatalf("static logical metadata: %+v %v", lm, err)
	}
	for _, err := range []error{g.ModelCatalog().CreateProviderModel(t.Context(), concrete), g.ModelCatalog().UpdateProviderModel(t.Context(), concrete), g.ModelCatalog().DeleteProviderModel(t.Context(), "p", "fixed"), g.ModelCatalog().UpdateLogicalModel(t.Context(), logical), g.ModelCatalog().DeleteLogicalModel(t.Context(), "shared")} {
		if !errors.Is(err, modelcatalog.ErrStaticModelReadOnly) {
			t.Fatalf("static model mutation = %v", err)
		}
	}
	store, _ := backend.Get(schema.StoreProviderModels)
	if err := store.Create(t.Context(), &concrete); err != nil {
		t.Fatal(err)
	}
	if err := g.ModelCatalog().RefreshModels(t.Context()); err == nil {
		t.Fatal("refresh shadowed static model with a dynamic identity collision")
	}
	if pm, ok, _ := g.ModelCatalog().GetProviderModel(t.Context(), "p", "fixed"); !ok || !pm.ReadOnly {
		t.Fatal("failed refresh retired static model")
	}
}

func TestProviderModelRoutePublicationRejectsInvalidTargets(t *testing.T) {
	for _, targets := range []string{
		`[{"name":"alias","kind":"provider-model","provider_id":"p","upstream_model":"m"}]`,
		`[{"kind":"provider-model","provider_id":"p","upstream_model":"m"},{"kind":"provider-model","provider_id":"q","upstream_model":"n"}]`,
		`[{"kind":"provider-model","provider_id":"p","upstream_model":"m"},{"name":"chat","kind":"logical-model","logical_model_id":"shared"}]`,
	} {
		t.Run(targets, func(t *testing.T) {
			g, backend, _ := controlPlaneGateway(t)
			ctx := t.Context()
			if err := g.ProviderManager().CreateConfig(ctx, provider.ProviderConfig{Id: "p", ProviderType: "openai"}); err != nil {
				t.Fatal(err)
			}
			if err := g.ModelCatalog().CreateProviderModel(ctx, modelcatalog.ProviderModel{ProviderID: "p", UpstreamModel: "m", Enabled: true}); err != nil {
				t.Fatal(err)
			}
			cfg := routecore.RouteConfig{ID: "r", Kind: routecore.RouteKindLLM, Protocol: routecore.RouteProtocolOpenAI, MatchPolicy: routecore.RouteMatchPolicy{PathPrefix: "/r"}, TargetPolicy: json.RawMessage(`{"type":"models","model_targets":` + targets + `}`)}
			for _, disabled := range []bool{false, true} {
				cfg.Disabled = disabled
				if err := g.RouteConfigManager().Create(ctx, cfg, ""); err == nil {
					t.Fatal("invalid route published")
				}
			}
			cfg.Disabled = false
			invalid := cfg.Clone()
			cfg.TargetPolicy = json.RawMessage(`{"type":"models","default_model":"m","model_targets":[{"kind":"provider-model","provider_id":"p","upstream_model":"m"}]}`)
			if err := g.RouteConfigManager().Create(ctx, cfg, ""); err != nil {
				t.Fatal(err)
			}
			before, err := g.RouteConfigManager().Get(ctx, "r")
			if err != nil {
				t.Fatal(err)
			}
			if err := g.RouteConfigManager().Update(ctx, "r", invalid); err == nil {
				t.Fatal("invalid update published")
			}
			store, _ := backend.Get(schema.StoreRoutes)
			if err := store.Update(ctx, &invalid); err != nil {
				t.Fatal(err)
			}
			if err := g.RouteConfigManager().Refresh(ctx); err == nil {
				t.Fatal("invalid stored route published")
			}
			after, err := g.RouteConfigManager().Get(ctx, "r")
			if err != nil || string(before.TargetPolicy) != string(after.TargetPolicy) {
				t.Fatalf("failed mutation changed published route: %v", err)
			}
		})
	}
}
