package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/a2a/jsonrpc"
)

func TestProxyForwardsOriginalBodyAndGovernsHeaders(t *testing.T) {
	body := []byte("{ \"jsonrpc\": \"2.0\", \"id\": 7, \"method\": \"SendMessage\", \"params\": {} }")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if !bytes.Equal(got, body) {
			t.Errorf("body changed: %q", got)
		}
		if r.Header.Get("Authorization") != "Bearer upstream-secret" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-For") != "" {
			t.Errorf("sensitive headers leaked: %#v", r.Header)
		}
		if r.Header.Get("A2A-Version") != "1.0" || r.URL.Query().Get("A2A-Version") != "" || r.URL.Query().Get("x") != "1" {
			t.Errorf("version/query = %q / %q", r.Header.Get("A2A-Version"), r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "upstream=secret")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`)
	}))
	defer upstream.Close()

	upstreamClient := upstream.Client()
	upstreamClient.Transport = bearerTestTransport{base: upstreamClient.Transport, token: "upstream-secret"}
	p, err := New(Options{InterfaceURL: upstream.URL + "/rpc?fixed=1", HTTPClient: upstreamClient})
	if err != nil {
		t.Fatal(err)
	}
	in := httptest.NewRequest(http.MethodPost, "https://gateway.example/a2a?A2A-Version=1.0&x=1", bytes.NewReader(body))
	in.Header.Set("Authorization", "Bearer virtual-key")
	in.Header.Set("X-Api-Key", "virtual-key")
	in.Header.Set("Cookie", "client=secret")
	in.Header.Set("X-Forwarded-For", "203.0.113.1")
	meta, err := jsonrpc.InspectRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Do(in.Context(), in, body, meta)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Close()
	if resp.Header.Get("Set-Cookie") != "" || !bytes.Contains(resp.Buffered, []byte(`"ok":true`)) {
		t.Fatalf("response = headers %#v body %q", resp.Header, resp.Buffered)
	}
}

func TestNewDoesNotMutateDefaultHTTPClient(t *testing.T) {
	originalCheckRedirect := http.DefaultClient.CheckRedirect
	http.DefaultClient.CheckRedirect = nil
	t.Cleanup(func() { http.DefaultClient.CheckRedirect = originalCheckRedirect })

	p, err := New(Options{InterfaceURL: "https://agent.example/rpc"})
	if err != nil {
		t.Fatal(err)
	}
	if http.DefaultClient.CheckRedirect != nil {
		t.Fatal("New mutated http.DefaultClient.CheckRedirect")
	}
	if p.client == http.DefaultClient {
		t.Fatal("proxy retained the process-wide default client")
	}
	if err := p.client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("proxy redirect policy error = %v", err)
	}
}

func TestProxyAppliesIdleTimeoutToNonStreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()

	p, err := New(Options{
		InterfaceURL: upstream.URL,
		HTTPClient:   upstream.Client(),
		TotalTimeout: 2 * time.Second,
		IdleTimeout:  20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"GetTask","params":{"id":"task-1"}}`)
	meta, err := jsonrpc.InspectRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	in := httptest.NewRequest(http.MethodPost, "https://gateway.example/a2a", bytes.NewReader(body))

	started := time.Now()
	_, err = p.Do(in.Context(), in, body, meta)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Do error = %v, want idle deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("non-stream response timed out after %v, want idle timeout before total timeout", elapsed)
	}
}

type bearerTestTransport struct {
	base  http.RoundTripper
	token string
}

func (t bearerTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

func TestCopyStreamPreservesFramesAndRejectsWrongID(t *testing.T) {
	good := []byte("data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{}}\n\n")
	var out bytes.Buffer
	if err := CopyStream(&out, bytes.NewReader(good), []byte("7")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), good) {
		t.Fatalf("stream changed: %q", out.Bytes())
	}
	if err := CopyStream(io.Discard, bytes.NewReader(good), []byte("8")); !IsInvalidResponse(err) {
		t.Fatalf("wrong id error = %v", err)
	}
	if err := CopyStream(io.Discard, strings.NewReader("event: update\n\n"), []byte("7")); !IsInvalidResponse(err) {
		t.Fatalf("invalid framing error = %v", err)
	}
}
