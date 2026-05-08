package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
	dispatcher "github.com/agent-guide/caddy-agent-gateway/pkg/dispatcher"
	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/credentialmgr"
	sched "github.com/agent-guide/caddy-agent-gateway/pkg/llm/credentialmgr/scheduler"
	"github.com/agent-guide/caddy-agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type testProvider struct {
	chatResp    *provider.ChatResponse
	generateErr error
	streamResp  *schema.StreamReader[*schema.Message]
	streamErr   error
	cfg         provider.ProviderConfig

	lastChatReq   *provider.ChatRequest
	lastStreamReq *provider.ChatRequest
}

func (p *testProvider) Chat(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.lastChatReq = req
	return p.chatResp, p.generateErr
}

func (p *testProvider) StreamChat(_ context.Context, req *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
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
	return p.cfg
}

type testResponsesProvider struct {
	*testProvider
	responseResp      *provider.ResponsesResponse
	responseErr       error
	responseStream    *schema.StreamReader[*provider.ResponsesStreamEvent]
	responseStreamErr error
	lastResponseReq   *provider.ResponsesRequest
}

func (p *testResponsesProvider) CreateResponses(_ context.Context, req *provider.ResponsesRequest) (*provider.ResponsesResponse, error) {
	p.lastResponseReq = req
	return p.responseResp, p.responseErr
}

func (p *testResponsesProvider) StreamResponses(_ context.Context, req *provider.ResponsesRequest) (*schema.StreamReader[*provider.ResponsesStreamEvent], error) {
	p.lastResponseReq = req
	return p.responseStream, p.responseStreamErr
}

type testStatusError struct {
	msg    string
	status int
}

func (e testStatusError) Error() string   { return e.msg }
func (e testStatusError) StatusCode() int { return e.status }

type testCredentialMarkingProvider struct {
	base      provider.Provider
	scheduler sched.CredentialScheduler
	credID    string
}

func (p *testCredentialMarkingProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	resp, err := p.base.Chat(ctx, req)
	p.mark(req.Model, err)
	return resp, err
}

func (p *testCredentialMarkingProvider) StreamChat(ctx context.Context, req *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	stream, err := p.base.StreamChat(ctx, req)
	p.mark(req.Model, err)
	return stream, err
}

func (p *testCredentialMarkingProvider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return p.base.ListModels(ctx)
}

func (p *testCredentialMarkingProvider) Capabilities() provider.ProviderCapabilities {
	return p.base.Capabilities()
}

func (p *testCredentialMarkingProvider) Config() provider.ProviderConfig {
	return p.base.Config()
}

func (p *testCredentialMarkingProvider) mark(model string, err error) {
	if p.scheduler == nil || p.credID == "" {
		return
	}
	result := sched.Result{CredentialID: p.credID, Model: model, Success: err == nil}
	if err != nil {
		status := http.StatusBadGateway
		var sc interface{ StatusCode() int }
		if errors.As(err, &sc) {
			status = sc.StatusCode()
		}
		result.Error = &sched.Error{
			Code:       http.StatusText(status),
			Message:    err.Error(),
			HTTPStatus: status,
			Retryable:  status == http.StatusTooManyRequests || status >= 500,
		}
	}
	p.scheduler.MarkResult(context.Background(), result)
}

func newHandler() *Handler {
	return NewHandler()
}

func newTestCredentialScheduler(t *testing.T, mgr *credentialmgr.Manager) sched.CredentialScheduler {
	t.Helper()
	scheduler := sched.NewScheduler(nil)
	listener, ok := scheduler.(credentialmgr.CredentialLifecycleListener)
	if !ok {
		t.Fatal("scheduler does not implement CredentialLifecycleListener")
	}
	mgr.AddListener(listener)
	scheduler.Rebuild(mgr.ListCredentials(credentialmgr.Filter{}))
	return scheduler
}

func TestMatchLLMApiRequiresVersionedOpenAIPath(t *testing.T) {
	handler := newHandler()
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/models", "/v1/embeddings"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		if !handler.MatchLLMApi(req) {
			t.Fatalf("MatchLLMApi(%q) = false, want true", path)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/tenant/chat/completions", nil)
	if handler.MatchLLMApi(req) {
		t.Fatal("MatchLLMApi matched unversioned suffix path")
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/models/extra", nil)
	if handler.MatchLLMApi(req) {
		t.Fatal("MatchLLMApi matched non-endpoint path")
	}
}

func TestServeLLMApiMarksOpenAIStreamFailures(t *testing.T) {
	credMgr := credentialmgr.NewManager(nil)
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "cred-openai-1",
		ProviderType: "openai",
		ProviderID:   "openai",
		Source:       credentialmgr.SourceAPIKey,
		Attributes: map[string]string{
			"scope": "id:openai",
		},
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	baseProv := &testProvider{
		streamErr: testStatusError{msg: "rate limit", status: http.StatusTooManyRequests},
		cfg: provider.ProviderConfig{
			Id:           "openai",
			ProviderType: "openai",
		},
	}
	scheduler := newTestCredentialScheduler(t, credMgr)
	prov := &testCredentialMarkingProvider{base: baseProv, scheduler: scheduler, credID: "cred-openai-1"}
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

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, prov, prepared); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusTooManyRequests)
	}

	_, err = scheduler.Pick(context.Background(), sched.Filter{
		Source:          credentialmgr.SourceAPIKey,
		CredentialScope: "id:openai",
		Model:           "gpt-4o-mini",
	}, nil)
	if err == nil {
		t.Fatal("expected scheduler to reject quota-exceeded credential")
	}
	type statusCoder interface {
		StatusCode() int
	}
	var sc statusCoder
	if !errors.As(err, &sc) || sc.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("expected 429 scheduler error, got %v", err)
	}
}

func TestServeLLMApiMapsClientCanceledStreamTo499(t *testing.T) {
	prov := &testProvider{
		streamErr: statuserr.New(http.StatusBadGateway, "Post \"https://api.openai.com/v1/chat/completions\": context canceled"),
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

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, prov, prepared); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != dispatcher.StatusClientClosedRequest {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, dispatcher.StatusClientClosedRequest)
	}
	if !strings.Contains(rec.Body.String(), "client canceled request") {
		t.Fatalf("unexpected error body: %q", rec.Body.String())
	}
}

func TestServeLLMApiReturnsChatCompletionResponse(t *testing.T) {
	prov := &testProvider{
		chatResp: &provider.ChatResponse{
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

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
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
	if prov.lastChatReq == nil || prov.lastChatReq.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected chat request: %+v", prov.lastChatReq)
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

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
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

func TestPrepareLLMApiRequestAllowsResponsesStateForProviderLevelHandling(t *testing.T) {
	handler := newHandler()

	body := `{"model":"gpt-4.1","input":"hello","previous_response_id":"resp_123"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))

	prepared, requirements, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prepared == nil || prepared.Type != provider.LLMApiRequestTypeResponses || prepared.ChatRequest != nil || prepared.ResponsesRequest == nil || prepared.ResponsesRequest.Model != "gpt-4.1" {
		t.Fatalf("unexpected prepared request: %+v", prepared)
	}
	if prepared.Model() != "gpt-4.1" {
		t.Fatalf("prepared.Model() = %q, want gpt-4.1", prepared.Model())
	}
	if prepared.Stream() {
		t.Fatal("prepared.Stream() = true, want false")
	}
	if requirements.Model != "gpt-4.1" {
		t.Fatalf("requirements.Model = %q, want gpt-4.1", requirements.Model)
	}
	if requirements.RequireStreaming {
		t.Fatal("requirements.RequireStreaming = true, want false")
	}
}

func TestServeLLMApiAllowsProviderResponsesRequestsThatFallbackCannotExpress(t *testing.T) {
	prov := &testResponsesProvider{
		testProvider: &testProvider{},
		responseResp: &provider.ResponsesResponse{
			ID:        "resp_provider",
			Object:    "response",
			CreatedAt: 1,
			Model:     "gpt-4.1",
			Output: []provider.ResponsesResponseOutput{{
				ID:     "msg_provider",
				Type:   "message",
				Role:   "assistant",
				Status: "completed",
				Content: []provider.ResponsesResponseContentPart{{
					Type:        "output_text",
					Text:        "provider path",
					Annotations: []any{},
				}},
			}},
		},
	}
	handler := newHandler()

	body := `{"model":"gpt-4.1","input":"hello","previous_response_id":"resp_123"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
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
	if prov.lastResponseReq == nil || prov.lastResponseReq.PreviousResponseID != "resp_123" {
		t.Fatalf("unexpected provider responses request: %+v", prov.lastResponseReq)
	}
	if prov.lastChatReq != nil {
		t.Fatalf("expected no chat fallback, got %+v", prov.lastChatReq)
	}
}

func TestServeLLMApiReturnsResponsesResponse(t *testing.T) {
	prov := &testProvider{
		chatResp: &provider.ChatResponse{
			Message: &schema.Message{
				Role:    schema.RoleType("assistant"),
				Content: "hello response",
				ResponseMeta: &schema.ResponseMeta{
					FinishReason: "stop",
					Usage: &schema.TokenUsage{
						PromptTokens:     4,
						CompletionTokens: 6,
						TotalTokens:      10,
					},
				},
			},
		},
	}
	handler := newHandler()

	body := `{"model":"gpt-4.1","input":"hello"}` + "\n"
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
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
	if prov.lastChatReq == nil || prov.lastChatReq.Model != "gpt-4.1" {
		t.Fatalf("unexpected chat request: %+v", prov.lastChatReq)
	}
	if len(prov.lastChatReq.Messages) != 1 || prov.lastChatReq.Messages[0].Content != "hello" {
		t.Fatalf("unexpected internal messages: %+v", prov.lastChatReq.Messages)
	}

	var resp provider.ResponsesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Object != "response" || resp.Model != "gpt-4.1" {
		t.Fatalf("unexpected response envelope: %+v", resp)
	}
	if len(resp.Output) != 1 || len(resp.Output[0].Content) != 1 || resp.Output[0].Content[0].Text != "hello response" {
		t.Fatalf("unexpected output: %+v", resp.Output)
	}
}

func TestServeLLMApiStreamsResponsesEvents(t *testing.T) {
	prov := &testProvider{
		streamResp: schema.StreamReaderFromArray([]*schema.Message{{
			Role:    schema.RoleType("assistant"),
			Content: "hello response stream",
		}}),
	}
	handler := newHandler()

	body := `{"model":"gpt-4.1","input":"hello","stream":true}` + "\n"
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
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
	if prov.lastStreamReq == nil || prov.lastStreamReq.Model != "gpt-4.1" {
		t.Fatalf("unexpected stream request: %+v", prov.lastStreamReq)
	}

	bodyText := rec.Body.String()
	for _, want := range []string{"event: response.created", "event: response.output_text.delta", "event: response.completed"} {
		if !strings.Contains(bodyText, want) {
			t.Fatalf("expected %q in stream body, got %q", want, bodyText)
		}
	}
}

func TestServeLLMApiFallsBackWhenWrappedProviderResponsesCreateIsUnsupported(t *testing.T) {
	baseProv := &testProvider{
		chatResp: &provider.ChatResponse{
			Message: &schema.Message{
				Role:    schema.RoleType("assistant"),
				Content: "hello wrapped fallback",
			},
		},
		cfg: provider.ProviderConfig{
			Id:           "zhipu-main",
			ProviderType: "zhipu",
		},
	}
	handler := newHandler()

	body := `{"model":"glm-4.7","input":"hello"}` + "\n"
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, baseProv, prepared); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusOK)
	}
	if baseProv.lastChatReq == nil || baseProv.lastChatReq.Model != "glm-4.7" {
		t.Fatalf("expected chat fallback, got %+v", baseProv.lastChatReq)
	}
	if !strings.Contains(rec.Body.String(), "hello wrapped fallback") {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

func TestServeLLMApiFallsBackWhenWrappedProviderResponsesStreamIsUnsupported(t *testing.T) {
	baseProv := &testProvider{
		streamResp: schema.StreamReaderFromArray([]*schema.Message{{
			Role:    schema.RoleType("assistant"),
			Content: "hello wrapped stream fallback",
		}}),
		cfg: provider.ProviderConfig{
			Id:           "zhipu-main",
			ProviderType: "zhipu",
		},
	}
	handler := newHandler()

	body := `{"model":"glm-4.7","input":"hello","stream":true}` + "\n"
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, baseProv, prepared); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusOK)
	}
	if baseProv.lastStreamReq == nil || baseProv.lastStreamReq.Model != "glm-4.7" {
		t.Fatalf("expected stream fallback, got %+v", baseProv.lastStreamReq)
	}
	bodyText := rec.Body.String()
	for _, want := range []string{"event: response.created", "event: response.output_text.delta", "hello wrapped stream fallback", "event: response.completed"} {
		if !strings.Contains(bodyText, want) {
			t.Fatalf("expected %q in stream body, got %q", want, bodyText)
		}
	}
}

func TestServeLLMApiPrefersProviderResponsesInterface(t *testing.T) {
	prov := &testResponsesProvider{
		testProvider: &testProvider{},
		responseResp: &provider.ResponsesResponse{
			ID:        "resp_provider",
			Object:    "response",
			CreatedAt: 1,
			Model:     "gpt-4.1",
			Output: []provider.ResponsesResponseOutput{{
				ID:     "msg_provider",
				Type:   "message",
				Role:   "assistant",
				Status: "completed",
				Content: []provider.ResponsesResponseContentPart{{
					Type:        "output_text",
					Text:        "provider path",
					Annotations: []any{},
				}},
			}},
		},
	}
	handler := newHandler()

	body := `{"model":"gpt-4.1","input":"hello"}` + "\n"
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, prov, prepared); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if prov.lastResponseReq == nil {
		t.Fatal("expected provider Responses interface to be used")
	}
	if prov.lastChatReq != nil {
		t.Fatalf("expected no chat fallback, got %+v", prov.lastChatReq)
	}
	if !strings.Contains(rec.Body.String(), "provider path") {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

func TestServeLLMApiRejectsUnsupportedResponsesFallbackState(t *testing.T) {
	prov := &testProvider{}
	handler := newHandler()

	body := `{"model":"gpt-4.1","input":"hello","previous_response_id":"resp_123"}` + "\n"
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req, prov, prepared); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "previous_response_id is not supported") {
		t.Fatalf("unexpected body: %q", rec.Body.String())
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
