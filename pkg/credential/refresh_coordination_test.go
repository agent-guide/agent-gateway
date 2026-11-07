package credential

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
)

func expiringTestCredential() *Credential {
	return &Credential{ID: "key", ProviderID: "p", ProviderType: "openai", Type: TypeOAuthToken, Attributes: map[string]string{"api_key": "old"}, Metadata: map[string]any{"expired": time.Now().Add(-time.Hour).Format(time.RFC3339)}}
}

func TestSecretRefreshDoesNotReenterOwnershipCoordination(t *testing.T) {
	m := NewManager(nil)
	if err := m.RegisterCredential(t.Context(), expiringTestCredential()); err != nil {
		t.Fatal(err)
	}
	var ownershipUpdates atomic.Int32
	rejected := errors.New("ownership coordinator must not be reentered")
	m.SetMutationGuard(func(ctx context.Context, action configmgr.MutationAction, _ string, _ *Credential, apply func(context.Context) error) error {
		if action == configmgr.MutationUpdate {
			ownershipUpdates.Add(1)
			return rejected
		}
		return apply(ctx)
	})
	m.SetRefresher(&testRefresher{refreshFn: func(_ context.Context, c *Credential) (*Credential, error) {
		c.Attributes["api_key"] = "fresh"
		c.Metadata["expired"] = time.Now().Add(time.Hour).Format(time.RFC3339)
		return c, nil
	}})
	got, err := m.RefreshCredentialIfNeeded(t.Context(), "key")
	if err != nil || got.APIKey() != "fresh" || ownershipUpdates.Load() != 0 {
		t.Fatalf("token-only refresh entered configuration coordination: %v, %v", got, err)
	}
	if err := m.UpdateCredential(t.Context(), got.Credential.Clone()); !errors.Is(err, rejected) || ownershipUpdates.Load() != 1 {
		t.Fatal("ordinary ownership writes bypassed coordination")
	}
}

func TestCredentialRevocationCannotBeUndoneByConcurrentRefresh(t *testing.T) {
	m := NewManager(nil)
	if err := m.RegisterCredential(t.Context(), expiringTestCredential()); err != nil {
		t.Fatal(err)
	}
	started, release, deleteEntered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	m.SetMutationGuard(func(ctx context.Context, action configmgr.MutationAction, _ string, _ *Credential, apply func(context.Context) error) error {
		if action == configmgr.MutationDelete {
			close(deleteEntered)
		}
		return apply(ctx)
	})
	m.SetRefresher(&testRefresher{refreshFn: func(_ context.Context, c *Credential) (*Credential, error) {
		close(started)
		<-release
		c.Attributes["api_key"] = "fresh"
		c.Metadata["expired"] = time.Now().Add(time.Hour).Format(time.RFC3339)
		return c, nil
	}})
	refreshed, deleted := make(chan error, 1), make(chan error, 1)
	go func() { _, err := m.RefreshCredentialIfNeeded(t.Context(), "key"); refreshed <- err }()
	<-started
	go func() { deleted <- m.DeregisterCredential(t.Context(), "key") }()
	<-deleteEntered
	select {
	case err := <-deleted:
		close(release)
		t.Fatalf("revocation raced token rotation: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-refreshed; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if m.GetCredential("key") != nil {
		t.Fatal("token rotation resurrected a revoked credential")
	}
}
