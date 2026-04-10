package localapikey

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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

