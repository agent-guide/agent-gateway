package runtimeassembly

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/credential"
	"github.com/agent-guide/agent-gateway/pkg/credential/scheduler"
	"gorm.io/gorm"
)

type usageDatabase struct {
	configstore.ConfigStoreBackend
	db *gorm.DB
}

func (b usageDatabase) Database() *gorm.DB { return b.db }

func testBackend(t *testing.T) *configstore.Backend {
	t.Helper()
	b, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: filepath.Join(t.TempDir(), "config.db")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	backend := configstore.NewBackend(b)
	if err = schema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db, _ := b.Database().DB(); _ = db.Close() })
	return backend
}

func TestCredentialsRebuildSchedulerAndOwnArguments(t *testing.T) {
	b := testBackend(t)
	args := []string{"custom"}
	first, err := NewCredentials(b, " ", args)
	if err != nil {
		t.Fatal(err)
	}
	command, copied := RefreshCommand(" ", args)
	args[0] = "changed"
	if command != credential.DefaultRefreshCommand || copied[0] != "custom" {
		t.Fatalf("command %q args %v", command, copied)
	}
	command, copied = RefreshCommand("", nil)
	if command != credential.DefaultRefreshCommand || len(copied) != 1 || copied[0] != credential.DefaultRefreshCommandArg {
		t.Fatal("default refresh command diverged")
	}
	if err = first.Manager.RegisterCredential(t.Context(), &credential.Credential{ID: "key", ProviderID: "p", ProviderType: "openai", Type: credential.TypeAPIKey, Attributes: map[string]string{"api_key": "test"}}); err != nil {
		t.Fatal(err)
	}
	second, err := NewCredentials(b, "custom-tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = second.Manager.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = second.Scheduler.Pick(t.Context(), scheduler.Filter{CredentialScope: credential.ProviderIDCredentialScope("p")}, nil); err != nil {
		t.Fatalf("loaded credential absent from scheduler: %v", err)
	}
	if err = first.Manager.DeregisterCredential(t.Context(), "key"); err != nil {
		t.Fatal(err)
	}
	if second.Manager.GetCredential("key") == nil {
		t.Fatal("independent lifecycles share manager state")
	}
}

func TestUsagePersistenceAndDegradation(t *testing.T) {
	for _, mode := range []string{"persistent", "no SQL capability", "SQL initialization failure"} {
		t.Run(mode, func(t *testing.T) {
			b := testBackend(t)
			var backend configstore.ConfigStoreBackend = b
			if mode == "no SQL capability" {
				backend = struct{ configstore.ConfigStoreBackend }{b}
			}
			if mode == "SQL initialization failure" {
				broken := testBackend(t)
				db, _ := broken.Database().DB()
				_ = db.Close()
				backend = usageDatabase{ConfigStoreBackend: b, db: broken.Database()}
			}
			svc := NewUsage(backend, nil, usage.Config{OTLP: usage.OTLPConfig{Endpoint: "http://127.0.0.1:4318", Protocol: "invalid"}})
			t.Cleanup(func() { _ = svc.Close() })
			persistent := mode == "persistent"
			if (svc.Query() != nil) != persistent || (svc.Prometheus() != nil) != persistent {
				t.Fatal("degradation changed query/Prometheus availability")
			}
			svc.Start()
			span, _ := svc.Observer().Begin(context.Background(), usage.InteractionDimensions{RouteKind: "agent", RouteProtocol: "a2a", RuntimeType: "http", RouteID: "route", AgentID: "agent"})
			span.Finish(usage.InteractionOutcome{Success: true, StatusCode: 200})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := svc.CloseContext(ctx); err != nil {
				t.Fatal(err)
			}
			if persistent {
				summary, err := svc.Query().Summary()
				if err != nil || summary.A2A.RequestCount != 1 {
					t.Fatalf("event not persisted: %+v, %v", summary, err)
				}
			}
			credentials, err := NewCredentials(backend, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = credentials.Manager.Load(t.Context()); err != nil {
				t.Fatalf("usage fallback broke config storage: %v", err)
			}
		})
	}
}
