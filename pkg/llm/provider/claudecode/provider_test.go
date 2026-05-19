package claudecode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/agent-guide/agent-gateway/pkg/httpclient"
	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

func TestChatUsesCLIAuthTokenBearerHeaders(t *testing.T) {
	var authHeader string
	var betaHeader string
	var appHeader string
	var uaHeader string
	var acceptHeader string
	var requestPath string
	var reqBody messagesRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		betaHeader = r.Header.Get("anthropic-beta")
		appHeader = r.Header.Get("x-app")
		uaHeader = r.Header.Get("User-Agent")
		acceptHeader = r.Header.Get("Accept")
		requestPath = r.URL.RequestURI()
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":34}}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{
		BaseURL: server.URL,
		Network: httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := provider.WithCredential(context.Background(), &credentialmgr.Credential{
		Type: credentialmgr.TypeCLIAuthToken,
		Metadata: map[string]any{
			"access_token": "sk-ant-oat-test",
		},
	})
	resp, err := prov.Chat(ctx, &provider.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []*schema.Message{
			schema.SystemMessage("system prompt"),
			schema.UserMessage("hello"),
		},
		Options: []model.Option{model.WithMaxTokens(512)},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if authHeader != "Bearer sk-ant-oat-test" {
		t.Fatalf("authorization = %q, want Bearer sk-ant-oat-test", authHeader)
	}
	if betaHeader != anthropicBeta {
		t.Fatalf("anthropic-beta = %q, want %q", betaHeader, anthropicBeta)
	}
	if appHeader != "cli" {
		t.Fatalf("x-app = %q, want cli", appHeader)
	}
	if uaHeader != userAgent {
		t.Fatalf("user-agent = %q, want %q", uaHeader, userAgent)
	}
	if acceptHeader != "application/json" {
		t.Fatalf("accept = %q, want application/json", acceptHeader)
	}
	if requestPath != "/v1/messages?beta=true" {
		t.Fatalf("request path = %q, want /v1/messages?beta=true", requestPath)
	}
	if reqBody.System != "system prompt" {
		t.Fatalf("system = %q, want system prompt", reqBody.System)
	}
	if len(reqBody.Messages) != 1 || reqBody.Messages[0].Role != "user" || reqBody.Messages[0].Content != "hello" {
		t.Fatalf("messages = %+v, want one user message", reqBody.Messages)
	}
	if reqBody.MaxTokens != 512 {
		t.Fatalf("max_tokens = %d, want 512", reqBody.MaxTokens)
	}
	if resp.Message == nil || resp.Message.Content != "hello" {
		t.Fatalf("unexpected response = %+v", resp)
	}
	if resp.Message.ResponseMeta == nil || resp.Message.ResponseMeta.Usage == nil || resp.Message.ResponseMeta.Usage.PromptTokens != 12 || resp.Message.ResponseMeta.Usage.CompletionTokens != 34 {
		t.Fatalf("unexpected usage = %+v", resp.Message.ResponseMeta)
	}
}

func TestStreamChatParsesAnthropicSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("accept = %q, want text/event-stream", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: content_block_delta\n")
		_, _ = io.WriteString(w, "data: {\"delta\":{\"type\":\"text_delta\",\"text\":\"hel\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\n")
		_, _ = io.WriteString(w, "data: {\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n\n")
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{
		BaseURL: server.URL,
		Network: httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := provider.WithCredential(context.Background(), &credentialmgr.Credential{
		Type: credentialmgr.TypeCLIAuthToken,
		Metadata: map[string]any{
			"access_token": "sk-ant-oat-test",
		},
	})
	stream, err := prov.StreamChat(ctx, &provider.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []*schema.Message{
			schema.UserMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("StreamChat() error = %v", err)
	}
	defer stream.Close()

	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("first recv: %v", err)
	}
	second, err := stream.Recv()
	if err != nil {
		t.Fatalf("second recv: %v", err)
	}
	if first.Content != "hel" || second.Content != "lo" {
		t.Fatalf("unexpected chunks: first=%+v second=%+v", first, second)
	}
}
