package credential

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

type failedCredentialDelete struct{ configstore.ConfigStore }

func (failedCredentialDelete) Delete(context.Context, ...any) error {
	return errors.New("injected delete failure")
}

func TestFailedPersistentDeleteRetainsCredentialAndDoesNotNotify(t *testing.T) {
	m := NewManager(failedCredentialDelete{&testCredentialStore{}})
	cred := &Credential{ID: "key", ProviderID: "p", ProviderType: "openai", Type: TypeAPIKey}
	if err := m.RegisterCredential(WithSkipPersist(t.Context()), cred); err != nil {
		t.Fatal(err)
	}
	listener := &testCredentialLifecycleListener{}
	m.AddListener(listener)
	if err := m.DeregisterCredential(t.Context(), cred.ID); err == nil {
		t.Fatal("delete claimed success")
	}
	if m.GetCredential(cred.ID) == nil {
		t.Fatal("failed persistent delete removed runtime credential")
	}
	if len(listener.deregistered) != 0 {
		t.Fatal("failed deletion published credential revocation")
	}
}
