package virtualkey

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractAPIKeysFromBearerToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer lk-test")
	if got := ExtractAPIKeys(req); len(got) != 1 || got[0] != "lk-test" {
		t.Fatalf("ExtractAPIKeys = %#v, want [lk-test]", got)
	}
}

func TestExtractAPIKeysReturnsBothHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", "sk-unrelated")
	req.Header.Set("Authorization", "Bearer vk-real")
	got := ExtractAPIKeys(req)
	if len(got) != 2 || got[0] != "sk-unrelated" || got[1] != "vk-real" {
		t.Fatalf("ExtractAPIKeys = %#v, want [sk-unrelated vk-real]", got)
	}
}

func TestExtractAPIKeysDedupesAndSkipsEmpty(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", "vk-same")
	req.Header.Set("Authorization", "Bearer vk-same")
	if got := ExtractAPIKeys(req); len(got) != 1 || got[0] != "vk-same" {
		t.Fatalf("ExtractAPIKeys = %#v, want [vk-same]", got)
	}

	none := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if got := ExtractAPIKeys(none); got != nil {
		t.Fatalf("ExtractAPIKeys = %#v, want nil", got)
	}
}
