package cc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/anthropic"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type testStreamingProvider struct {
	chunks []*schema.Message
	cfg    provider.ProviderConfig
}

func (p *testStreamingProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{}, nil
}

func (p *testStreamingProvider) StreamChat(context.Context, *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](len(p.chunks))
	go func() {
		defer sw.Close()
		for _, chunk := range p.chunks {
			sw.Send(chunk, nil)
		}
	}()
	return sr, nil
}

func (p *testStreamingProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}

func (p *testStreamingProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Streaming: true}
}

func (p *testStreamingProvider) Config() provider.ProviderConfig {
	return p.cfg
}

func TestHandlerName(t *testing.T) {
	handler := NewHandler(nil)
	if handler.Name() != "cc" {
		t.Fatalf("Name() = %q, want cc", handler.Name())
	}
}

func TestServeLLMApiCountTokensReturnsEstimate(t *testing.T) {
	handler := NewHandler(nil)
	body, err := json.Marshal(anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 16,
		Messages: []anthropic.MessageItem{{
			Role:    "user",
			Content: anthropic.MessageContent{{Type: "text", Text: "hello"}},
		}},
		Tools: []anthropic.ToolDefinition{{
			Name:        "lookup",
			Description: "Lookup data",
		}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens?beta=true", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, &testStreamingProvider{}, nil); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.InputTokens <= 0 {
		t.Fatalf("input_tokens = %d, want positive", resp.InputTokens)
	}
}

func TestServeLLMApiStreamPassesThroughStatefulClaudeCodeToolUse(t *testing.T) {
	handler := NewHandler(nil)
	prov := &testStreamingProvider{
		cfg: provider.ProviderConfig{Id: "codex", ProviderType: "codex"},
		chunks: []*schema.Message{{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "Agent",
					Arguments: `{"name":"researcher"}`,
				},
			}},
			ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_use"},
		}},
	}

	body, err := json.Marshal(anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 16,
		Stream:    true,
		Messages: []anthropic.MessageItem{{
			Role:    "user",
			Content: anthropic.MessageContent{{Type: "text", Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, prov, prepared); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}

	payload, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyText := string(payload)
	if !strings.Contains(bodyText, `"type":"tool_use"`) || !strings.Contains(bodyText, `"name":"Agent"`) {
		t.Fatalf("missing Agent tool_use in stream: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"stop_reason":"tool_use"`) {
		t.Fatalf("missing tool_use stop_reason in stream: %s", bodyText)
	}
}
