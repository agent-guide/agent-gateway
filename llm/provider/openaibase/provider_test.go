package openaibase

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/agent-guide/caddy-agent-gateway/pkg/httpclient"
)

func TestBaseUsesEmbeddedProviderConfig(t *testing.T) {
	var hit bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.URL.Path != "/models" {
			t.Fatalf("request path = %q, want /models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	}))
	defer server.Close()

	cfg := provider.ProviderConfig{
		APIKey:       "test-key",
		BaseURL:      "http://127.0.0.1:1",
		Network:      httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
		AuthStrategy: provider.AuthStrategyManagedAPIKeyFirst,
	}
	base := NewBase(cfg)

	base.BaseURL = server.URL
	models, err := base.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if !hit {
		t.Fatal("server was not called")
	}
	if len(models) != 1 || models[0].ID != "test-model" {
		t.Fatalf("models = %#v, want test-model", models)
	}
}

func TestBaseEmbeddingUsesCredentialOverrideForAPIKeyAndBaseURL(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("request path = %q, want /embeddings", r.URL.Path)
		}
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}],"model":"text-embedding-3-large","usage":{"prompt_tokens":1,"completion_tokens":0}}`))
	}))
	defer server.Close()

	base := NewBase(provider.ProviderConfig{
		APIKey:       "static-key",
		BaseURL:      "https://static.example/v1",
		Network:      httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
		AuthStrategy: provider.AuthStrategyManagedAPIKeyFirst,
	})

	ctx := provider.WithCredential(context.Background(), providerCredential{
		apiKey:  "managed-key",
		baseURL: server.URL,
	}.toCredential())
	resp, err := base.Embedding(ctx, &provider.EmbeddingRequest{
		Model: "text-embedding-3-large",
		Texts: []string{"hello"},
	})
	if err != nil {
		t.Fatalf("Embedding() error = %v", err)
	}
	if resp == nil || resp.Model != "text-embedding-3-large" || len(resp.Embeddings) != 1 {
		t.Fatalf("unexpected embedding response: %+v", resp)
	}
	if authHeader != "Bearer managed-key" {
		t.Fatalf("authorization = %q, want Bearer managed-key", authHeader)
	}
}

func TestBaseCreateResponseCallsResponsesEndpoint(t *testing.T) {
	var body string
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("request path = %q, want /responses", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("request method = %q, want POST", r.Method)
		}
		authHeader = r.Header.Get("Authorization")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","created_at":1,"model":"gpt-4.1","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	base := NewBase(provider.ProviderConfig{
		APIKey:       "test-key",
		BaseURL:      server.URL,
		Network:      httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
		AuthStrategy: provider.AuthStrategyManagedAPIKeyFirst,
	})

	ctx := provider.WithCredential(context.Background(), providerCredential{
		apiKey:  "managed-key",
		baseURL: server.URL,
	}.toCredential())
	resp, err := base.DoCreateResponses(ctx, &provider.ResponsesRequest{
		Model: "gpt-4.1",
		Input: "hello",
	})
	if err != nil {
		t.Fatalf("CreateResponse() error = %v", err)
	}
	if resp == nil || resp.ID != "resp_1" || resp.Model != "gpt-4.1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if body == "" {
		t.Fatal("expected request body to be sent")
	}
	if authHeader != "Bearer managed-key" {
		t.Fatalf("authorization = %q, want Bearer managed-key", authHeader)
	}
}

func TestBaseStreamResponseParsesSSEEvents(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("request path = %q, want /responses", r.URL.Path)
		}
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.created\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":1,\"model\":\"gpt-4.1\",\"output\":[]}}\n\n"))
		_, _ = w.Write([]byte("event: response.output_text.delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"hello\"}\n\n"))
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":1,\"model\":\"gpt-4.1\",\"output\":[]}}\n\n"))
	}))
	defer server.Close()

	base := NewBase(provider.ProviderConfig{
		APIKey:       "test-key",
		BaseURL:      server.URL,
		Network:      httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
		AuthStrategy: provider.AuthStrategyManagedAPIKeyFirst,
	})

	ctx := provider.WithCredential(context.Background(), providerCredential{
		apiKey:  "managed-key",
		baseURL: server.URL,
	}.toCredential())
	stream, err := base.DoStreamResponses(ctx, &provider.ResponsesRequest{
		Model:  "gpt-4.1",
		Input:  "hello",
		Stream: true,
	})
	if err != nil {
		t.Fatalf("StreamResponse() error = %v", err)
	}
	defer stream.Close()

	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("first event: %v", err)
	}
	if first.Type != "response.created" || first.Response == nil || first.Response.ID != "resp_1" {
		t.Fatalf("unexpected first event: %+v", first)
	}
	second, err := stream.Recv()
	if err != nil {
		t.Fatalf("second event: %v", err)
	}
	if second.Type != "response.output_text.delta" || second.Delta != "hello" {
		t.Fatalf("unexpected second event: %+v", second)
	}
	third, err := stream.Recv()
	if err != nil {
		t.Fatalf("third event: %v", err)
	}
	if third.Type != "response.completed" || third.Response == nil || third.Response.ID != "resp_1" {
		t.Fatalf("unexpected third event: %+v", third)
	}
	if authHeader != "Bearer managed-key" {
		t.Fatalf("authorization = %q, want Bearer managed-key", authHeader)
	}
}

type providerCredential struct {
	apiKey  string
	baseURL string
}

func (c providerCredential) toCredential() *credentialmgr.Credential {
	return &credentialmgr.Credential{
		Attributes: map[string]string{
			"api_key":  c.apiKey,
			"base_url": c.baseURL,
		},
	}
}
