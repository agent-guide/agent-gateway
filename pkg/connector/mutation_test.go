package connector_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/connector"
)

type pausedMutationStore struct {
	*testConfigStore
	pause     bool
	persisted chan struct{}
	release   chan struct{}
}

type pausedRecoveryStore struct {
	*uncertainStore
	pauseRead   bool
	readStarted chan struct{}
	release     chan struct{}
}

func (s *pausedRecoveryStore) Get(ctx context.Context, keys ...any) (any, error) {
	item, err := s.uncertainStore.Get(ctx, keys...)
	if s.pauseRead {
		close(s.readStarted)
		<-s.release
	}
	return item, err
}

func TestConnectorShutdownFencesRecoveryPublication(t *testing.T) {
	store := &pausedRecoveryStore{uncertainStore: &uncertainStore{testConfigStore: newTestConfigStore(t).(*testConfigStore)}, readStarted: make(chan struct{}), release: make(chan struct{})}
	manager := connector.NewManager(store)
	t.Cleanup(manager.Close)
	cfg := connector.ConnectorConfig{ID: "tools", Name: "Tools", Type: "mcp", MCP: &connector.MCPConfig{Transport: connector.TransportStdio, Command: "unused"}}
	if err := manager.Create(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	store.fail = true
	cfg.Description = "committed update"
	if err := manager.Update(t.Context(), cfg.ID, cfg); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("expected unconfirmed mutation: %v", err)
	}
	before := manager.Snapshot()
	pendingBefore, _ := manager.PendingMutation(cfg.ID)
	store.pauseRead = true
	done := make(chan error, 1)
	go func() { done <- manager.Recover(t.Context(), cfg.ID) }()
	<-store.readStarted
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	shutdownErr := manager.CloseContext(ctx)
	close(store.release)
	err := <-done
	if shutdownErr != nil {
		t.Fatal(shutdownErr)
	}
	if !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("late recovery lost pending state: %v", err)
	}
	if pending, ok := manager.PendingMutation(cfg.ID); !ok || pending.OperationID != pendingBefore.OperationID {
		t.Fatal("shutdown discarded recovery facts")
	}
	if !reflect.DeepEqual(before, manager.Snapshot()) {
		t.Fatal("recovery published after shutdown")
	}
}

func (s *pausedMutationStore) afterWrite(err error) error {
	if err == nil && s.pause {
		close(s.persisted)
		<-s.release
	}
	return err
}

func (s *pausedMutationStore) Create(ctx context.Context, obj any) error {
	return s.afterWrite(s.testConfigStore.Create(ctx, obj))
}

func (s *pausedMutationStore) Update(ctx context.Context, obj any) error {
	return s.afterWrite(s.testConfigStore.Update(ctx, obj))
}

func (s *pausedMutationStore) Delete(ctx context.Context, keys ...any) error {
	return s.afterWrite(s.testConfigStore.Delete(ctx, keys...))
}

func TestConnectorShutdownFencesCommittedMutationPublication(t *testing.T) {
	for _, action := range []string{"create", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			store := &pausedMutationStore{testConfigStore: newTestConfigStore(t).(*testConfigStore), persisted: make(chan struct{}), release: make(chan struct{})}
			manager := connector.NewManager(store)
			t.Cleanup(manager.Close)
			cfg := connector.ConnectorConfig{ID: "tools", Name: "Tools", Type: "mcp", MCP: &connector.MCPConfig{Transport: connector.TransportStdio, Command: "unused"}}
			if err := manager.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			if action != "create" {
				if err := manager.Create(t.Context(), cfg); err != nil {
					t.Fatal(err)
				}
			}
			before := manager.Snapshot()
			store.pause = true
			done := make(chan error, 1)
			go func() {
				switch action {
				case "create":
					done <- manager.Create(t.Context(), cfg)
				case "update":
					cfg.Description = "committed update"
					done <- manager.Update(t.Context(), cfg.ID, cfg)
				case "delete":
					done <- manager.Delete(t.Context(), cfg.ID)
				}
			}()
			<-store.persisted
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			shutdownErr := manager.CloseContext(ctx)
			close(store.release)
			err := <-done
			if shutdownErr != nil {
				t.Fatal(shutdownErr)
			}
			if !errors.Is(err, configmgr.ErrMutationPending) {
				t.Fatalf("late committed mutation lost recovery state: %v", err)
			}
			if _, pending := manager.PendingMutation(cfg.ID); !pending {
				t.Fatal("late mutation cleared pending recovery")
			}
			if !reflect.DeepEqual(before, manager.Snapshot()) {
				t.Fatal("mutation published after shutdown")
			}
		})
	}
}

func TestDeleteMissingConnectorDoesNotCreatePendingMutation(t *testing.T) {
	for _, transactional := range []bool{false, true} {
		name := "memory"
		if transactional {
			name = "sqlite"
		}
		t.Run(name, func(t *testing.T) {
			store := newTestConfigStore(t)
			if transactional {
				creator, err := sqlite.Open(t.Context(), sqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				backend := configstore.NewBackend(creator)
				t.Cleanup(func() { _ = backend.Close() })
				if err := backend.Register(schema.StoreConnectors, schema.ConnectorSchema); err != nil {
					t.Fatal(err)
				}
				store, err = backend.Get(schema.StoreConnectors)
				if err != nil {
					t.Fatal(err)
				}
			}
			manager := connector.NewManager(store)
			t.Cleanup(manager.Close)
			for range 2 {
				if err := manager.Delete(t.Context(), "missing"); !errors.Is(err, configstore.ErrNotFound) {
					t.Fatalf("missing delete: %v", err)
				}
				if _, pending := manager.PendingMutation("missing"); pending {
					t.Fatal("missing delete requires recovery")
				}
			}
		})
	}
}

func TestHTTPConnectorPersistsDefaultLimits(t *testing.T) {
	for _, transactional := range []bool{false, true} {
		name := "memory"
		if transactional {
			name = "sqlite"
		}
		t.Run(name, func(t *testing.T) {
			store := newTestConfigStore(t)
			if transactional {
				creator, err := sqlite.Open(t.Context(), sqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				backend := configstore.NewBackend(creator)
				t.Cleanup(func() { _ = backend.Close() })
				if err := backend.Register(schema.StoreConnectors, schema.ConnectorSchema); err != nil {
					t.Fatal(err)
				}
				store, err = backend.Get(schema.StoreConnectors)
				if err != nil {
					t.Fatal(err)
				}
			}
			manager := connector.NewManager(store)
			t.Cleanup(manager.Close)
			cfg := httpConnector(t, "https://orders.example.com", "/lookup")
			cfg.HTTPAPI.TimeoutMS = 0
			cfg.HTTPAPI.MaxResponseBytes = 0
			for _, action := range []string{"create", "update"} {
				var err error
				if action == "create" {
					err = manager.Create(t.Context(), cfg)
				} else {
					cfg.Description = "updated"
					err = manager.Update(t.Context(), cfg.ID, cfg)
				}
				if err != nil {
					t.Fatalf("%s with omitted limits: %v", action, err)
				}
				stored, err := store.Get(t.Context(), cfg.ID)
				if err != nil {
					t.Fatal(err)
				}
				got := stored.(*connector.ConnectorConfig)
				if got.HTTPAPI.TimeoutMS != 10000 || got.HTTPAPI.MaxResponseBytes != 1<<20 {
					t.Fatalf("%s persisted unnormalized limits: %+v", action, got.HTTPAPI)
				}
				if _, pending := manager.PendingMutation(cfg.ID); pending {
					t.Fatal("default limits require recovery")
				}
			}
		})
	}
}
