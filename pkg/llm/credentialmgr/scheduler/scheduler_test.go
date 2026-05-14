package scheduler

import (
	"context"
	"testing"
)

func TestPickUsesCredentialScope(t *testing.T) {
	s := NewScheduler(nil)
	s.RegisterCredential(&ManagedCredential{
		Credential: Credential{
			ID:           "openai-main",
			ProviderType: "openai",
			ProviderID:   "openai-main",
			Scope:        "id:openai-main",
			Type:         "api_key",
		},
	})

	picked, err := s.Pick(context.Background(), Filter{
		CredentialScope: "id:openai-main",
		Model:           "gpt-test",
	}, nil)
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if picked == nil || picked.ID != "openai-main" {
		t.Fatalf("picked credential = %#v, want openai-main", picked)
	}
}

func TestPickUsesExplicitCredentialScope(t *testing.T) {
	s := NewScheduler(nil)
	s.RegisterCredential(&ManagedCredential{
		Credential: Credential{
			ID:           "openai-shared",
			ProviderType: "openai",
			ProviderID:   "openai-main",
			Scope:        "type:openai",
			Type:         "api_key",
		},
	})

	picked, err := s.Pick(context.Background(), Filter{
		CredentialScope: "type:openai",
		Model:           "gpt-test",
	}, nil)
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if picked == nil || picked.ID != "openai-shared" {
		t.Fatalf("picked credential = %#v, want openai-shared", picked)
	}
}
