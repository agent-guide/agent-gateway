package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/credential"
	"github.com/agent-guide/agent-gateway/pkg/gateway/modelcatalog"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	_ "github.com/agent-guide/agent-gateway/pkg/llm/provider/openai"
)

type uncertainProviderTransaction struct {
	configstore.ConfigStoreBackend
	uncertain   bool
	rollback    bool
	afterCommit func()
}

func (b *uncertainProviderTransaction) Transaction(ctx context.Context, names []string, apply func(map[string]configstore.ConfigStore) error) error {
	err := b.ConfigStoreBackend.(configstore.TransactionalBackend).Transaction(ctx, names, func(stores map[string]configstore.ConfigStore) error {
		if err := apply(stores); err != nil {
			return err
		}
		if b.rollback {
			return errors.New("injected uncertain rollback")
		}
		return nil
	})
	if err == nil {
		if b.afterCommit != nil {
			b.afterCommit()
		}
		if b.uncertain {
			return errors.New("injected uncertain commit")
		}
	}
	return err
}

func controlPlaneGateway(t *testing.T) (*AgentGateway, *configstore.Backend, *configsqlite.SQLiteConfigStoreCreator) {
	t.Helper()
	creator, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := creator.UsageDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	backend := configstore.NewBackend(creator)
	if err := schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	store, _ := backend.Get(schema.StoreCredentials)
	creds := credential.NewManager(store)
	if err := creds.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	g := NewAgentGateway()
	if err := g.Bootstrap(t.Context(), BootstrapOptions{ConfigStoreBackend: backend, CredentialManager: creds}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return g, backend, creator
}

func providerRoute(id, providerID string) routecore.AgentRouteConfig {
	return routecore.AgentRouteConfig{ID: id, Kind: routecore.RouteKindLLM, Protocol: routecore.RouteProtocolOpenAI,
		MatchPolicy:  routecore.RouteMatchPolicy{PathPrefix: "/" + id + "/"},
		TargetPolicy: []byte(fmt.Sprintf(`{"type":"direct-provider","provider_target":{"provider_id":%q}}`, providerID))}
}

func TestProviderDeletionAndRouteCreationAreSerializable(t *testing.T) {
	g, backend, _ := controlPlaneGateway(t)
	providers, _ := backend.Get(schema.StoreProviders)
	routes, _ := backend.Get(schema.StoreRoutes)
	for i := range 16 {
		id := fmt.Sprintf("p%d", i)
		routeID := fmt.Sprintf("r%d", i)
		if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: id, ProviderType: "openai"}); err != nil {
			t.Fatal(err)
		}
		var createErr, deleteErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			createErr = g.AgentRouteConfigManager().Create(t.Context(), providerRoute(routeID, id), "")
		}()
		go func() { defer wg.Done(); deleteErr = g.ProviderManager().DeleteConfig(t.Context(), id) }()
		wg.Wait()
		_, providerErr := providers.Get(t.Context(), id)
		_, routeErr := routes.Get(t.Context(), routeID)
		if createErr == nil {
			if !errors.Is(deleteErr, ErrResourceReferenced) || providerErr != nil || routeErr != nil {
				t.Fatalf("creation winner: create=%v delete=%v provider=%v route=%v", createErr, deleteErr, providerErr, routeErr)
			}
		} else if deleteErr != nil || !errors.Is(createErr, ErrProviderNotConfigured) || !errors.Is(providerErr, configstore.ErrNotFound) || !errors.Is(routeErr, configstore.ErrNotFound) {
			t.Fatalf("deletion winner: create=%v delete=%v provider=%v route=%v", createErr, deleteErr, providerErr, routeErr)
		}
	}
}

func TestProviderDeletionRollsBackOverlayCleanupFailure(t *testing.T) {
	g, backend, creator := controlPlaneGateway(t)
	if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: "p", ProviderType: "openai"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := g.ModelCatalog().CreateManagedModel(t.Context(), modelcatalog.ManagedModel{ProviderID: "p", UpstreamModel: name, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := creator.UsageDB().Exec(`CREATE TRIGGER fail_cleanup BEFORE DELETE ON model_configs WHEN OLD.upstream_model='b' BEGIN SELECT RAISE(ABORT,'injected failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.ProviderManager().DeleteConfig(t.Context(), "p"); err == nil {
		t.Fatal("cleanup failure returned success")
	}
	providers, _ := backend.Get(schema.StoreProviders)
	models, _ := backend.Get(schema.StoreManagedModels)
	if _, err := providers.Get(t.Context(), "p"); err != nil {
		t.Fatal("Provider deletion partially committed")
	}
	if items, err := models.List(t.Context()); err != nil || len(items) != 2 {
		t.Fatalf("partial cleanup: %v, %v", items, err)
	}
	if _, err := g.ProviderManager().GetConfig(t.Context(), "p"); err != nil {
		t.Fatal("rollback invalidated the valid runtime config")
	}
	if err := creator.UsageDB().Exec(`DROP TRIGGER fail_cleanup`).Error; err != nil {
		t.Fatal(err)
	}
	if err := creator.UsageDB().Exec(`CREATE TRIGGER fail_cleanup BEFORE DELETE ON model_configs WHEN OLD.upstream_model='b' BEGIN SELECT RAISE(IGNORE); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.ProviderManager().DeleteConfig(t.Context(), "p"); err == nil {
		t.Fatal("authoritative readback did not detect a silently skipped cleanup")
	}
	if _, err := providers.Get(t.Context(), "p"); err != nil {
		t.Fatal("failed readback partially committed Provider deletion")
	}
	if items, err := models.List(t.Context()); err != nil || len(items) != 2 {
		t.Fatal("failed readback partially committed overlay deletion")
	}
	if err := creator.UsageDB().Exec(`DROP TRIGGER fail_cleanup`).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.ProviderManager().DeleteConfig(t.Context(), "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ProviderManager().GetConfig(t.Context(), "p"); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("deleted provider cached: %v", err)
	}
	if items, err := models.List(t.Context()); err != nil || len(items) != 0 {
		t.Fatalf("cleanup not committed: %v, %v", items, err)
	}
}

func TestProviderCredentialOwnershipAndManagementDeclarations(t *testing.T) {
	g, _, _ := controlPlaneGateway(t)
	if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: "p", ProviderType: "openai"}); err != nil {
		t.Fatal(err)
	}
	a := agent.Agent{ID: "a", Name: "a", Disabled: true, Runtime: agent.Runtime{Type: agent.RuntimeTypeHTTP, HTTP: &agent.HTTPRuntime{CardURL: "https://example.org/card", Protocol: "a2a"}}, Resources: agent.Resources{ProviderIDs: []string{"p"}}}
	if err := g.AgentManager().Create(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	cred := &credential.Credential{ID: "cred", Type: credential.TypeAPIKey, ProviderID: "p", ProviderType: "openai", Attributes: map[string]string{"api_key": "test"}}
	if err := g.CredentialManager().RegisterCredential(t.Context(), cred); err != nil {
		t.Fatal(err)
	}
	if err := g.ProviderManager().DeleteConfig(t.Context(), "p"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("credential owner deletion = %v", err)
	}
	if err := g.CredentialManager().DeregisterCredential(t.Context(), "cred"); err != nil {
		t.Fatal(err)
	}
	if err := g.ProviderManager().DeleteConfig(t.Context(), "p"); err != nil {
		t.Fatal(err)
	}
	got, ok := g.AgentManager().GetSnapshot("a")
	if !ok || len(got.Resources.ProviderIDs) != 1 || got.Resources.ProviderIDs[0] != "p" {
		t.Fatal("deletion removed management declarations")
	}
	if err := g.CredentialManager().RegisterCredential(t.Context(), cred); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("orphan credential creation = %v", err)
	}
}

func TestDirectRouteDeletionCannotBypassAgentReferences(t *testing.T) {
	g, _, _ := controlPlaneGateway(t)
	if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: "p", ProviderType: "openai"}); err != nil {
		t.Fatal(err)
	}
	if err := g.AgentRouteConfigManager().Create(t.Context(), providerRoute("r", "p"), ""); err != nil {
		t.Fatal(err)
	}
	a := agent.Agent{ID: "a", Name: "a", Disabled: true, Runtime: agent.Runtime{Type: agent.RuntimeTypeHTTP, HTTP: &agent.HTTPRuntime{CardURL: "https://example.org/card", Protocol: "a2a"}}, Routes: agent.Routes{LLMRouteIDs: []string{"r"}}}
	if err := g.AgentManager().Create(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	if err := g.AgentRouteConfigManager().Delete(t.Context(), "r"); !errors.Is(err, ErrResourceReferenced) {
		t.Fatalf("direct delete bypassed references: %v", err)
	}
}

func TestProviderDeletionSerializesCredentialAndModelWrites(t *testing.T) {
	g, backend, _ := controlPlaneGateway(t)
	models, _ := backend.Get(schema.StoreManagedModels)
	for _, kind := range []string{"credential", "model"} {
		for i := range 8 {
			id := fmt.Sprintf("%s%d", kind, i)
			if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: id, ProviderType: "openai"}); err != nil {
				t.Fatal(err)
			}
			var createErr, deleteErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); deleteErr = g.ProviderManager().DeleteConfig(t.Context(), id) }()
			go func() {
				defer wg.Done()
				if kind == "credential" {
					createErr = g.CredentialManager().RegisterCredential(t.Context(), &credential.Credential{ID: id, ProviderID: id, ProviderType: "openai", Type: credential.TypeAPIKey})
				} else {
					createErr = g.ModelCatalog().CreateManagedModel(t.Context(), modelcatalog.ManagedModel{ProviderID: id, UpstreamModel: "model"})
				}
			}()
			wg.Wait()
			if kind == "credential" && createErr == nil {
				if !errors.Is(deleteErr, ErrResourceReferenced) {
					t.Fatalf("credential owner was deleted: %v", deleteErr)
				}
			} else {
				if deleteErr != nil {
					t.Fatalf("delete = %v", deleteErr)
				}
				if createErr != nil && !errors.Is(createErr, ErrProviderNotConfigured) {
					t.Fatalf("create = %v", createErr)
				}
				if _, err := models.Get(t.Context(), id, "model"); !errors.Is(err, configstore.ErrNotFound) {
					t.Fatal("orphan model survived Provider deletion")
				}
			}
		}
	}
}

func TestAgentDeletionSerializesAgentRouteCreation(t *testing.T) {
	g, backend, _ := controlPlaneGateway(t)
	routes, _ := backend.Get(schema.StoreRoutes)
	for i := range 8 {
		id := fmt.Sprintf("agent%d", i)
		a := agent.Agent{ID: id, Name: id, Disabled: true, Runtime: agent.Runtime{Type: agent.RuntimeTypeHTTP, HTTP: &agent.HTTPRuntime{CardURL: "https://example.org/card", Protocol: "a2a"}}}
		if err := g.AgentManager().Create(t.Context(), a); err != nil {
			t.Fatal(err)
		}
		cfg := routecore.AgentRouteConfig{ID: id, Kind: routecore.RouteKindAgent, Protocol: routecore.RouteProtocolAgent, MatchPolicy: routecore.RouteMatchPolicy{PathPrefix: "/" + id + "/"}, TargetPolicy: []byte(fmt.Sprintf(`{"agent_id":%q}`, id))}
		var createErr, deleteErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); createErr = g.AgentRouteConfigManager().Create(t.Context(), cfg, "") }()
		go func() { defer wg.Done(); deleteErr = g.AgentManager().Delete(t.Context(), id) }()
		wg.Wait()
		if createErr == nil {
			if !errors.Is(deleteErr, agent.ErrAgentRouteTarget) {
				t.Fatalf("Agent deletion bypassed route: %v", deleteErr)
			}
		} else {
			if deleteErr != nil {
				t.Fatal(deleteErr)
			}
			if _, err := routes.Get(t.Context(), id); !errors.Is(err, configstore.ErrNotFound) {
				t.Fatal("AgentRoute targets deleted Agent")
			}
		}
	}
}

func TestProviderUncertainCommitAndPublicationFailureRequireRecovery(t *testing.T) {
	for _, phase := range []string{"commit", "publication"} {
		t.Run(phase, func(t *testing.T) {
			g, backend, _ := controlPlaneGateway(t)
			if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: "p", ProviderType: "openai"}); err != nil {
				t.Fatal(err)
			}
			if err := g.ModelCatalog().CreateManagedModel(t.Context(), modelcatalog.ManagedModel{ProviderID: "p", UpstreamModel: "model"}); err != nil {
				t.Fatal(err)
			}
			fault := &uncertainProviderTransaction{ConfigStoreBackend: backend, uncertain: phase == "commit"}
			configs := g.ProviderManager().configs
			if phase == "publication" {
				fault.afterCommit = func() { g.ProviderManager().configs = nil }
			}
			g.control.backend = fault
			if err := g.ProviderManager().DeleteConfig(t.Context(), "p"); !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatalf("delete = %v", err)
			}
			g.ProviderManager().configs = configs
			state := g.ProviderManager().PendingMutation("p")
			if state == nil || state.OperationID == "" {
				t.Fatal("committed deletion has no observable operation identity")
			}
			if _, err := g.ProviderManager().GetConfig(t.Context(), "p"); !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatal("unconfirmed Provider served stale config")
			}
			if _, err := g.ProviderManager().ResolveProvider(t.Context(), "p"); !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatal("unconfirmed Provider remained executable")
			}
			if err := g.ProviderManager().DeleteConfig(t.Context(), "p"); !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatal("retry issued another delete")
			}
			if err := g.AgentRouteConfigManager().Create(t.Context(), providerRoute("pending", "p"), ""); !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatal("new reference bypassed recovery gate")
			}
			fault.uncertain, fault.afterCommit = false, nil
			if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: "q", ProviderType: "openai"}); err != nil {
				t.Fatalf("unrelated Provider blocked: %v", err)
			}
			if err := g.ProviderManager().Recover(t.Context(), "p"); err != nil {
				t.Fatal(err)
			}
			if g.ProviderManager().PendingMutation("p") != nil {
				t.Fatal("recovery retained pending state")
			}
			if _, err := g.ProviderManager().GetConfig(t.Context(), "p"); !errors.Is(err, ErrProviderNotConfigured) {
				t.Fatalf("committed deletion was not recovered: %v", err)
			}
			if err := g.ProviderManager().Recover(t.Context(), "p"); err != nil {
				t.Fatal("recovery was not idempotent")
			}
		})
	}
}

func TestProviderCreateAndUpdateUncertainOutcomesRequireRecovery(t *testing.T) {
	for _, action := range []configmgr.MutationAction{configmgr.MutationCreate, configmgr.MutationUpdate} {
		for _, rollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/rollback=%t", action, rollback), func(t *testing.T) {
				g, backend, _ := controlPlaneGateway(t)
				manager := g.ProviderManager()
				if action == configmgr.MutationUpdate {
					if err := manager.CreateConfig(t.Context(), provider.ProviderConfig{Id: "p", ProviderType: "openai", DefaultModel: "before"}); err != nil {
						t.Fatal(err)
					}
				}
				fault := &uncertainProviderTransaction{ConfigStoreBackend: backend, uncertain: !rollback, rollback: rollback}
				g.control.backend = fault
				cfg := provider.ProviderConfig{Id: "p", ProviderType: "openai", DefaultModel: "after"}
				write := func() error {
					if action == configmgr.MutationCreate {
						return manager.CreateConfig(t.Context(), cfg)
					}
					return manager.UpdateConfig(t.Context(), "p", cfg)
				}
				if err := write(); !errors.Is(err, configmgr.ErrMutationPending) {
					t.Fatalf("uncertain write = %v", err)
				}
				pending := manager.PendingMutation("p")
				if pending == nil || pending.Action != action {
					t.Fatalf("pending = %+v", pending)
				}
				if _, err := manager.GetConfig(t.Context(), "p"); !errors.Is(err, configmgr.ErrMutationPending) {
					t.Fatal("unconfirmed config remained readable")
				}
				if _, err := manager.ResolveProvider(t.Context(), "p"); !errors.Is(err, configmgr.ErrMutationPending) {
					t.Fatal("unconfirmed config remained executable")
				}
				if err := write(); !errors.Is(err, configmgr.ErrMutationPending) || manager.PendingMutation("p").OperationID != pending.OperationID {
					t.Fatal("retry bypassed pending write")
				}
				fault.uncertain, fault.rollback = false, false
				if err := manager.CreateConfig(t.Context(), provider.ProviderConfig{Id: "q", ProviderType: "openai"}); err != nil {
					t.Fatalf("unrelated write blocked: %v", err)
				}
				if err := manager.Recover(t.Context(), "p"); err != nil {
					t.Fatal(err)
				}
				got, err := manager.GetConfig(t.Context(), "p")
				if rollback && action == configmgr.MutationCreate {
					if !errors.Is(err, ErrProviderNotConfigured) {
						t.Fatalf("rolled back create was replayed: %v", err)
					}
				} else {
					want := "after"
					if rollback {
						want = "before"
					}
					if err != nil || got.DefaultModel != want {
						t.Fatalf("recovered config = %+v, %v; want %s", got, err, want)
					}
				}
				if manager.PendingMutation("p") != nil {
					t.Fatal("recovery did not clear pending")
				}
			})
		}
	}
}

type uncertainProviderStore struct{ configstore.ConfigStore }

func (s uncertainProviderStore) Create(ctx context.Context, item any) error {
	if err := s.ConfigStore.Create(ctx, item); err != nil {
		return err
	}
	return errors.New("injected error after create committed")
}
func (s uncertainProviderStore) Update(ctx context.Context, item any) error {
	if err := s.ConfigStore.Update(ctx, item); err != nil {
		return err
	}
	return errors.New("injected error after update committed")
}

func TestProviderNontransactionalWriteErrorsRequireRecovery(t *testing.T) {
	for _, update := range []bool{false, true} {
		t.Run(fmt.Sprintf("update=%t", update), func(t *testing.T) {
			g, backend, _ := controlPlaneGateway(t)
			manager := g.ProviderManager()
			cfg := provider.ProviderConfig{Id: "p", ProviderType: "openai", DefaultModel: "before"}
			if update {
				if err := manager.CreateConfig(t.Context(), cfg); err != nil {
					t.Fatal(err)
				}
			}
			g.control.backend = struct{ configstore.ConfigStoreBackend }{backend}
			manager.store = uncertainProviderStore{manager.store}
			cfg.DefaultModel = "after"
			var err error
			if update {
				err = manager.UpdateConfig(t.Context(), "p", cfg)
			} else {
				err = manager.CreateConfig(t.Context(), cfg)
			}
			if !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatalf("unconfirmed store write = %v", err)
			}
			if err := manager.Recover(t.Context(), "p"); err != nil {
				t.Fatal(err)
			}
			got, err := manager.GetConfig(t.Context(), "p")
			if err != nil || got.DefaultModel != "after" {
				t.Fatalf("recovered config = %+v, %v", got, err)
			}
		})
	}
}

func TestPendingProviderDeletionBlocksManagedModelDeletion(t *testing.T) {
	g, backend, _ := controlPlaneGateway(t)
	if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: "p", ProviderType: "openai"}); err != nil {
		t.Fatal(err)
	}
	model := modelcatalog.ManagedModel{ProviderID: "p", UpstreamModel: "model", Enabled: true}
	if err := g.ModelCatalog().CreateManagedModel(t.Context(), model); err != nil {
		t.Fatal(err)
	}
	g.control.backend = &uncertainProviderTransaction{ConfigStoreBackend: backend, rollback: true}
	if err := g.ProviderManager().DeleteConfig(t.Context(), "p"); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("uncertain deletion = %v", err)
	}
	if err := g.ModelCatalog().DeleteManagedModel(t.Context(), "p", "model"); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("model deletion bypassed pending Provider: %v", err)
	}
	if err := g.ProviderManager().Recover(t.Context(), "p"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := g.ModelCatalog().GetManagedModel(t.Context(), "p", "model"); err != nil || !ok {
		t.Fatalf("overlay lost during recovery: exists=%t, %v", ok, err)
	}
	if err := g.ModelCatalog().DeleteManagedModel(t.Context(), "p", "model"); err != nil {
		t.Fatalf("model deletion remained blocked after recovery: %v", err)
	}
}

type unsupportedProviderTransaction struct{ configstore.ConfigStoreBackend }

func (unsupportedProviderTransaction) Transaction(context.Context, []string, func(map[string]configstore.ConfigStore) error) error {
	return configstore.ErrTransactionsUnsupported
}

func TestProviderWritesSupportBackendsWithoutTransactions(t *testing.T) {
	g, backend, _ := controlPlaneGateway(t)
	g.control.backend = unsupportedProviderTransaction{backend}
	cfg := provider.ProviderConfig{Id: "p", ProviderType: "openai", DefaultModel: "before"}
	if err := g.ProviderManager().CreateConfig(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.DefaultModel = "after"
	if err := g.ProviderManager().UpdateConfig(t.Context(), "p", cfg); err != nil {
		t.Fatal(err)
	}
	got, err := g.ProviderManager().GetConfig(t.Context(), "p")
	if err != nil || got.DefaultModel != "after" {
		t.Fatalf("nontransactional write = %+v, %v", got, err)
	}
}
