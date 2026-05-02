package gatewayadmin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientLoginParsesToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/auth/login" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token":    "session-token",
			"username": "default",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "default", "dummy")
	token, err := client.login()
	if err != nil {
		t.Fatalf("login returned error: %v", err)
	}
	if token != "session-token" {
		t.Fatalf("token = %q, want %q", token, "session-token")
	}
}

func TestClientLoginIncludesMalformedResponseBodyInError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/auth/login" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"username":"default"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "default", "dummy")
	_, err := client.login()
	if err == nil {
		t.Fatal("login returned nil error, want parse failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing token") {
		t.Fatalf("error = %q, want missing token detail", msg)
	}
	if !strings.Contains(msg, `body="{\"username\":\"default\"}"`) {
		t.Fatalf("error = %q, want raw response body", msg)
	}
}
