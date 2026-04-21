package credentialmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestPickWithFilterSelectsRequestedSource(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	for _, cred := range []*Credential{
		{ID: "api-key", ProviderType: "openai", Source: SourceAPIKey},
		{ID: "cliauth", ProviderType: "openai", Source: SourceCLIAuth},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	picked, err := mgr.PickWithFilter(context.Background(), "openai", "gpt-test", nil, Filter{Source: SourceCLIAuth})
	if err != nil {
		t.Fatalf("PickWithFilter returned error: %v", err)
	}
	if picked.ID != "cliauth" {
		t.Fatalf("picked credential = %q, want cliauth", picked.ID)
	}
}

func TestMarkResultAppliesQuotaCooldown(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	if err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cred-1",
		ProviderType: "openai",
		Source:       SourceAPIKey,
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	mgr.MarkResult(context.Background(), Result{
		CredentialID: "cred-1",
		ProviderType: "openai",
		Model:        "gpt-test",
		Error: &Error{
			Message:    "quota exceeded",
			HTTPStatus: http.StatusTooManyRequests,
			Retryable:  true,
		},
	})

	cred := mgr.GetCredential("cred-1")
	if cred == nil {
		t.Fatal("credential not found")
	}
	if !cred.Unavailable || !cred.Quota.Exceeded || cred.NextRetryAfter.IsZero() {
		t.Fatalf("credential was not marked cooling down: %+v", cred)
	}
	state := cred.ModelStates["gpt-test"]
	if state == nil || !state.Unavailable || !state.Quota.Exceeded {
		t.Fatalf("model state was not marked cooling down: %+v", state)
	}
}

func TestCredentialMarshalsProviderTypeAndProviderID(t *testing.T) {
	cred := &Credential{
		ID:           "cred-1",
		ProviderType: "zhipu",
		ProviderID:   "zhipu-test",
	}

	raw, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if got["provider_type"] != "zhipu" {
		t.Fatalf("provider_type = %#v, want zhipu", got["provider_type"])
	}
	if got["provider_id"] != "zhipu-test" {
		t.Fatalf("provider_id = %#v, want zhipu-test", got["provider_id"])
	}
	if _, exists := got["provider"]; exists {
		t.Fatalf("legacy provider field should not be emitted: %#v", got)
	}
}
