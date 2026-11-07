package gateway

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	agentruntime "github.com/agent-guide/agent-gateway/pkg/agent/runtime"
	"github.com/agent-guide/agent-gateway/pkg/agent/runtime/runtimetest"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	configschema "github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configstoresqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
)

func TestAgentGatewayOwnsRuntimeRegistry(t *testing.T) {
	t.Parallel()

	gateway := NewAgentGateway()
	if gateway.RuntimeRegistry() == nil {
		t.Fatal("RuntimeRegistry() = nil")
	}
}

func TestAgentGatewayBootstrapRegistersLinkedNativeBackends(t *testing.T) {
	ctx := t.Context()
	store, err := configstore.OpenBackend(ctx, "sqlite", configstoresqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatalf("OpenBackend: %v", err)
	}
	if err := configschema.RegisterDefaultStores(store); err != nil {
		t.Fatalf("RegisterDefaultStores: %v", err)
	}
	gateway := NewAgentGateway()
	if err := gateway.Bootstrap(ctx, BootstrapOptions{ConfigStoreBackend: store}); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	got := gateway.RuntimeRegistry().RuntimeTypes()
	if len(got) != 3 || got[0] != agent.RuntimeTypeACP || got[1] != agent.RuntimeTypeBuiltin || got[2] != agent.RuntimeTypeHTTP {
		t.Fatalf("runtime types = %v, want [acp builtin http]", got)
	}
}

func TestAgentGatewayResetReplacesRuntimeRegistry(t *testing.T) {
	t.Parallel()

	gateway := NewAgentGateway()
	backend := runtimetest.NewBackend("fake")
	if err := gateway.RuntimeRegistry().Register(backend); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	old := gateway.RuntimeRegistry()
	gateway.Reset()
	current := gateway.RuntimeRegistry()
	if current == nil || current == old {
		t.Fatal("Reset() did not install a fresh runtime registry")
	}
	if _, err := current.Resolve("fake"); !errors.Is(err, agentruntime.ErrRuntimeNotExecutable) {
		t.Fatalf("Resolve(fake) after Reset error = %v", err)
	}
}

// Gateway owns orchestration; concrete backend continuation storage is covered
// inside runtimebackend without introducing a reverse dependency on this package.
type shutdownBackend struct {
	*runtimetest.Backend
	order []string
}

func (b *shutdownBackend) DiscardAllContinuations() { b.order = append(b.order, "discard") }
func (*shutdownBackend) ValidateContinuationDecision(string, agentruntime.PendingPermission, agentruntime.PermissionDecision) error {
	return nil
}
func (*shutdownBackend) ResolveContinuation(context.Context, string, agentruntime.PermissionDecision, time.Time) error {
	return nil
}
func (b *shutdownBackend) ExpireContinuation(context.Context, string) error {
	b.order = append(b.order, "expire")
	return nil
}

func TestAgentGatewayResetDrainsBeforeBackendDiscard(t *testing.T) {
	g := NewAgentGateway()
	old := g.PermissionBroker()
	backend := &shutdownBackend{Backend: runtimetest.NewBackend(agent.RuntimeTypeBuiltin)}
	if err := g.RuntimeRegistry().Register(backend); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Register(agentruntime.PendingPermission{RequestID: "permission", AgentID: "a", RuntimeType: agent.RuntimeTypeBuiltin, RunID: "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExpiresAt: time.Now().Add(time.Hour)}, "continuation", backend); err != nil {
		t.Fatal(err)
	}
	g.Reset()
	if !reflect.DeepEqual(backend.order, []string{"expire", "discard"}) {
		t.Fatalf("shutdown order: %v", backend.order)
	}
	if g.PermissionBroker() == old || len(old.List("a")) != 0 {
		t.Fatal("broker ownership was not retired")
	}
	if _, err := old.Register(agentruntime.PendingPermission{RequestID: "late", AgentID: "a", RuntimeType: agent.RuntimeTypeBuiltin, RunID: "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExpiresAt: time.Now().Add(time.Hour)}, "late", backend); !errors.Is(err, agentruntime.ErrBackendUnavailable) {
		t.Fatalf("old broker admitted continuation: %v", err)
	}
}
