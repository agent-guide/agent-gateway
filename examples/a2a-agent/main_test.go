package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2acard "github.com/agent-guide/agent-gateway/pkg/a2a/card"
	a2aclient "github.com/agent-guide/agent-gateway/pkg/a2a/client"
	a2ajsonrpc "github.com/agent-guide/agent-gateway/pkg/a2a/jsonrpc"
	a2aproxy "github.com/agent-guide/agent-gateway/pkg/a2a/proxy"
)

func TestExampleCardAndPathBClient(t *testing.T) {
	server := httptest.NewServer(exampleHandler{})
	defer server.Close()

	snapshot, _, err := a2acard.Fetch(context.Background(), server.Client(), server.URL+cardPath, a2acard.Validators{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Interfaces) != 1 || snapshot.Interfaces[0].URL != server.URL+rpcPath {
		t.Fatalf("interfaces = %#v", snapshot.Interfaces)
	}
	client, err := a2aclient.New(context.Background(), snapshot.Interfaces[0], server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	request := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))}
	result, err := client.SendMessage(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("SendMessage returned no result")
	}
	var events int
	for _, streamErr := range client.SendStreamingMessage(context.Background(), request) {
		if streamErr != nil {
			t.Fatal(streamErr)
		}
		events++
	}
	if events != 2 {
		t.Fatalf("stream events = %d, want 2", events)
	}
}

func TestExamplePathAProxyStripsIngressCredentials(t *testing.T) {
	server := httptest.NewServer(exampleHandler{})
	defer server.Close()
	proxy, err := a2aproxy.New(a2aproxy.Options{InterfaceURL: server.URL + rpcPath, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		body string
	}{
		{"sync", `{"jsonrpc":"2.0","id":"proxy-sync","method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"text":"hello"}]}}}`},
		{"stream", `{"jsonrpc":"2.0","id":"proxy-stream","method":"SendStreamingMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"text":"hello"}]}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meta, err := a2ajsonrpc.InspectRequest([]byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			inbound := httptest.NewRequest(http.MethodPost, "http://gateway.example/a2a", strings.NewReader(test.body))
			inbound.Header.Set("Authorization", "Bearer ingress-secret")
			inbound.Header.Set("X-Api-Key", "ingress-secret")
			response, err := proxy.Do(context.Background(), inbound, []byte(test.body), meta)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Close()
			if !response.Streaming {
				if !bytes.Contains(response.Buffered, []byte(contextID)) {
					t.Fatalf("response = %s", response.Buffered)
				}
				return
			}
			var output bytes.Buffer
			if err := a2aproxy.CopyStream(&output, response.Body, meta.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(io.Discard, response.Body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "TASK_STATE_COMPLETED") {
				t.Fatalf("stream = %s", output.String())
			}
		})
	}
}
