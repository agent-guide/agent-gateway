package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestOpenAIEndpointURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		base string
		want string
	}{
		{base: "http://127.0.0.1:8080/v1", want: "http://127.0.0.1:8080/v1/responses"},
		{base: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080/v1/responses"},
	}
	for _, tc := range cases {
		if got := openAIEndpointURL(tc.base, "/responses"); got != tc.want {
			t.Fatalf("openAIEndpointURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestAnthropicEndpointURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		base string
		want string
	}{
		{base: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080/v1/messages"},
		{base: "http://127.0.0.1:8080/v1", want: "http://127.0.0.1:8080/v1/messages"},
	}
	for _, tc := range cases {
		if got := anthropicEndpointURL(tc.base, "/messages"); got != tc.want {
			t.Fatalf("anthropicEndpointURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestDefaultEnvValues(t *testing.T) {
	t.Setenv("AGENT_GATEWAY_MAX_TOKENS", "321")
	t.Setenv("AGENT_GATEWAY_TIMEOUT", "45.5")

	if got := defaultMaxTokens(); got != 321 {
		t.Fatalf("defaultMaxTokens() = %d, want 321", got)
	}
	if got := defaultTimeoutSeconds(); got != 45.5 {
		t.Fatalf("defaultTimeoutSeconds() = %v, want 45.5", got)
	}
}

func TestRunResponsesUsesResponsesEndpoint(t *testing.T) {
	t.Parallel()

	var (
		gotPath          string
		gotAuth          string
		gotAPIKey        string
		gotContentType   string
		gotMaxOutput     int
		gotInputMessages []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		gotContentType = r.Header.Get("Content-Type")

		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal request body: %v", err)
		}
		gotMaxOutput = int(payload["max_output_tokens"].(float64))
		for _, raw := range payload["input"].([]any) {
			gotInputMessages = append(gotInputMessages, raw.(map[string]any))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output_text":"done","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`))
	}))
	defer srv.Close()

	chatBaseURL = srv.URL
	chatAPIKey = "vk-test"
	chatModel = "gpt-4.1"
	chatSystem = "system prompt"
	chatStream = false
	chatMaxTokens = 77
	chatTimeout = 5

	if err := runResponses("hello"); err != nil {
		t.Fatalf("runResponses: %v", err)
	}

	if gotPath != "/v1/responses" {
		t.Fatalf("path = %q, want /v1/responses", gotPath)
	}
	if gotAuth != "Bearer vk-test" {
		t.Fatalf("Authorization = %q, want Bearer vk-test", gotAuth)
	}
	if gotAPIKey != "vk-test" {
		t.Fatalf("x-api-key = %q, want vk-test", gotAPIKey)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotMaxOutput != 77 {
		t.Fatalf("max_output_tokens = %d, want 77", gotMaxOutput)
	}
	if len(gotInputMessages) != 2 {
		t.Fatalf("input message count = %d, want 2", len(gotInputMessages))
	}
}

func TestPrintResponsesResponseOutputFallback(t *testing.T) {
	var out bytes.Buffer
	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&out, r)
		close(done)
	}()

	body := `{"output":[{"type":"message","content":[{"type":"output_text","text":"fallback text"}]}]}`
	if err := printResponsesResponse(bytes.NewBufferString(body)); err != nil {
		t.Fatalf("printResponsesResponse: %v", err)
	}

	_ = w.Close()
	<-done

	if got := out.String(); got != "fallback text\n" {
		t.Fatalf("stdout = %q, want %q", got, "fallback text\n")
	}
}
