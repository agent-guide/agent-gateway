package credentialmgr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

type testManualRefresher struct {
	refreshFn func(context.Context, *Credential) (*Credential, error)
}

func (r *testManualRefresher) Refresh(ctx context.Context, cred *Credential) (*Credential, error) {
	if r == nil || r.refreshFn == nil {
		return nil, nil
	}
	return r.refreshFn(ctx, cred)
}

func TestPickWithFilterSelectsRequestedSource(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	for _, cred := range []*Credential{
		{ID: "api-key", ProviderType: "openai", ProviderID: "openai", Source: SourceAPIKey},
		{ID: "cliauth", ProviderType: "openai", ProviderID: "openai", Source: SourceCLIAuthToken},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	picked, err := mgr.PickWithFilter(context.Background(), Filter{
		Source:       SourceCLIAuthToken,
		ProviderType: "openai",
		ProviderID:   "openai",
		Model:        "gpt-test",
	}, nil)
	if err != nil {
		t.Fatalf("PickWithFilter returned error: %v", err)
	}
	if picked.ID != "cliauth" {
		t.Fatalf("picked credential = %q, want cliauth", picked.ID)
	}
}

func TestPickWithFilterSelectsRequestedProviderID(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	for _, cred := range []*Credential{
		{ID: "openai-main", ProviderType: "openai", ProviderID: "openai-main", Source: SourceAPIKey},
		{ID: "openai-backup", ProviderType: "openai", ProviderID: "openai-backup", Source: SourceAPIKey},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	picked, err := mgr.PickWithFilter(context.Background(), Filter{
		Source:     SourceAPIKey,
		ProviderID: "openai-main",
		Model:      "gpt-test",
	}, nil)
	if err != nil {
		t.Fatalf("PickWithFilter returned error: %v", err)
	}
	if picked.ID != "openai-main" {
		t.Fatalf("picked credential = %q, want openai-main", picked.ID)
	}
}

func TestPickWithFilterRejectsMissingProviderID(t *testing.T) {
	mgr := NewManager(nil, nil, nil)

	_, err := mgr.PickWithFilter(context.Background(), Filter{
		ProviderType: "openai",
		Model:        "gpt-test",
	}, nil)
	if err == nil {
		t.Fatal("PickWithFilter returned nil error, want provider_id requirement")
	}
}

func TestPickWithFilterReturnsNotFoundWhenFilteredCandidatesAbsent(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	for _, cred := range []*Credential{
		{ID: "api-key", ProviderType: "openai", ProviderID: "openai", Source: SourceAPIKey},
		{ID: "api-key-2", ProviderType: "openai", ProviderID: "openai", Source: SourceAPIKey},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	_, err := mgr.PickWithFilter(context.Background(), Filter{
		Source:       SourceCLIAuthToken,
		ProviderType: "openai",
		ProviderID:   "openai",
		Model:        "gpt-test",
	}, nil)
	if err == nil {
		t.Fatal("PickWithFilter returned nil error, want credential_not_found")
	}

	var credErr *Error
	if !errors.As(err, &credErr) {
		t.Fatalf("PickWithFilter error type = %T, want *Error", err)
	}
	if credErr.Code != "credential_not_found" {
		t.Fatalf("error code = %q, want credential_not_found", credErr.Code)
	}
}

func TestPickWithFilterReturnsCooldownWhenFilteredCandidatesAllCoolingDown(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	for _, cred := range []*Credential{
		{ID: "cli-1", ProviderType: "openai", ProviderID: "openai", Source: SourceCLIAuthToken},
		{ID: "cli-2", ProviderType: "openai", ProviderID: "openai", Source: SourceCLIAuthToken},
		{ID: "api-key", ProviderType: "openai", ProviderID: "openai", Source: SourceAPIKey},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	for _, id := range []string{"cli-1", "cli-2"} {
		mgr.MarkResult(context.Background(), Result{
			CredentialID: id,
			ProviderType: "openai",
			Model:        "gpt-test",
			Error: &Error{
				Message:    "quota exceeded",
				HTTPStatus: http.StatusTooManyRequests,
				Retryable:  true,
			},
		})
	}

	_, err := mgr.PickWithFilter(context.Background(), Filter{
		Source:       SourceCLIAuthToken,
		ProviderType: "openai",
		ProviderID:   "openai",
		Model:        "gpt-test",
	}, nil)
	if err == nil {
		t.Fatal("PickWithFilter returned nil error, want cooldown error")
	}

	type statusCoder interface {
		StatusCode() int
	}
	var sc statusCoder
	if !errors.As(err, &sc) {
		t.Fatalf("PickWithFilter error type = %T, want status code capable error", err)
	}
	if sc.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want %d", sc.StatusCode(), http.StatusTooManyRequests)
	}
	if !strings.Contains(err.Error(), "retry after") {
		t.Fatalf("error message = %q, want retry-after hint", err.Error())
	}
}

func TestPickWithFilterReturnsUnavailableWhenFilteredCandidateBlockedButNotCoolingDown(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	for _, cred := range []*Credential{
		{ID: "cli-1", ProviderType: "openai", ProviderID: "openai", Source: SourceCLIAuthToken},
		{ID: "api-key", ProviderType: "openai", ProviderID: "openai", Source: SourceAPIKey},
	} {
		if err := mgr.RegisterCredential(context.Background(), cred); err != nil {
			t.Fatalf("register %s: %v", cred.ID, err)
		}
	}

	retryAfter := 5 * time.Second
	mgr.MarkResult(context.Background(), Result{
		CredentialID: "cli-1",
		ProviderType: "openai",
		Model:        "gpt-test",
		RetryAfter:   &retryAfter,
		Error: &Error{
			Message:    "upstream temporarily unavailable",
			HTTPStatus: http.StatusServiceUnavailable,
			Retryable:  true,
		},
	})

	_, err := mgr.PickWithFilter(context.Background(), Filter{
		Source:       SourceCLIAuthToken,
		ProviderType: "openai",
		ProviderID:   "openai",
		Model:        "gpt-test",
	}, nil)
	if err == nil {
		t.Fatal("PickWithFilter returned nil error, want credential_unavailable")
	}

	var credErr *Error
	if !errors.As(err, &credErr) {
		t.Fatalf("PickWithFilter error type = %T, want *Error", err)
	}
	if credErr.Code != "credential_unavailable" {
		t.Fatalf("error code = %q, want credential_unavailable", credErr.Code)
	}
}

func TestMarkResultAppliesQuotaCooldown(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	if err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cred-1",
		ProviderType: "openai",
		ProviderID:   "openai",
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

func TestRefreshCredentialIfNeededRefreshesExpiredCLIAuthCredential(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	mgr.SetManualRefresher("codex", &testManualRefresher{
		refreshFn: func(_ context.Context, cred *Credential) (*Credential, error) {
			updated := cred.Clone()
			updated.Attributes["api_key"] = "fresh-key"
			updated.Metadata["expired"] = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
			return updated, nil
		},
	})

	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cli-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       SourceCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "stale-key",
		},
		Metadata: map[string]any{
			MetadataManualRefreshNameKey: "codex",
			"expired":                    expired,
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	updated, err := mgr.RefreshCredentialIfNeeded(context.Background(), "cli-1")
	if err != nil {
		t.Fatalf("RefreshCredentialIfNeeded returned error: %v", err)
	}
	if updated == nil {
		t.Fatal("RefreshCredentialIfNeeded returned nil credential")
	}
	if got := updated.Attributes["api_key"]; got != "fresh-key" {
		t.Fatalf("api_key = %q, want fresh-key", got)
	}
}

func TestRefreshCredentialIfNeededSkipsWhenNoMatchingManualRefresher(t *testing.T) {
	mgr := NewManager(nil, nil, nil)

	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cli-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       SourceCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "stale-key",
		},
		Metadata: map[string]any{
			MetadataManualRefreshNameKey: "codex",
			"expired":                    expired,
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	updated, err := mgr.RefreshCredentialIfNeeded(context.Background(), "cli-1")
	if err != nil {
		t.Fatalf("RefreshCredentialIfNeeded returned error: %v", err)
	}
	if updated == nil {
		t.Fatal("RefreshCredentialIfNeeded returned nil credential")
	}
	if got := updated.Attributes["api_key"]; got != "stale-key" {
		t.Fatalf("api_key = %q, want stale-key", got)
	}
}

func TestRefreshCredentialIfNeededHonorsCredentialSpecificExpiryDelta(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	refreshed := false
	mgr.SetManualRefresher("gemini", &testManualRefresher{
		refreshFn: func(_ context.Context, cred *Credential) (*Credential, error) {
			refreshed = true
			updated := cred.Clone()
			updated.Attributes["api_key"] = "fresh-key"
			return updated, nil
		},
	})

	expiry := time.Now().UTC().Add(20 * time.Second).Format(time.RFC3339)
	if err := mgr.RegisterCredential(context.Background(), &Credential{
		ID:           "cli-1",
		ProviderType: "gemini",
		ProviderID:   "gemini",
		Source:       SourceCLIAuthToken,
		Attributes: map[string]string{
			"api_key": "stale-key",
		},
		Metadata: map[string]any{
			MetadataManualRefreshNameKey:     "gemini",
			MetadataManualRefreshExpiryDelta: "10s",
			"expired":                        expiry,
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	updated, err := mgr.RefreshCredentialIfNeeded(context.Background(), "cli-1")
	if err != nil {
		t.Fatalf("RefreshCredentialIfNeeded returned error: %v", err)
	}
	if updated == nil {
		t.Fatal("RefreshCredentialIfNeeded returned nil credential")
	}
	if refreshed {
		t.Fatal("expected credential-specific delta to skip refresh")
	}
	if got := updated.Attributes["api_key"]; got != "stale-key" {
		t.Fatalf("api_key = %q, want stale-key", got)
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
