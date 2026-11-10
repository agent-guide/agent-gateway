package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/routecore"
	"go.uber.org/zap"
)

func TestStandaloneLLMRequestBodyLimit(t *testing.T) {
	g := gateway.NewAgentGateway()
	if err := g.Bootstrap(t.Context(), gateway.BootstrapOptions{StaticLLMRoutes: []routecore.RouteConfig{{
		ID: "cc", Kind: routecore.RouteKindLLM, Protocol: routecore.RouteProtocolCC,
		MatchPolicy:  routecore.RouteMatchPolicy{PathPrefix: "/probe"},
		TargetPolicy: []byte(`{"provider_target":{"provider_id":"unused"}}`),
	}}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	body := `{"model":"probe","messages":[{"role":"user","content":"x"}]}` + strings.Repeat(" ", 100)
	for _, limit := range []int64{0, 128, 256} {
		h, err := newDispatchHandler(g, zap.NewNop(), limit)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/probe/v1/messages/count_tokens", strings.NewReader(body)))
		want := http.StatusOK
		if limit == 128 {
			want = http.StatusRequestEntityTooLarge
		}
		if w.Code != want {
			t.Fatalf("limit=%d status=%d: %s", limit, w.Code, w.Body.String())
		}
	}
}

func TestStandaloneRejectsNegativeBodyLimitBeforeOpeningStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.db")
	err := Run(t.Context(), Options{ConfigStorePath: path, MaxLLMRequestBodyBytes: -1})
	if err == nil || !strings.Contains(err.Error(), "max_llm_request_body_bytes") {
		t.Fatalf("invalid limit = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid configuration created storage: %v", err)
	}
}
