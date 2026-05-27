package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr"
	sched "github.com/agent-guide/agent-gateway/pkg/llm/credentialmgr/scheduler"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type testProvider struct {
	streamErr error
	cfg       provider.ProviderConfig
}

func (p *testProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, nil
}

func (p *testProvider) StreamChat(context.Context, *provider.ChatRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, p.streamErr
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

func TestMatchLLMApiIncludesCountTokens(t *testing.T) {
	handler := NewHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	if !handler.MatchLLMApi(req) {
		t.Fatal("MatchLLMApi returned false for /v1/messages/count_tokens")
	}
}

func TestServeLLMApiMarksAnthropicStreamFailures(t *testing.T) {
	credMgr := credentialmgr.NewManager(nil)
	if err := credMgr.RegisterCredential(context.Background(), &credentialmgr.Credential{
		ID:           "cred-anthropic-1",
		ProviderType: "anthropic",
		ProviderID:   "anthropic",
		Scope:        "id:anthropic",
		Type:         credentialmgr.TypeAPIKey,
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	baseProv := &testProvider{
		streamErr: testStatusError{msg: "rate limit", status: http.StatusTooManyRequests},
		cfg: provider.ProviderConfig{
			Id:           "anthropic",
			ProviderType: "anthropic",
		},
	}
	scheduler := newTestCredentialScheduler(t, credMgr)
	prov := &testCredentialMarkingProvider{base: baseProv, scheduler: scheduler, credID: "cred-anthropic-1"}
	handler := NewHandler(nil)

	body, err := json.Marshal(MessagesRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 16,
		Stream:    true,
		Messages: []MessageItem{{
			Role: "user",
			Content: []ContentBlock{{
				Type: "text",
				Text: "hello",
			}},
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
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusTooManyRequests)
	}

	_, err = scheduler.Pick(context.Background(), sched.Filter{
		Type:            credentialmgr.TypeAPIKey,
		CredentialScope: "id:anthropic",
		Model:           "claude-sonnet-4-5",
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

func TestPrepareLLMApiRequestAcceptsSystemBlockArray(t *testing.T) {
	handler := NewHandler(nil)

	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"max_tokens":16,
		"stream":false,
		"system":[
			{"type":"text","text":"You are Claude Code."},
			{"type":"text","text":"Follow the user's instructions."}
		],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"}]}
		]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", bytes.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	if prepared == nil || prepared.ChatRequest == nil {
		t.Fatal("prepared chat request is nil")
	}
	if len(prepared.ChatRequest.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(prepared.ChatRequest.Messages))
	}
	if prepared.ChatRequest.Messages[0].Role != schema.System {
		t.Fatalf("first role = %q, want %q", prepared.ChatRequest.Messages[0].Role, schema.System)
	}
	wantSystem := "You are Claude Code.\nFollow the user's instructions."
	if prepared.ChatRequest.Messages[0].Content != wantSystem {
		t.Fatalf("system content = %q, want %q", prepared.ChatRequest.Messages[0].Content, wantSystem)
	}
}

func TestPrepareLLMApiRequestAcceptsStringSystemPrompt(t *testing.T) {
	handler := NewHandler(nil)

	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"max_tokens":16,
		"stream":false,
		"system":"You are a helpful assistant.",
		"messages":[
			{"role":"user","content":"hello"}
		]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	prepared, _, err := handler.PrepareLLMApiRequest(req)
	if err != nil {
		t.Fatalf("PrepareLLMApiRequest returned error: %v", err)
	}
	if prepared == nil || prepared.ChatRequest == nil {
		t.Fatal("prepared chat request is nil")
	}
	if len(prepared.ChatRequest.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(prepared.ChatRequest.Messages))
	}
	if prepared.ChatRequest.Messages[0].Role != schema.System {
		t.Fatalf("first role = %q, want %q", prepared.ChatRequest.Messages[0].Role, schema.System)
	}
	if prepared.ChatRequest.Messages[0].Content != "You are a helpful assistant." {
		t.Fatalf("system content = %q", prepared.ChatRequest.Messages[0].Content)
	}
}
