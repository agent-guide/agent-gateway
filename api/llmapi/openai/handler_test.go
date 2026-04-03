package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agent-guide/caddy-agent-gateway/admin"
	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/gateway"
	routepkg "github.com/agent-guide/caddy-agent-gateway/gateway/route"
	"gorm.io/gorm"

	"github.com/cloudwego/eino/schema"
	"golang.org/x/crypto/bcrypt"

	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/credential"
	"github.com/agent-guide/caddy-agent-gateway/llm/cliauth/manager"
	"github.com/agent-guide/caddy-agent-gateway/llm/provider"
)

func init() {
	provider.RegisterProvider("testdynamic", func(cfg provider.ProviderConfig) (provider.Provider, error) {
		message, _ := cfg.Options["message"].(string)
		return &testProvider{
			generateResp: &provider.GenerateResponse{
				Message: &schema.Message{Role: schema.RoleType("assistant"), Content: message},
			},
		}, nil
	})
}

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

func newSeededHandler(cliauthMgr *manager.Manager, prov provider.Provider) *Handler {
	handler := NewHandler()
	gw := initGatewayForTests(nil, gateway.NewStaticProviderResolver(func(name string) (provider.Provider, bool) {
		if name != "openai" || prov == nil {
			return nil, false
		}
		return prov, true
	}), nil, cliauthMgr, nil)
	handler.SetAgentGateway(gw)
	handler.RouteID = "openai-test-route"
	gw.EnsureRoute(routepkg.Route{
		ID:   handler.RouteID,
		Name: handler.RouteID,
		Targets: []routepkg.RouteTarget{{
			ProviderRef: "openai",
			Mode:        routepkg.TargetModeWeighted,
			Weight:      1,
		}},
	})
	return handler
}

func initGatewayForTests(routeLoader routepkg.RouteLoader, providerResolver gateway.ProviderResolver, localAPIKeyStore configstoreintf.LocalAPIKeyStorer, cliauthMgr *manager.Manager, selector routepkg.RouteSelector) *gateway.AgentGateway {
	gw := gateway.NewAgentGateway()
	gw.Configure(routeLoader, providerResolver, localAPIKeyStore, cliauthMgr, selector)
	return gw
}

type testLocalAPIKeyStore struct {
	items map[string]*routepkg.LocalAPIKey
}

func (s *testLocalAPIKeyStore) ListByUserID(context.Context, string) ([]any, error) { return nil, nil }
func (s *testLocalAPIKeyStore) Create(_ context.Context, key string, _ string, obj any) error {
	item, ok := obj.(*routepkg.LocalAPIKey)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*routepkg.LocalAPIKey{}
	}
	cloned := *item
	s.items[key] = &cloned
	return nil
}
func (s *testLocalAPIKeyStore) Update(ctx context.Context, key string, obj any) error {
	if _, ok := s.items[key]; !ok {
		return errors.New("not found")
	}
	return s.Create(ctx, key, "", obj)
}
func (s *testLocalAPIKeyStore) Delete(context.Context, string) error { return nil }
func (s *testLocalAPIKeyStore) Get(_ context.Context, key string) (any, error) {
	item, ok := s.items[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return item, nil
}

type testProviderConfigStore struct {
	items map[string]map[string]any
}

func (s *testProviderConfigStore) ListByName(_ context.Context, name string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for _, item := range s.items {
		data, err := json.Marshal(item["config"])
		if err != nil {
			return nil, err
		}
		obj, err := provider.DecodeStoredProviderConfig(data)
		if err != nil {
			return nil, err
		}
		cfg, _ := obj.(*provider.ProviderConfig)
		if cfg == nil {
			continue
		}
		if name == "" || cfg.ProviderName == name {
			out = append(out, cfg)
		}
	}
	return out, nil
}

func (s *testProviderConfigStore) Create(_ context.Context, id string, name string, obj any) (string, error) {
	if s.items == nil {
		s.items = map[string]map[string]any{}
	}
	cfgMap, ok := obj.(map[string]any)
	if !ok {
		data, err := json.Marshal(obj)
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(data, &cfgMap); err != nil {
			return "", err
		}
	}
	if cfgMap == nil {
		cfgMap = map[string]any{}
	}
	cloned := map[string]any{"id": id, "tag": name, "config": cfgMap}
	s.items[id] = cloned
	return id, nil
}

func (s *testProviderConfigStore) Update(ctx context.Context, id string, obj any) error {
	item, ok := s.items[id]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	cfgMap, ok := obj.(map[string]any)
	if !ok {
		data, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &cfgMap); err != nil {
			return err
		}
	}
	if cfgMap == nil {
		cfgMap = map[string]any{}
	}
	if providerName, _ := cfgMap["provider_name"].(string); providerName != "" {
		item["tag"] = providerName
	}
	item["config"] = cfgMap
	s.items[id] = item
	return nil
}

func (s *testProviderConfigStore) Delete(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

func (s *testProviderConfigStore) Get(_ context.Context, id string) (string, any, error) {
	item, ok := s.items[id]
	if !ok {
		return "", nil, gorm.ErrRecordNotFound
	}
	tag, _ := item["tag"].(string)
	data, err := json.Marshal(item["config"])
	if err != nil {
		return "", nil, err
	}
	obj, err := provider.DecodeStoredProviderConfig(data)
	if err != nil {
		return "", nil, err
	}
	return tag, obj, nil
}

type testRouteStore struct {
	items map[string]*routepkg.Route
	tags  map[string]string
}

func (s *testRouteStore) ListByTag(_ context.Context, tag string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for id, item := range s.items {
		if tag != "" && s.tags[id] != tag {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *testRouteStore) ListByTagPrefix(_ context.Context, tagPrefix string) ([]any, error) {
	out := make([]any, 0, len(s.items))
	for id, item := range s.items {
		if tagPrefix != "" && !strings.HasPrefix(s.tags[id], tagPrefix) {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *testRouteStore) Create(_ context.Context, id string, tag string, obj any) error {
	r, ok := obj.(*routepkg.Route)
	if !ok {
		return errors.New("unexpected type")
	}
	if s.items == nil {
		s.items = map[string]*routepkg.Route{}
	}
	if s.tags == nil {
		s.tags = map[string]string{}
	}
	cloned := *r
	s.items[id] = &cloned
	s.tags[id] = tag
	return nil
}

func (s *testRouteStore) Update(ctx context.Context, id string, obj any) error {
	if _, ok := s.items[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	return s.Create(ctx, id, s.tags[id], obj)
}

func (s *testRouteStore) Delete(_ context.Context, id string) error {
	delete(s.items, id)
	delete(s.tags, id)
	return nil
}

func (s *testRouteStore) Get(_ context.Context, id string) (any, error) {
	item, ok := s.items[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return item, nil
}

type integrationConfigStore struct {
	providerStore    configstoreintf.ProviderConfigStorer
	routeStore       configstoreintf.RouteStorer
	localAPIKeyStore configstoreintf.LocalAPIKeyStorer
}

func (s *integrationConfigStore) GetCredentialStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.CredentialStorer, error) {
	return nil, nil
}
func (s *integrationConfigStore) GetProviderConfigStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.ProviderConfigStorer, error) {
	return s.providerStore, nil
}
func (s *integrationConfigStore) GetLocalAPIKeyStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.LocalAPIKeyStorer, error) {
	return s.localAPIKeyStore, nil
}
func (s *integrationConfigStore) GetRouteStore(context.Context, configstoreintf.ConfigObjectDecoder) (configstoreintf.RouteStorer, error) {
	return s.routeStore, nil
}

func TestServeLLMApiMarksOpenAIStreamFailures(t *testing.T) {
	cliauthMgr := manager.NewManager(nil, nil, nil)
	if err := cliauthMgr.Register(context.Background(), &credential.Credential{
		ID:       "cred-openai-1",
		Provider: "openai",
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	handler := newSeededHandler(cliauthMgr, &testProvider{
		streamErr: testStatusError{msg: "rate limit", status: http.StatusTooManyRequests},
	})

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
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusBadGateway)
	}

	cred := cliauthMgr.Get("cred-openai-1")
	if cred == nil {
		t.Fatal("credential not found after request")
	}
	if !cred.Quota.Exceeded {
		t.Fatal("expected quota state to be marked exceeded")
	}
	if !cred.Unavailable {
		t.Fatal("expected credential to be marked unavailable")
	}
	if cred.Status != credential.StatusError {
		t.Fatalf("expected credential status %q, got %q", credential.StatusError, cred.Status)
	}
	if cred.StatusMessage != "quota exceeded" {
		t.Fatalf("expected status message %q, got %q", "quota exceeded", cred.StatusMessage)
	}
	if cred.NextRetryAfter.IsZero() {
		t.Fatal("expected next retry time to be set")
	}

	modelState := cred.ModelStates["gpt-4o-mini"]
	if modelState == nil {
		t.Fatal("expected model state to be recorded")
	}
	if !modelState.Quota.Exceeded {
		t.Fatal("expected model quota state to be marked exceeded")
	}
	if !modelState.Unavailable {
		t.Fatal("expected model state to be marked unavailable")
	}
	if modelState.StatusMessage != "quota exceeded" {
		t.Fatalf("expected model status message %q, got %q", "quota exceeded", modelState.StatusMessage)
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

	cliauthMgr := manager.NewManager(nil, nil, nil)
	if err := cliauthMgr.Register(context.Background(), &credential.Credential{
		ID:       "cred-openai-2",
		Provider: "openai",
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	handler := newSeededHandler(cliauthMgr, prov)

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
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusOK)
	}

	if prov.lastGenerateReq == nil {
		t.Fatal("expected provider Generate to be called")
	}
	if prov.lastGenerateReq.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected model: got %q want %q", prov.lastGenerateReq.Model, "gpt-4o-mini")
	}
	if len(prov.lastGenerateReq.Messages) != 1 || prov.lastGenerateReq.Messages[0].Content != "hello" {
		t.Fatalf("unexpected generated request messages: %+v", prov.lastGenerateReq.Messages)
	}

	var resp ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Object != "chat.completion" {
		t.Fatalf("unexpected object: got %q want %q", resp.Object, "chat.completion")
	}
	if resp.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected model: got %q want %q", resp.Model, "gpt-4o-mini")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("unexpected number of choices: got %d want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Fatalf("unexpected role: got %q want %q", resp.Choices[0].Message.Role, "assistant")
	}
	if resp.Choices[0].Message.Content != "hello back" {
		t.Fatalf("unexpected content: got %q want %q", resp.Choices[0].Message.Content, "hello back")
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("unexpected finish reason: got %q want %q", resp.Choices[0].FinishReason, "stop")
	}
	if resp.Usage.PromptTokens != 3 || resp.Usage.CompletionTokens != 5 || resp.Usage.TotalTokens != 8 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
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

	cliauthMgr := manager.NewManager(nil, nil, nil)
	if err := cliauthMgr.Register(context.Background(), &credential.Credential{
		ID:       "cred-openai-3",
		Provider: "openai",
	}); err != nil {
		t.Fatalf("register credential: %v", err)
	}

	handler := newSeededHandler(cliauthMgr, prov)

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
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("unexpected content type: got %q", got)
	}
	if prov.lastStreamReq == nil {
		t.Fatal("expected provider Stream to be called")
	}

	bodyText := rec.Body.String()
	if !strings.Contains(bodyText, "data: [DONE]") {
		t.Fatalf("expected done marker in stream body, got %q", bodyText)
	}

	firstLine := firstDataLine(bodyText)
	if firstLine == "" {
		t.Fatalf("expected at least one SSE data line, got %q", bodyText)
	}

	var chunk chatCompletionChunk
	if err := json.Unmarshal([]byte(firstLine), &chunk); err != nil {
		t.Fatalf("unmarshal stream chunk: %v", err)
	}
	if chunk.Object != "chat.completion.chunk" {
		t.Fatalf("unexpected object: got %q want %q", chunk.Object, "chat.completion.chunk")
	}
	if chunk.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected model: got %q want %q", chunk.Model, "gpt-4o-mini")
	}
	if len(chunk.Choices) != 1 {
		t.Fatalf("unexpected number of choices: got %d want 1", len(chunk.Choices))
	}
	if chunk.Choices[0].Delta.Role != "assistant" {
		t.Fatalf("unexpected role delta: got %q want %q", chunk.Choices[0].Delta.Role, "assistant")
	}
	if chunk.Choices[0].Delta.Content != "hello stream" {
		t.Fatalf("unexpected content delta: got %q want %q", chunk.Choices[0].Delta.Content, "hello stream")
	}
	if chunk.Choices[0].FinishReason != "stop" {
		t.Fatalf("unexpected finish reason: got %q want %q", chunk.Choices[0].FinishReason, "stop")
	}
}

func TestServeLLMApiRequiresLocalAPIKeyWhenRouteRequiresIt(t *testing.T) {
	handler := NewHandler()
	gw := initGatewayForTests(nil, gateway.NewStaticProviderResolver(func(name string) (provider.Provider, bool) {
		if name == "openai" {
			return &testProvider{}, true
		}
		return nil, false
	}), nil, nil, nil)
	handler.SetAgentGateway(gw)
	handler.RouteID = "openai-test-route"
	gw.EnsureRoute(routepkg.Route{
		ID:      handler.RouteID,
		Name:    handler.RouteID,
		Targets: []routepkg.RouteTarget{{ProviderRef: "openai", Mode: routepkg.TargetModeWeighted, Weight: 1}},
		Policy: routepkg.RoutePolicy{
			Auth: routepkg.AuthPolicy{RequireLocalAPIKey: true},
		},
	})

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
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestServeLLMApiRoutesViaLocalAPIKeyAndRouteTarget(t *testing.T) {
	openAIProv := &testProvider{}
	openRouterProv := &testProvider{
		generateResp: &provider.GenerateResponse{
			Message: &schema.Message{
				Role:    schema.RoleType("assistant"),
				Content: "routed via openrouter",
			},
		},
	}

	handler := NewHandler()
	gw := initGatewayForTests(nil, gateway.NewStaticProviderResolver(func(name string) (provider.Provider, bool) {
		switch name {
		case "openai":
			return openAIProv, true
		case "openrouter":
			return openRouterProv, true
		default:
			return nil, false
		}
	}), &testLocalAPIKeyStore{
		items: map[string]*routepkg.LocalAPIKey{
			"local-test-key": {
				Key:             "local-test-key",
				AllowedRouteIDs: []string{"chat-prod"},
			},
		},
	}, nil, nil)
	handler.SetAgentGateway(gw)
	handler.RouteID = "chat-prod"
	gw.EnsureRoute(routepkg.Route{
		ID:   "chat-prod",
		Name: "chat-prod",
		Targets: []routepkg.RouteTarget{{
			ProviderRef: "openrouter",
			Mode:        routepkg.TargetModeWeighted,
			Weight:      1,
		}},
	})

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
	req.Header.Set("x-api-key", "local-test-key")
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusOK)
	}
	if openAIProv.lastGenerateReq != nil {
		t.Fatal("expected openai provider not to be called")
	}
	if openRouterProv.lastGenerateReq == nil {
		t.Fatal("expected openrouter provider to be called")
	}
}

func TestServeLLMApiReloadsRouteTargetsPerRequest(t *testing.T) {
	openAIProv := &testProvider{
		generateResp: &provider.GenerateResponse{
			Message: &schema.Message{Role: schema.RoleType("assistant"), Content: "from openai"},
		},
	}
	openRouterProv := &testProvider{
		generateResp: &provider.GenerateResponse{
			Message: &schema.Message{Role: schema.RoleType("assistant"), Content: "from openrouter"},
		},
	}

	currentRoute := &routepkg.Route{
		ID:   "chat-prod",
		Name: "chat-prod",
		Targets: []routepkg.RouteTarget{{
			ProviderRef: "openai",
			Mode:        routepkg.TargetModeWeighted,
			Weight:      1,
		}},
	}

	handler := NewHandler()
	gw := initGatewayForTests(func(context.Context, string) (*routepkg.Route, error) {
		return currentRoute, nil
	}, gateway.NewStaticProviderResolver(func(name string) (provider.Provider, bool) {
		switch name {
		case "openai":
			return openAIProv, true
		case "openrouter":
			return openRouterProv, true
		default:
			return nil, false
		}
	}), &testLocalAPIKeyStore{
		items: map[string]*routepkg.LocalAPIKey{
			"local-test-key": {
				Key:             "local-test-key",
				AllowedRouteIDs: []string{"chat-prod"},
			},
		},
	}, nil, nil)
	handler.SetAgentGateway(gw)
	handler.RouteID = "chat-prod"
	gw.EnsureRoute(*currentRoute)

	makeReq := func() *httptest.ResponseRecorder {
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
		req.Header.Set("x-api-key", "local-test-key")
		rec := httptest.NewRecorder()
		if err := handler.ServeLLMApi(rec, req); err != nil {
			t.Fatalf("ServeLLMApi returned error: %v", err)
		}
		return rec
	}

	rec := makeReq()
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected first status: got %d want %d", rec.Code, http.StatusOK)
	}
	if openAIProv.lastGenerateReq == nil {
		t.Fatal("expected first request to use openai provider")
	}
	if openRouterProv.lastGenerateReq != nil {
		t.Fatal("expected openrouter provider not to be called yet")
	}

	currentRoute = &routepkg.Route{
		ID:   "chat-prod",
		Name: "chat-prod",
		Targets: []routepkg.RouteTarget{{
			ProviderRef: "openrouter",
			Mode:        routepkg.TargetModeWeighted,
			Weight:      1,
		}},
	}

	rec = makeReq()
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected second status: got %d want %d", rec.Code, http.StatusOK)
	}
	if openRouterProv.lastGenerateReq == nil {
		t.Fatal("expected second request to use refreshed openrouter provider")
	}
}

func TestAdminProvisionedRouteAndLocalAPIKeyDriveOpenAIHandler(t *testing.T) {
	openRouterProv := &testProvider{
		generateResp: &provider.GenerateResponse{
			Message: &schema.Message{Role: schema.RoleType("assistant"), Content: "from openrouter"},
		},
	}

	cfgStore := &integrationConfigStore{
		providerStore:    &testProviderConfigStore{items: map[string]map[string]any{}},
		routeStore:       &testRouteStore{items: map[string]*routepkg.Route{}},
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*routepkg.LocalAPIKey{}},
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	adminHandler := admin.NewHandler(nil, cfgStore, nil, "admin", string(passwordHash), nil)
	adminToken := loginAdminForTest(t, adminHandler, "admin", "secret-pass")

	postJSON := func(path string, body any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body for %s: %v", path, err)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		adminHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST %s status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}

	postJSON("/admin/providers", map[string]any{
		"id":            "openrouter",
		"provider_name": "openrouter",
		"base_url":      "https://openrouter.example",
	})
	postJSON("/admin/routes", routepkg.Route{
		ID:   "chat-prod",
		Name: "chat-prod",
		Targets: []routepkg.RouteTarget{{
			ProviderRef: "openrouter",
			Mode:        routepkg.TargetModeWeighted,
			Weight:      1,
		}},
		Policy: routepkg.RoutePolicy{
			Auth: routepkg.AuthPolicy{RequireLocalAPIKey: true},
		},
	})
	postJSON("/admin/local_api_keys", routepkg.LocalAPIKey{
		Key:             "lk-e2e",
		UserID:          "admin",
		AllowedRouteIDs: []string{"chat-prod"},
	})

	handler := NewHandler()
	gw := initGatewayForTests(func(ctx context.Context, routeID string) (*routepkg.Route, error) {
		item, err := cfgStore.routeStore.Get(ctx, routeID)
		if err != nil {
			return nil, err
		}
		route, ok := item.(*routepkg.Route)
		if !ok {
			return nil, errors.New("unexpected route type")
		}
		return route, nil
	}, gateway.NewStaticProviderResolver(func(name string) (provider.Provider, bool) {
		if name == "openrouter" {
			return openRouterProv, true
		}
		return nil, false
	}), cfgStore.localAPIKeyStore, nil, nil)
	handler.SetAgentGateway(gw)
	handler.RouteID = "chat-prod"

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
	req.Header.Set("x-api-key", "lk-e2e")
	rec := httptest.NewRecorder()

	if err := handler.ServeLLMApi(rec, req); err != nil {
		t.Fatalf("ServeLLMApi returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", rec.Code, http.StatusOK)
	}
	if openRouterProv.lastGenerateReq == nil {
		t.Fatal("expected request to be routed to provider created through admin")
	}
}

func TestServeLLMApiReloadsProviderConfigPerRequest(t *testing.T) {
	cfgStore := &integrationConfigStore{
		providerStore: &testProviderConfigStore{items: map[string]map[string]any{
			"dyn-provider": {
				"id":  "dyn-provider",
				"tag": "testdynamic",
				"config": map[string]any{
					"id":            "dyn-provider",
					"provider_name": "testdynamic",
					"options": map[string]any{
						"message": "first provider version",
					},
				},
			},
		}},
		routeStore: &testRouteStore{items: map[string]*routepkg.Route{
			"chat-prod": {
				ID:   "chat-prod",
				Name: "chat-prod",
				Targets: []routepkg.RouteTarget{{
					ProviderRef: "dyn-provider",
					Mode:        routepkg.TargetModeWeighted,
					Weight:      1,
				}},
				Policy: routepkg.RoutePolicy{
					Auth: routepkg.AuthPolicy{RequireLocalAPIKey: true},
				},
			},
		}},
		localAPIKeyStore: &testLocalAPIKeyStore{items: map[string]*routepkg.LocalAPIKey{
			"lk-dynamic": {
				Key:             "lk-dynamic",
				AllowedRouteIDs: []string{"chat-prod"},
			},
		}},
	}

	handler := NewHandler()
	gw := initGatewayForTests(func(ctx context.Context, routeID string) (*routepkg.Route, error) {
		item, err := cfgStore.routeStore.Get(ctx, routeID)
		if err != nil {
			return nil, err
		}
		route, _ := item.(*routepkg.Route)
		return route, nil
	}, gateway.ProviderResolverFunc(func(ctx context.Context, ref string) (provider.Provider, string, error) {
		tag, obj, err := cfgStore.providerStore.Get(ctx, ref)
		if err != nil {
			return nil, "", err
		}
		cfg, err := provider.NormalizeStoredProviderConfig(tag, obj)
		if err != nil {
			return nil, "", fmt.Errorf("normalize provider config %q: %w", ref, err)
		}
		prov, err := provider.NewProvider(cfg)
		if err != nil {
			return nil, "", err
		}
		return prov, cfg.ProviderName, nil
	}), cfgStore.localAPIKeyStore, nil, nil)
	handler.SetAgentGateway(gw)
	handler.RouteID = "chat-prod"

	makeReq := func() ChatCompletionResponse {
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
		req.Header.Set("x-api-key", "lk-dynamic")
		rec := httptest.NewRecorder()
		if err := handler.ServeLLMApi(rec, req); err != nil {
			t.Fatalf("ServeLLMApi returned error: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status code: got %d want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp ChatCompletionResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		return resp
	}

	resp := makeReq()
	if resp.Choices[0].Message.Content != "first provider version" {
		t.Fatalf("unexpected first content: %q", resp.Choices[0].Message.Content)
	}

	cfgStore.providerStore.(*testProviderConfigStore).items["dyn-provider"]["config"] = map[string]any{
		"id":            "dyn-provider",
		"provider_name": "testdynamic",
		"options": map[string]any{
			"message": "second provider version",
		},
	}

	resp = makeReq()
	if resp.Choices[0].Message.Content != "second provider version" {
		t.Fatalf("unexpected second content: %q", resp.Choices[0].Message.Content)
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

func loginAdminForTest(t *testing.T, handler *admin.Handler, username string, password string) string {
	t.Helper()

	loginBody, err := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/admin/auth/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("unexpected login status: got %d want %d body=%s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}

	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(loginRec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Token == "" {
		t.Fatal("login token is empty")
	}
	return loginResp.Token
}
