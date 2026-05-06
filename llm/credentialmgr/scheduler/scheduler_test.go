package scheduler

import (
	"context"
	"testing"
)

func TestNewSchedulerDefaultsProviderScopeToProviderID(t *testing.T) {
	s := NewScheduler("", nil)
	s.RegisterCredential(&ManagedCredential{
		Credential: Credential{
			ID:           "openai-main",
			ProviderType: "openai",
			ProviderID:   "openai-main",
			Source:       "api_key",
		},
	})

	picked, err := s.Pick(context.Background(), Filter{
		ProviderID: "openai-main",
		Model:      "gpt-test",
	}, nil)
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if picked == nil || picked.ID != "openai-main" {
		t.Fatalf("picked credential = %#v, want openai-main", picked)
	}
}

func TestPickUsesProviderTypeScopeWhenConfigured(t *testing.T) {
	s := NewScheduler(ProviderScopeProviderType, nil)
	s.RegisterCredential(&ManagedCredential{
		Credential: Credential{
			ID:           "openai-main",
			ProviderType: "openai",
			ProviderID:   "openai-main",
			Source:       "api_key",
		},
	})

	picked, err := s.Pick(context.Background(), Filter{
		ProviderType: "openai",
		Model:        "gpt-test",
	}, nil)
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if picked == nil || picked.ID != "openai-main" {
		t.Fatalf("picked credential = %#v, want openai-main", picked)
	}
}
