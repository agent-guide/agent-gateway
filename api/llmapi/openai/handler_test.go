package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/credential"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/manager"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type testProvider struct {
	generateResp *provider.GenerateResponse
	generateErr  error
	streamResp   *schema.StreamReader[*schema.Message]
	streamErr    error

	lastGenerateReq *provider.GenerateRequest
	lastStreamReq   *provider.GenerateRequest
}

func (p *testProvider) Generate(_ context.Context, req *provider.GenerateRequest) (*provider.GenerateResponse, error) {
	p.lastGenerateReq = req
	return p.generateResp, p.generateErr
}

func (p *testProvider) Stream(_ context.Context, req *provider.GenerateRequest) (*schema.StreamReader[*schema.Message], error) {
	p.lastStreamReq = req
	return p.streamResp, p.streamErr
}

func (p *testProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return nil, nil
}

func (p *testProvider) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Streaming: true}
}

func (p *testProvider) Config() provider.ProviderConfig {
	return provider.ProviderConfig{}
}

type testStatusError struct {
	msg    string
	status int
}

func (e testStatusError) Error() string   { return e.msg }
func (e testStatusError) StatusCode() int { return e.status }

func newHandler() *Handler {
	return NewHandler()
}

func withRouteInfo(req *http.Request) *http.Request {
	return req
}

func TestServeLLMApiMarksOpenAIStreamFailures(t *testing.T) {
	cliauthMgr := manager.NewManager(nil, nil, nil)
	if err := cliauthMgr.Register(context.Background(), &credential.Credential{
		ID:       "cred-openai-1",
		Provider: "openai",
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	baseProv := &testProvider{
		streamErr: testStatusError{msg: "rate limit", status: http.StatusTooManyRequests},
	}
	prov := provider.WrapWithAuthManager(baseProv, "openai", cliauthMgr)
	handler := newHandler()

	body, err := json.Marshal(ChatCompletionRequest{
		Model:  "gpt-4o-mini",
		Stream: true,
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := withRouteInfo(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	prepared, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, prov, prepared); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusBadGateway)
	}

	cred := cliauthMgr.Get("cred-openai-1")
	if cred == nil {
		t.Fatal("credential not found after request")
	}
	if !cred.Quota.Exceeded || !cred.Unavailable {
		t.Fatal("expected credential to be marked unavailable and quota exceeded")
	}
	modelState := cred.ModelStates["gpt-4o-mini"]
	if modelState == nil || !modelState.Quota.Exceeded || !modelState.Unavailable {
		t.Fatal("expected model state to be marked unavailable and quota exceeded")
	}
}

func TestServeLLMApiReturnsChatCompletionResponse(t *testing.T) {
	prov := &testProvider{
		generateResp: &provider.GenerateResponse{
			Message: &schema.Message{
				Role:    schema.RoleType("assistant"),
				Content: "hello back",
				ResponseMeta: &schema.ResponseMeta{
					FinishReason: "stop",
					Usage: &schema.TokenUsage{
						PromptTokens:     3,
						CompletionTokens: 5,
						TotalTokens:      8,
					},
				},
			},
		},
	}
	handler := newHandler()

	body, err := json.Marshal(ChatCompletionRequest{
		Model: "gpt-4o-mini",
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := withRouteInfo(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	prepared, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, prov, prepared); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusOK)
	}
	if prov.lastGenerateReq == nil || prov.lastGenerateReq.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected generate request: %+v", prov.lastGenerateReq)
	}

	var resp ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Object != "chat.completion" || resp.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected response envelope: %+v", resp)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "hello back" {
		t.Fatalf("unexpected choices: %+v", resp.Choices)
	}
}

func TestServeLLMApiStreamsOpenAIChunks(t *testing.T) {
	prov := &testProvider{
		streamResp: schema.StreamReaderFromArray([]*schema.Message{{
			Role:    schema.RoleType("assistant"),
			Content: "hello stream",
			ResponseMeta: &schema.ResponseMeta{
				FinishReason: "stop",
			},
		}}),
	}
	handler := newHandler()

	body, err := json.Marshal(ChatCompletionRequest{
		Model:  "gpt-4o-mini",
		Stream: true,
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := withRouteInfo(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)))
	prepared, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, prov, prepared); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusOK)
	}
	if prov.lastStreamReq == nil {
		t.Fatal("expected provider Stream to be called")
	}

	bodyText := rec.Body.String()
	if !strings.Contains(bodyText, "data: [DONE]") {
		t.Fatalf("expected done marker in stream body, got %q", bodyText)
	}

	firstLine := firstDataLine(bodyText)
	var chunk chatCompletionChunk
	if err := json.Unmarshal([]byte(firstLine), &chunk); err != nil {
		t.Fatalf("unmarshal stream chunk: %v", err)
	}
	if chunk.Model != "gpt-4o-mini" || len(chunk.Choices) != 1 || chunk.Choices[0].Delta.Content != "hello stream" {
		t.Fatalf("unexpected chunk: %+v", chunk)
	}
}

func firstDataLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	return ""
}
