package contracttest

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/extension"
)

func TestSecretReferencesExercisesAbsenceAndRestoresEnvironment(t *testing.T) {
	const variable = "AGW_SECRET_REFERENCE_CONTRACT_REGRESSION"
	const secret = "resolved-contract-value"
	t.Setenv(variable, "original")
	var present []bool
	var values []string
	f := &extension.Factory{SecretFields: []string{"/token"}, Validate: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		value, ok := os.LookupEnv(variable)
		present = append(present, ok)
		values = append(values, value)
		return raw, ctx.Err()
	}, Prepare: func(ctx context.Context, c extension.PreparedConfig, _ extension.Snapshot) (extension.Resource, error) {
		if c.Secrets["/token"] != secret {
			t.Error("secret resolution failed")
		}
		return secretTestResource{}, ctx.Err()
	}}
	regs := []extension.Registration{{Descriptor: extension.Descriptor{ID: "example.secret.contract", Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime", "validation"}}, Factory: f}}
	input := []extension.Envelope{{ModuleID: "example.secret.contract", SchemaVersion: 1, Config: json.RawMessage(`{"token":{"$secret_ref":"env:AGW_SECRET_REFERENCE_CONTRACT_REGRESSION"}}`)}}
	t.Run("contract", func(t *testing.T) { SecretReferences(t, regs, input, variable, secret) })
	if len(present) < 2 || present[0] || !present[1] || values[1] != secret {
		t.Fatalf("absent/present phases: %v %v", present, values)
	}
	if value, ok := os.LookupEnv(variable); !ok || value != "original" {
		t.Fatal("contract did not restore caller environment")
	}
}

type secretTestResource struct{}

func (secretTestResource) Health(ctx context.Context) error { return ctx.Err() }
func (secretTestResource) Close(ctx context.Context) error  { return ctx.Err() }
