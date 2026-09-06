package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

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
