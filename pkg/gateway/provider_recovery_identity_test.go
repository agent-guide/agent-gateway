package gateway

import (
	"errors"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func TestProviderRecoveryRejectsMismatchedAuthoritativeID(t *testing.T) {
	g, backend, creator := controlPlaneGateway(t)
	m := g.ProviderManager()
	if err := m.CreateConfig(t.Context(), provider.ProviderConfig{Id: "q", ProviderType: "openai", DefaultModel: "keep-q"}); err != nil {
		t.Fatal(err)
	}
	g.control.backend = &uncertainProviderTransaction{ConfigStoreBackend: backend, uncertain: true}
	if err := m.CreateConfig(t.Context(), provider.ProviderConfig{Id: "p", ProviderType: "openai", DefaultModel: "after"}); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("uncertain create = %v", err)
	}
	pending := m.PendingMutation("p")
	if err := creator.UsageDB().Exec(`UPDATE providers SET config=? WHERE id=?`, `{"id":"q","provider_type":"openai","default_model":"corrupt"}`, "p").Error; err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(t.Context(), "p"); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("mismatched recovery = %v", err)
	}
	if state := m.PendingMutation("p"); state == nil || state.OperationID != pending.OperationID || state.RecoveryAttempts != 1 || state.RetryAfter.IsZero() {
		t.Fatalf("lost recovery state: %+v", state)
	}
	if _, err := m.ResolveProvider(t.Context(), "p"); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatal("mismatched recovery admitted execution")
	}
	if cfg, err := m.GetConfig(t.Context(), "q"); err != nil || cfg.DefaultModel != "keep-q" {
		t.Fatalf("recovery corrupted another Provider: %+v, %v", cfg, err)
	}
	store, _ := backend.Get(schema.StoreProviders)
	if err := store.Update(t.Context(), &provider.ProviderConfig{Id: "p", ProviderType: "openai", DefaultModel: "after"}); err != nil {
		t.Fatal(err)
	}
	m.recoveryClock = func() time.Time { return time.Now().Add(time.Minute) }
	if err := m.Recover(t.Context(), "p"); err != nil {
		t.Fatal(err)
	}
	if cfg, err := m.GetConfig(t.Context(), "p"); err != nil || cfg.DefaultModel != "after" || m.PendingMutation("p") != nil {
		t.Fatalf("corrected recovery = %+v, %v", cfg, err)
	}
}
