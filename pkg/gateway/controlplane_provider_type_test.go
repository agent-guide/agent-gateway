package gateway

import (
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/credential"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

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
