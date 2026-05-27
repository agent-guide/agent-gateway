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
	var acceptHeader string
	var requestPath string
	var userAgent string
	var xApp string
	var sessionHeader string
	var dangerousHeader string
	var reqBody messagesRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		betaHeader = r.Header.Get("anthropic-beta")
		acceptHeader = r.Header.Get("Accept")
		userAgent = r.Header.Get("User-Agent")
		xApp = r.Header.Get("x-app")
		sessionHeader = r.Header.Get("x-claude-code-session-id")
		dangerousHeader = r.Header.Get("anthropic-dangerous-direct-browser-access")
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
	if acceptHeader != "application/json" {
		t.Fatalf("accept = %q, want application/json", acceptHeader)
	}
	if userAgent != defaultClaudeCodeUserAgent {
		t.Fatalf("user-agent = %q, want %q", userAgent, defaultClaudeCodeUserAgent)
	}
	if xApp != "" {
		t.Fatalf("x-app = %q, want empty", xApp)
	}
	if sessionHeader == "" {
		t.Fatal("x-claude-code-session-id is empty")
	}
	if dangerousHeader != "" {
		t.Fatalf("anthropic-dangerous-direct-browser-access = %q, want empty", dangerousHeader)
	}
	if requestPath != "/v1/messages?beta=true" {
		t.Fatalf("request path = %q, want /v1/messages?beta=true", requestPath)
	}
	if len(reqBody.System) < 4 {
		t.Fatalf("system = %+v, want default Claude Code system blocks plus user system prompt", reqBody.System)
	}
	if reqBody.System[1].CacheControl == nil || reqBody.System[1].CacheControl.Type != "ephemeral" {
		t.Fatalf("default sdk identity block cache_control = %+v, want ephemeral", reqBody.System[1].CacheControl)
	}
	if reqBody.System[len(reqBody.System)-1].Text != "system prompt" {
		t.Fatalf("last system block = %q, want system prompt", reqBody.System[len(reqBody.System)-1].Text)
	}
	if len(reqBody.Messages) != 1 || reqBody.Messages[0].Role != "user" || reqBody.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("messages = %+v, want one user message", reqBody.Messages)
	}
	if reqBody.Messages[0].Content[0].CacheControl == nil || reqBody.Messages[0].Content[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("user content cache_control = %+v, want ephemeral", reqBody.Messages[0].Content[0].CacheControl)
	}
	if reqBody.MaxTokens != 512 {
		t.Fatalf("max_tokens = %d, want 512", reqBody.MaxTokens)
	}
	if reqBody.Metadata.UserID == "" {
		t.Fatal("metadata.user_id is empty")
	}
	if reqBody.Thinking == nil || reqBody.Thinking.Type != "adaptive" {
		t.Fatalf("thinking = %+v, want adaptive", reqBody.Thinking)
	}
	if reqBody.ContextManagement == nil || len(reqBody.ContextManagement.Edits) != 1 {
		t.Fatalf("context_management = %+v, want one edit", reqBody.ContextManagement)
	}
	if reqBody.OutputConfig == nil || reqBody.OutputConfig.Effort != "high" {
		t.Fatalf("output_config = %+v, want effort=high", reqBody.OutputConfig)
	}
	if len(reqBody.Tools) != 0 {
		t.Fatalf("tools = %+v, want empty list", reqBody.Tools)
	}
	if resp.Message == nil || resp.Message.Content != "hello" {
		t.Fatalf("unexpected response = %+v", resp)
	}
	if resp.Message.ResponseMeta == nil || resp.Message.ResponseMeta.Usage == nil || resp.Message.ResponseMeta.Usage.PromptTokens != 12 || resp.Message.ResponseMeta.Usage.CompletionTokens != 34 {
		t.Fatalf("unexpected usage = %+v", resp.Message.ResponseMeta)
	}
}

func TestChatUsesAPIKeyHeaderForManagedAPIKeyCredential(t *testing.T) {
	var authHeader string
	var apiKeyHeader string
	var reqBody messagesRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		apiKeyHeader = r.Header.Get("x-api-key")
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
		Type: credentialmgr.TypeAPIKey,
		Attributes: map[string]string{
			"api_key": "sk-ant-api-test",
		},
	})
	resp, err := prov.Chat(ctx, &provider.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []*schema.Message{
			schema.UserMessage("hello"),
		},
		Options: []model.Option{model.WithMaxTokens(512)},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if authHeader != "Bearer sk-ant-api-test" {
		t.Fatalf("authorization = %q, want Bearer sk-ant-api-test", authHeader)
	}
	if apiKeyHeader != "" {
		t.Fatalf("x-api-key = %q, want empty", apiKeyHeader)
	}
	if len(reqBody.Messages) != 1 || reqBody.Messages[0].Role != "user" || reqBody.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("messages = %+v, want one user message", reqBody.Messages)
	}
	if resp.Message == nil || resp.Message.Content != "hello" {
		t.Fatalf("unexpected response = %+v", resp)
	}
}

func TestChatUsesBearerAuthorizationForProviderFallbackAPIKey(t *testing.T) {
	var authHeader string
	var apiKeyHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		apiKeyHeader = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":34}}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "sk-ant-api-fallback",
		Network: httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = prov.Chat(context.Background(), &provider.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []*schema.Message{
			schema.UserMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if authHeader != "Bearer sk-ant-api-fallback" {
		t.Fatalf("authorization = %q, want Bearer sk-ant-api-fallback", authHeader)
	}
	if apiKeyHeader != "" {
		t.Fatalf("x-api-key = %q, want empty", apiKeyHeader)
	}
}

func TestChatUsesAPIKeyHeaderWhenAuthModeIsExplicitlyAPIKey(t *testing.T) {
	var authHeader string
	var apiKeyHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		apiKeyHeader = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":34}}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "sk-ant-api-fallback",
		Options: map[string]any{
			"auth_mode": "api_key",
		},
		Network: httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = prov.Chat(context.Background(), &provider.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []*schema.Message{
			schema.UserMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if authHeader != "" {
		t.Fatalf("authorization = %q, want empty", authHeader)
	}
	if apiKeyHeader != "sk-ant-api-fallback" {
		t.Fatalf("x-api-key = %q, want sk-ant-api-fallback", apiKeyHeader)
	}
}

func TestChatBuildsClaudeCodeStyleBody(t *testing.T) {
	var reqBody messagesRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":34}}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "sk-ant-api-fallback",
		Network: httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = prov.Chat(context.Background(), &provider.ChatRequest{
		Model: "upstream-model",
		Messages: []*schema.Message{
			schema.SystemMessage("system prompt"),
			{Role: schema.Assistant, Content: "prior answer"},
			schema.UserMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if reqBody.Model != "upstream-model" {
		t.Fatalf("model = %q, want upstream-model", reqBody.Model)
	}
	if reqBody.MaxTokens != defaultClaudeCodeMaxTokens {
		t.Fatalf("max_tokens = %d, want %d", reqBody.MaxTokens, defaultClaudeCodeMaxTokens)
	}
	if len(reqBody.System) < 4 {
		t.Fatalf("system = %+v, want default system plus user system prompt", reqBody.System)
	}
	if reqBody.System[1].CacheControl == nil || reqBody.System[1].CacheControl.Type != "ephemeral" {
		t.Fatalf("default sdk identity block cache_control = %+v, want ephemeral", reqBody.System[1].CacheControl)
	}
	if reqBody.System[len(reqBody.System)-1].Text != "system prompt" {
		t.Fatalf("last system block = %q, want system prompt", reqBody.System[len(reqBody.System)-1].Text)
	}
	if len(reqBody.Messages) != 2 {
		t.Fatalf("messages = %+v, want assistant and user messages", reqBody.Messages)
	}
	if reqBody.Messages[0].Role != "assistant" || reqBody.Messages[0].Content[0].Text != "prior answer" {
		t.Fatalf("assistant message = %+v, want prior answer", reqBody.Messages[0])
	}
	if reqBody.Messages[1].Role != "user" || reqBody.Messages[1].Content[0].Text != "hello" {
		t.Fatalf("user message = %+v, want hello", reqBody.Messages[1])
	}
	if reqBody.Messages[1].Content[0].CacheControl == nil || reqBody.Messages[1].Content[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("user cache_control = %+v, want ephemeral", reqBody.Messages[1].Content[0].CacheControl)
	}
	if reqBody.Metadata.UserID == "" {
		t.Fatal("metadata.user_id is empty")
	}
	if reqBody.Thinking == nil || reqBody.Thinking.Type != "adaptive" {
		t.Fatalf("thinking = %+v, want adaptive", reqBody.Thinking)
	}
	if reqBody.ContextManagement == nil || len(reqBody.ContextManagement.Edits) != 1 {
		t.Fatalf("context_management = %+v, want one edit", reqBody.ContextManagement)
	}
	if reqBody.OutputConfig == nil || reqBody.OutputConfig.Effort != "high" {
		t.Fatalf("output_config = %+v, want effort=high", reqBody.OutputConfig)
	}
	if len(reqBody.Tools) != 0 {
		t.Fatalf("tools = %+v, want empty list", reqBody.Tools)
	}
}

func TestChatPreservesToolChoiceNone(t *testing.T) {
	var reqBody messagesRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":34}}`))
	}))
	defer server.Close()

	prov, err := New(provider.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "sk-ant-api-fallback",
		Network: httpclient.NetworkConfig{RequestTimeoutSeconds: 5},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = prov.Chat(context.Background(), &provider.ChatRequest{
		Model: "upstream-model",
		Messages: []*schema.Message{
			schema.UserMessage("hello"),
		},
		Options: []model.Option{
			model.WithTools([]*schema.ToolInfo{{Name: "lookup"}}),
			model.WithToolChoice(schema.ToolChoiceForbidden),
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if string(reqBody.ToolChoice) != `{"type":"none"}` {
		t.Fatalf("tool_choice = %s, want {\"type\":\"none\"}", string(reqBody.ToolChoice))
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
