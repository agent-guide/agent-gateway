package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestClientUsesOfficialMethodVersionAndTenant(t *testing.T) {
	methods := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if r.Header.Get("A2A-Version") != "1.0" {
			t.Errorf("A2A-Version = %q", r.Header.Get("A2A-Version"))
		}
		params, _ := request["params"].(map[string]any)
		if params["tenant"] != "tenant-a" {
			t.Errorf("tenant = %#v", params["tenant"])
		}
		method, _ := request["method"].(string)
		id, _ := request["id"].(string)
		methods <- method
		w.Header().Set("Content-Type", "application/json")
		if method == "CancelTask" {
			fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"id\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_CANCELED\"}}}", id)
			return
		}
		fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"message\":{\"messageId\":\"m1\",\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"ok\"}]}}}", id)
	}))
	defer server.Close()
	iface := a2a.AgentInterface{URL: server.URL, ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version, Tenant: "tenant-a"}
	client, err := New(context.Background(), iface, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	_, err = client.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: "task-1"})
	if err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	if got := <-methods; got != "SendMessage" {
		t.Fatalf("method = %q", got)
	}
	if got := <-methods; got != "CancelTask" {
		t.Fatalf("method = %q", got)
	}
}

func TestClientRejectsWrongResponseEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{\"jsonrpc\":\"2.0\",\"id\":\"wrong\",\"result\":{}}")
	}))
	defer server.Close()
	iface := a2a.AgentInterface{URL: server.URL, ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version}
	client, err := New(context.Background(), iface, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))})
	if err == nil {
		t.Fatal("mismatched response id accepted")
	}
}

func TestGuardTransportRejectsMissingBody(t *testing.T) {
	transport := &guardTransport{base: http.DefaultTransport}
	request, err := http.NewRequest(http.MethodPost, "http://example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(request); err == nil || !strings.Contains(err.Error(), "body is required") {
		t.Fatalf("RoundTrip() error = %v", err)
	}
}

func TestGuardTransportEnforcesRequestAndResponseBodyLimits(t *testing.T) {
	baseCalled := false
	transport := &guardTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		baseCalled = true
		return nil, errors.New("unexpected request")
	})}
	request, err := http.NewRequest(http.MethodPost, "http://example.invalid", io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), int(MaxBodyBytes+1)))))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(request); err == nil || !strings.Contains(err.Error(), "request exceeds") {
		t.Fatalf("oversized request error = %v", err)
	}
	if baseCalled {
		t.Fatal("oversized request reached the network transport")
	}

	transport.base = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), int(MaxBodyBytes+1)))),
		}, nil
	})
	request, err = http.NewRequest(http.MethodPost, "http://example.invalid", strings.NewReader(`{"jsonrpc":"2.0","id":"1","method":"SendMessage","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(request); err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestAcceptsEventStreamMediaTypeInList(t *testing.T) {
	if !acceptsMediaType("application/json, text/event-stream; q=0.8", "text/event-stream") {
		t.Fatal("event stream with quality parameter was not recognized")
	}
	if acceptsMediaType("text/event-stream; q=0.0", "text/event-stream") {
		t.Fatal("disabled event stream media type was accepted")
	}
}

func TestClientStreamsValidatedSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["method"] != "SendStreamingMessage" {
			t.Errorf("method = %#v", request["method"])
		}
		id, _ := request["id"].(string)
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		fmt.Fprintf(w, ": heartbeat\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"message\":{\"messageId\":\"m1\",\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"ok\"}]}}}\n\n", id)
	}))
	defer server.Close()
	iface := a2a.AgentInterface{URL: server.URL, ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version}
	client, err := New(context.Background(), iface, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	var events int
	for _, streamErr := range client.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))}) {
		if streamErr != nil {
			t.Fatalf("stream error = %v", streamErr)
		}
		events++
	}
	if events != 1 {
		t.Fatalf("events = %d, want 1", events)
	}
}

func TestClientRejectsDataLessNamedSSEEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: update\n\n")
	}))
	defer server.Close()
	iface := a2a.AgentInterface{URL: server.URL, ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version}
	client, err := New(context.Background(), iface, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	var gotErr error
	for _, streamErr := range client.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))}) {
		gotErr = streamErr
	}
	if gotErr == nil {
		t.Fatal("data-less named event accepted")
	}
}

func TestValidatedSSEEnforcesEventLimitWhileReading(t *testing.T) {
	var output bytes.Buffer
	err := copyValidatedSSEWithLimits(&output, strings.NewReader("data: "+strings.Repeat("x", 128)), json.RawMessage(`"1"`), 32, 1024)
	if err == nil || !strings.Contains(err.Error(), "SSE event exceeds 32 bytes") {
		t.Fatalf("copyValidatedSSEWithLimits() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("oversized event wrote %d bytes", output.Len())
	}
}

func TestValidatedSSEEnforcesAggregateLimit(t *testing.T) {
	var output bytes.Buffer
	err := copyValidatedSSEWithLimits(&output, strings.NewReader(": heartbeat\n\n: heartbeat\n\n"), json.RawMessage(`"1"`), 32, 20)
	if err == nil || !strings.Contains(err.Error(), "stream exceeds 20 bytes") {
		t.Fatalf("copyValidatedSSEWithLimits() error = %v", err)
	}
}

func TestClientReturnsTypedHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	iface := a2a.AgentInterface{URL: server.URL, ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version}
	client, err := New(context.Background(), iface, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))})
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("SendMessage() error = %v", err)
	}
}

func TestIdleTimeoutBodyAbortsBlockedRead(t *testing.T) {
	source, writer := io.Pipe()
	defer writer.Close()
	body := newIdleTimeoutBody(source, 10*time.Millisecond)
	defer body.Close()
	_, err := body.Read(make([]byte, 1))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Read() error = %v, want deadline exceeded", err)
	}
}
