package gateway

import (
	"errors"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/credential"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func TestCredentialGuardRejectsAmbiguousProviderOwnership(t *testing.T) {
	g, _, _ := controlPlaneGateway(t)
	for _, id := range []string{"TeamA", "teama"} {
		if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: id, ProviderType: "openai"}); err != nil {
			t.Fatal(err)
		}
	}
	err := g.CredentialManager().RegisterCredential(t.Context(), &credential.Credential{ID: "key", ProviderID: "TeamA", ProviderType: "openai", Type: credential.TypeAPIKey})
	if !errors.Is(err, ErrDependencyNotConfigured) || !strings.Contains(err.Error(), "multiple Provider IDs") {
		t.Fatalf("ambiguous credential owner = %v", err)
	}
	if g.CredentialManager().GetCredential("key") != nil {
		t.Fatal("ambiguous credential was published")
	}
}

func TestCredentialGuardResolvesCanonicalProviderID(t *testing.T) {
	g, backend, _ := controlPlaneGateway(t)
	if err := g.ProviderManager().CreateConfig(t.Context(), provider.ProviderConfig{Id: "TeamA", ProviderType: "openai"}); err != nil {
		t.Fatal(err)
	}
	cred := &credential.Credential{ID: "key", ProviderID: "TeamA", ProviderType: "openai", Type: credential.TypeAPIKey, Attributes: map[string]string{"api_key": "test"}}
	if err := g.CredentialManager().RegisterCredential(t.Context(), cred); err != nil {
		t.Fatal(err)
	}
	stored := g.CredentialManager().GetCredential("key")
	if stored.ProviderID != "teama" {
		t.Fatal("fixture did not normalize credential owner")
	}
	stored.Label = "updated"
	if err := g.CredentialManager().UpdateCredential(t.Context(), &stored.Credential); err != nil {
		t.Fatalf("update normalized credential: %v", err)
	}
	if err := g.CredentialManager().ReloadFromStore(t.Context()); err != nil {
		t.Fatalf("reload normalized credential: %v", err)
	}
	g.control.backend = &uncertainProviderTransaction{ConfigStoreBackend: backend, uncertain: true}
	if err := g.ProviderManager().UpdateConfig(t.Context(), "TeamA", provider.ProviderConfig{ProviderType: "openai"}); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("uncertain Provider update = %v", err)
	}
	if err := g.CredentialManager().UpdateCredential(t.Context(), &stored.Credential); !errors.Is(err, configmgr.ErrMutationPending) {
		t.Fatalf("canonical owner pending gate bypassed: %v", err)
	}
}

func TestProviderCredentialGuardsAcceptEquivalentTypeSpellings(t *testing.T) {
	for _, spelling := range []string{"OPENAI", " openai "} {
		t.Run(spelling, func(t *testing.T) {
			g, _, _ := controlPlaneGateway(t)
			cfg := provider.ProviderConfig{Id: "p", ProviderType: "openai"}
			if err := g.ProviderManager().CreateConfig(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}
			register := func(id string) error {
				return g.CredentialManager().RegisterCredential(t.Context(), &credential.Credential{
					ID: id, ProviderID: "p", ProviderType: "openai", Type: credential.TypeAPIKey,
					Attributes: map[string]string{"api_key": "test"},
				})
			}
			if err := register("before"); err != nil {
				t.Fatal(err)
			}
			cfg.ProviderType = spelling
			if err := g.ProviderManager().UpdateConfig(t.Context(), cfg.Id, cfg); err != nil {
				t.Fatalf("equivalent type update: %v", err)
			}
			if err := register("after"); err != nil {
				t.Fatalf("credential after type update: %v", err)
			}
		})
	}
}
