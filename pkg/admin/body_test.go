package admin

import (
	"github.com/agent-guide/agent-gateway/pkg/llm/provider"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminBodyBoundaryBeforeMutation(t *testing.T) {
	h := NewHandler(nil, nil)
	calls := 0
	h.mux.HandleFunc("POST /admin/body-test", func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if len(raw) != MaxRequestBodyBytes {
			t.Errorf("body size %d", len(raw))
		}
		w.WriteHeader(http.StatusCreated)
	})
	for _, unknown := range []bool{false, true} {
		for _, n := range []int{MaxRequestBodyBytes, MaxRequestBodyBytes + 1} {
			req := httptest.NewRequest("POST", "/admin/body-test", strings.NewReader("{}"+strings.Repeat(" ", n-2)))
			if unknown {
				req.ContentLength = -1
				req.TransferEncoding = []string{"chunked"}
			}
			before := calls
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if n > MaxRequestBodyBytes {
				if rr.Code != 413 || rr.Body.String() != "{\"error\":\"request body exceeds 4194304 bytes\"}\n" || calls != before {
					t.Fatalf("oversize: %d %s calls=%d", rr.Code, rr.Body.String(), calls)
				}
			} else if rr.Code != 201 || calls != before+1 {
				t.Fatalf("supported: %d", rr.Code)
			}
		}
	}
	req := httptest.NewRequest("POST", "/admin/body-test", strings.NewReader("{}"))
	req.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 415 || calls != 2 {
		t.Fatalf("compressed: %d calls=%d", rr.Code, calls)
	}
}

func TestAdminPanicJSON(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	h := NewHandler(nil, zap.New(core))
	h.mux.HandleFunc("GET /admin/panic-test", func(http.ResponseWriter, *http.Request) { panic("private diagnostic") })
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/admin/panic-test", nil))
	if rr.Code != 500 || rr.Body.String() != "{\"error\":\"internal server error\"}\n" || rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	entries := logs.FilterMessage("admin request panicked").All()
	if len(entries) != 1 || entries[0].ContextMap()["panic"] != "private diagnostic" {
		t.Fatalf("missing operator panic diagnostic: %+v", entries)
	}

}

func TestCommunityBodyBoundaryLeavesStoreUnchanged(t *testing.T) {
	store := &testProviderConfigStore{items: map[string]*provider.ProviderConfig{}}
	gateway := newTestAgentGateway(&testConfigStore{providerStore: store}, nil, nil, nil, nil)
	defer gateway.Close()
	h := NewHandler(gateway, nil)
	payload := `{"id":"body-provider","provider_type":"openai","base_url":"https://example.com"}`
	for _, unknown := range []bool{false, true} {
		req := httptest.NewRequest("POST", "/admin/llm/providers", strings.NewReader(payload+strings.Repeat(" ", MaxRequestBodyBytes)))
		if unknown {
			req.ContentLength = -1
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 413 || len(store.items) != 0 {
			t.Fatalf("oversized write: %d %#v", rr.Code, store.items)
		}
	}
	req := httptest.NewRequest("POST", "/admin/llm/providers", strings.NewReader(payload))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 201 || len(store.items) != 1 {
		t.Fatalf("supported write: %d %s", rr.Code, rr.Body.String())
	}
}

func TestAdminRejectsCompressionInRepeatedHeaders(t *testing.T) {
	h := NewHandler(nil, nil)
	called := false
	h.mux.HandleFunc("POST /admin/compression-test", func(http.ResponseWriter, *http.Request) { called = true })
	req := httptest.NewRequest("POST", "/admin/compression-test", strings.NewReader(`{}`))
	req.Header.Add("Content-Encoding", "identity")
	req.Header.Add("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 415 || called {
		t.Fatalf("repeated compressed encoding admitted: %d", rr.Code)
	}
}
