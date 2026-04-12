package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/credential"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/manager"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
	"github.com/cloudwego/eino/schema"
)

type testProvider struct {
	streamErr error
}

func (p *testProvider) Generate(context.Context, *provider.GenerateRequest) (*provider.GenerateResponse, error) {
	return nil, nil
}

func (p *testProvider) Stream(context.Context, *provider.GenerateRequest) (*schema.StreamReader[*schema.Message], error) {
	return nil, p.streamErr
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

func TestServeLLMApiMarksAnthropicStreamFailures(t *testing.T) {
	cliauthMgr := manager.NewManager(nil, nil, nil)
	if err := cliauthMgr.RegisterCredential(context.Background(), &credential.Credential{
		ID:       "cred-anthropic-1",
		Provider: "anthropic",
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	baseProv := &testProvider{
		streamErr: testStatusError{msg: "rate limit", status: http.StatusTooManyRequests},
	}
	prov := provider.WrapWithAuthManager(baseProv, "anthropic", cliauthMgr)
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

	cred := cliauthMgr.GetCredential("cred-anthropic-1")
	if cred == nil || !cred.Quota.Exceeded || !cred.Unavailable || cred.NextRetryAfter.IsZero() {
		t.Fatal("expected credential to be marked unavailable and quota exceeded")
	}
}
