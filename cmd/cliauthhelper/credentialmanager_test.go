package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
)

func TestCredentialManagerPersistsRoundTrip(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "credentials.json")

	mgr, err := NewCredentialManager(storePath)
	if err != nil {
		t.Fatalf("NewCredentialManager() error = %v", err)
	}

	cred := &credentialmgr.Credential{
		ID:           "cred-1",
		ProviderType: "openai",
		ProviderID:   "openai-main",
		Source:       credentialmgr.SourceCLIAuthToken,
		Label:        "OpenAI Login",
		Metadata: map[string]any{
			"access_token": "secret",
		},
	}
	if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
		t.Fatalf("RegisterCredential() error = %v", err)
	}

	reloaded, err := NewCredentialManager(storePath)
	if err != nil {
		t.Fatalf("reload NewCredentialManager() error = %v", err)
	}

	got := reloaded.GetCredential("cred-1")
	if got == nil {
		t.Fatal("GetCredential() returned nil")
	}
	if got.ProviderType != "openai" || got.ProviderID != "openai-main" {
		t.Fatalf("reloaded credential = %+v", got)
	}
	if got.Metadata["access_token"] != "secret" {
		t.Fatalf("access_token = %#v, want secret", got.Metadata["access_token"])
	}
}

func TestCredentialManagerListCredentialsAppliesFilter(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "credentials.json")
	mgr, err := NewCredentialManager(storePath)
	if err != nil {
		t.Fatalf("NewCredentialManager() error = %v", err)
	}

	for _, cred := range []*credentialmgr.Credential{
		{ID: "cli-1", ProviderType: "openai", ProviderID: "openai", Source: credentialmgr.SourceCLIAuthToken},
		{ID: "api-1", ProviderType: "openai", ProviderID: "openai", Source: credentialmgr.SourceAPIKey},
		{ID: "cli-2", ProviderType: "anthropic", ProviderID: "anthropic", Source: credentialmgr.SourceCLIAuthToken},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("RegisterCredential(%s) error = %v", cred.ID, err)
		}
	}

	got := mgr.ListCredentials(credentialmgr.Filter{
		Source:       credentialmgr.SourceCLIAuthToken,
		ProviderType: "openai",
	})
	if len(got) != 1 || got[0].ID != "cli-1" {
		t.Fatalf("ListCredentials() = %#v, want only cli-1", got)
	}
}

func TestCredentialManagerUpdateMissingReturnsNotFound(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "credentials.json")
	mgr, err := NewCredentialManager(storePath)
	if err != nil {
		t.Fatalf("NewCredentialManager() error = %v", err)
	}

	err = mgr.UpdateCredential(context.Background(), &credentialmgr.Credential{
		ID:           "missing",
		ProviderType: "openai",
	})
	if !errors.Is(err, configstoreintf.ErrNotFound) {
		t.Fatalf("UpdateCredential() error = %v, want ErrNotFound", err)
	}
}
