package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/a2a/jsonrpc"
)

func TestProxyForwardsOriginalBodyAndGovernsHeaders(t *testing.T) {
	body := []byte("{ \"jsonrpc\": \"2.0\", \"id\": 7, \"method\": \"SendMessage\", \"params\": {} }")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if !bytes.Equal(got, body) {
			t.Errorf("body changed: %q", got)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-For") != "" {
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

	p, err := New(Options{InterfaceURL: upstream.URL + "/rpc?fixed=1", HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	in := httptest.NewRequest(http.MethodPost, "https://gateway.example/a2a?A2A-Version=1.0&x=1", bytes.NewReader(body))
	in.Header.Set("Authorization", "Bearer virtual-key")
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
}
