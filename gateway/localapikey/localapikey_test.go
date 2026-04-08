package localapikey

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testLocalAPIKeyStore struct {
	get func(context.Context, string) (any, error)
}

func (s testLocalAPIKeyStore) ListByUserID(context.Context, string) ([]any, error) { return nil, nil }
func (s testLocalAPIKeyStore) Create(context.Context, string, string, any) error   { return nil }
func (s testLocalAPIKeyStore) Update(context.Context, string, any) error           { return nil }
func (s testLocalAPIKeyStore) Delete(context.Context, string) error                { return nil }
func (s testLocalAPIKeyStore) Get(ctx context.Context, key string) (any, error) {
	return s.get(ctx, key)
}

func TestValidateForRouteUsesAllowedRouteIDsOnly(t *testing.T) {
	key, err := ValidateForRoute("chat-prod", true, &LocalAPIKey{
		Key:             "lk-test",
		AllowedRouteIDs: []string{"chat-prod"},
	})
	if err != nil {
		t.Fatalf("ValidateForRoute returned error: %v", err)
	}
	if key == nil || key.Key != "lk-test" {
		t.Fatalf("unexpected local api key: %#v", key)
	}
}

func TestValidateForRouteRejectsUnlistedRoute(t *testing.T) {
	_, err := ValidateForRoute("chat-prod", true, &LocalAPIKey{
		Key:             "lk-test",
		AllowedRouteIDs: []string{"embeddings"},
	})
	if err == nil {
		t.Fatal("ValidateForRoute returned nil error, want route rejection")
	}
}

func TestExtractAPIKeyFromBearerToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer lk-test")
	if got := ExtractAPIKey(req); got != "lk-test" {
		t.Fatalf("ExtractAPIKey = %q, want %q", got, "lk-test")
	}
}

func TestAuthenticateRequestRequiresLocalAPIKeyWhenRouteDemandsIt(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	_, err := AuthenticateRequest(context.Background(), nil, req, "chat-prod", true)
	if err == nil {
		t.Fatal("AuthenticateRequest returned nil error, want auth failure")
	}
}

func TestAuthenticateRequestLoadsAndValidatesLocalAPIKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("x-api-key", "lk-test")

	key, err := AuthenticateRequest(context.Background(), testLocalAPIKeyStore{
		get: func(context.Context, string) (any, error) {
			return &LocalAPIKey{
				Key:             "lk-test",
				AllowedRouteIDs: []string{"chat-prod"},
			}, nil
		},
	}, req, "chat-prod", true)
	if err != nil {
		t.Fatalf("AuthenticateRequest returned error: %v", err)
	}
	if key == nil || key.Key != "lk-test" {
		t.Fatalf("unexpected local api key: %#v", key)
	}
}
