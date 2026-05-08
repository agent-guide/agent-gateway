package virtualkey

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractAPIKeyFromBearerToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer lk-test")
	if got := ExtractAPIKey(req); got != "lk-test" {
		t.Fatalf("ExtractAPIKey = %q, want %q", got, "lk-test")
	}
}
