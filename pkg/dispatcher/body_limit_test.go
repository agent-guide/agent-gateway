package dispatcher_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/dispatcher"
	"github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/anthropic"
	"github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/cc"
	"github.com/agent-guide/agent-gateway/pkg/dispatcher/llmapi/openai"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
)

// Exercise the real protocol parsers while stopping before upstream execution.
type bodyLimitAPI struct {
	dispatcher.LLMApiHandler
	prepared bool
}

func (p *bodyLimitAPI) PrepareLLMApiRequest(r *http.Request) (*dispatcher.PreparedLLMApiRequest, llmroute.RequestRequirements, error) {
	v, req, err := p.LLMApiHandler.PrepareLLMApiRequest(r)
	if err == nil {
		p.prepared = true
		v.Disposition = dispatcher.ExecutionLocal
	}
	return v, req, err
}

func (*bodyLimitAPI) ServeLLMApi(w http.ResponseWriter, _ *http.Request, _ provider.Provider, _ *dispatcher.PreparedLLMApiRequest) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type countedBody struct {
	io.ReadCloser
	reads int
}

func (b *countedBody) Read(p []byte) (int, error) {
	b.reads++
	return b.ReadCloser.Read(p)
}

func bodyLimitGateway(t *testing.T, protocol string) *gateway.AgentGateway {
	t.Helper()
	g := gateway.NewAgentGateway()
	if err := g.Bootstrap(t.Context(), gateway.BootstrapOptions{StaticProviderModels: fixtureProviderModels([]routecore.RouteConfig{{
		ID: "body-limit", Kind: routecore.RouteKindLLM, Protocol: routecore.RouteProtocol(protocol),
		MatchPolicy:  routecore.RouteMatchPolicy{PathPrefix: "/probe"},
		TargetPolicy: []byte(`{"default_model":"gpt-4.1","model_targets":[{"name":"gpt-4.1","kind":"provider-model","provider_id":"must-not-resolve","upstream_model":"gpt-4.1"}]}`),
	}}, nil), StaticLLMRoutes: []routecore.RouteConfig{{
		ID: "body-limit", Kind: routecore.RouteKindLLM, Protocol: routecore.RouteProtocol(protocol),
		MatchPolicy:  routecore.RouteMatchPolicy{PathPrefix: "/probe"},
		TargetPolicy: []byte(`{"default_model":"gpt-4.1","model_targets":[{"name":"gpt-4.1","kind":"provider-model","provider_id":"must-not-resolve","upstream_model":"gpt-4.1"}]}`),
	}}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return g
}

const bodyLimitPayload = `{"model":"probe","max_tokens":1,"messages":[{"role":"user","content":"x"}],"input":"x","stream":true}`

func TestLLMRequestBodyLimitBoundaries(t *testing.T) {
	for _, endpoint := range []struct {
		name, path string
		api        dispatcher.LLMApiHandler
	}{
		{"chat", "/v1/chat/completions", openai.NewHandler()},
		{"responses", "/v1/responses", openai.NewHandler()},
		{"messages", "/v1/messages", anthropic.NewHandler(nil)},
		{"cc", "/v1/messages", cc.NewHandler(nil)},
		{"count-tokens", "/v1/messages/count_tokens", cc.NewHandler(nil)},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			g := bodyLimitGateway(t, endpoint.api.Name())
			const limit = 128
			for _, unknown := range []bool{false, true} {
				for _, size := range []int{limit - 1, limit, limit + 1} {
					t.Run(fmt.Sprintf("size=%d/unknown=%t", size, unknown), func(t *testing.T) {
						api := &bodyLimitAPI{LLMApiHandler: endpoint.api}
						h := dispatcher.NewHandler(g, map[string]dispatcher.LLMApiHandler{api.Name(): api}, nil, dispatcher.HandlerOptions{MaxLLMRequestBodyBytes: limit})
						r := httptest.NewRequest(http.MethodPost, "/probe"+endpoint.path, strings.NewReader(bodyLimitPayload+strings.Repeat(" ", size-len(bodyLimitPayload))))
						body := &countedBody{ReadCloser: r.Body}
						r.Body = body
						if unknown {
							r.ContentLength = -1
							r.TransferEncoding = []string{"chunked"}
						}
						w := httptest.NewRecorder()
						h.ServeHTTP(w, r)
						want := http.StatusNoContent
						if size > limit {
							want = http.StatusRequestEntityTooLarge
						}
						if w.Code != want || api.prepared != (size <= limit) {
							t.Fatalf("status=%d prepared=%t body=%s", w.Code, api.prepared, w.Body.String())
						}
						if size > limit {
							if w.Body.String() != "{\"error\":\"LLM request body exceeds 128 bytes\"}\n" {
								t.Fatalf("unhelpful error: %s", w.Body.String())
							}
							if !unknown && body.reads != 0 {
								t.Fatal("read body despite known oversized length")
							}
						}
					})
				}
			}
		})
	}
}

func TestLLMRequestBodyLimitCanExceedDefault(t *testing.T) {
	g := bodyLimitGateway(t, "openai")
	size := dispatcher.DefaultMaxLLMRequestBodyBytes + 1
	body := bodyLimitPayload + strings.Repeat(" ", size-len(bodyLimitPayload))
	for _, limit := range []int64{0, 32 << 20} {
		api := &bodyLimitAPI{LLMApiHandler: openai.NewHandler()}
		h := dispatcher.NewHandler(g, map[string]dispatcher.LLMApiHandler{"openai": api}, nil, dispatcher.HandlerOptions{MaxLLMRequestBodyBytes: limit})
		if err := h.Validate(); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/probe/v1/chat/completions", strings.NewReader(body)))
		want := http.StatusNoContent
		if limit == 0 {
			want = http.StatusRequestEntityTooLarge
		}
		if w.Code != want {
			t.Fatalf("limit=%d status=%d: %s", limit, w.Code, w.Body.String())
		}
	}
}

func TestLLMRequestBodyLimitPreservesOuterLimit(t *testing.T) {
	g := bodyLimitGateway(t, "openai")
	api := &bodyLimitAPI{LLMApiHandler: openai.NewHandler()}
	h := dispatcher.NewHandler(g, map[string]dispatcher.LLMApiHandler{"openai": api}, nil, dispatcher.HandlerOptions{MaxLLMRequestBodyBytes: 1024})
	r := httptest.NewRequest(http.MethodPost, "/probe/v1/chat/completions", strings.NewReader(bodyLimitPayload))
	r.ContentLength = -1
	w := httptest.NewRecorder()
	r.Body = http.MaxBytesReader(w, r.Body, 64)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "exceeds 64 bytes") {
		t.Fatalf("outer limit lost: %d %s", w.Code, w.Body.String())
	}
}

func TestLLMRequestBodyLimitRejectsNegativeConfiguration(t *testing.T) {
	h := dispatcher.NewHandler(nil, nil, nil, dispatcher.HandlerOptions{EnableMCP: true, MaxLLMRequestBodyBytes: -1})
	if err := h.Validate(); err == nil {
		t.Fatal("negative limit accepted")
	}
}

func TestLLMMalformedRequestReturnsBadRequest(t *testing.T) {
	for _, endpoint := range []struct {
		name, path string
		api        dispatcher.LLMApiHandler
	}{
		{"chat", "/v1/chat/completions", openai.NewHandler()},
		{"responses", "/v1/responses", openai.NewHandler()},
		{"messages", "/v1/messages", anthropic.NewHandler(nil)},
		{"cc", "/v1/messages", cc.NewHandler(nil)},
		{"count-tokens", "/v1/messages/count_tokens", cc.NewHandler(nil)},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			g := bodyLimitGateway(t, endpoint.api.Name())
			for _, body := range []string{"not json", `{"model":`, `{} {}`} {
				api := &bodyLimitAPI{LLMApiHandler: endpoint.api}
				h := dispatcher.NewHandler(g, map[string]dispatcher.LLMApiHandler{api.Name(): api}, nil, dispatcher.HandlerOptions{})
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/probe"+endpoint.path, strings.NewReader(body)))
				if w.Code != http.StatusBadRequest || api.prepared {
					t.Fatalf("body=%q: status=%d prepared=%t response=%s", body, w.Code, api.prepared, w.Body.String())
				}
			}
		})
	}
}
