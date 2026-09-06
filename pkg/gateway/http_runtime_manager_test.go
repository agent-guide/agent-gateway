package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/credential"
)

func TestHTTPRuntimeManagerReusesCardAndUsesLiveCredential(t *testing.T) {
	var mu sync.Mutex
	cardRequests := 0
	var authorizations []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card":
			mu.Lock()
			cardRequests++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "{\"name\":\"Remote\",\"description\":\"\",\"version\":\"1\",\"capabilities\":{\"streaming\":false},"+
				"\"defaultInputModes\":[\"text/plain\"],\"defaultOutputModes\":[\"text/plain\"],\"skills\":[],"+
				"\"supportedInterfaces\":[{\"url\":%q,\"protocolBinding\":\"JSONRPC\",\"protocolVersion\":\"1.0\",\"tenant\":\"tenant-a\"}],"+
				"\"securitySchemes\":{\"bearer\":{\"httpAuthSecurityScheme\":{\"scheme\":\"Bearer\"}}},"+
				"\"securityRequirements\":[{\"schemes\":{\"bearer\":[]}}]}", server.URL+"/a2a")
		case "/a2a":
			mu.Lock()
			authorizations = append(authorizations, r.Header.Get("Authorization"))
			mu.Unlock()
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			id, _ := request["id"].(string)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"message\":{\"messageId\":\"m1\",\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"ok\"}]}}}", id)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	credentials := credential.NewManager(nil)
	cred := &credential.Credential{
		ID: "remote-key", Type: credential.TypeAPIKey, Scope: credential.HTTPAgentCredentialScope("remote"),
		Attributes: map[string]string{"api_key": "first"},
	}
	if err := credentials.RegisterCredential(context.Background(), cred); err != nil {
		t.Fatal(err)
	}
	manager := NewHTTPRuntimeManager(nil, credentials, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "remote-key")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	first, err := manager.ResolveExecution("remote")
	if err != nil {
		t.Fatal(err)
	}
	if first.Interface.Tenant != "tenant-a" || first.Streaming {
		t.Fatalf("execution = %#v", first)
	}
	send := func() {
		t.Helper()
		_, err := first.Client.SendMessage(context.Background(), &a2a.SendMessageRequest{
			Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello")),
		})
		if err != nil {
			t.Fatalf("SendMessage() error = %v", err)
		}
	}
	send()
	rotated := cred.Clone()
	rotated.Attributes["api_key"] = "second"
	if err := credentials.UpdateCredential(context.Background(), rotated); err != nil {
		t.Fatal(err)
	}
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	second, err := manager.ResolveExecution("remote")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("secret-only rotation replaced execution handle")
	}
	send()
	mu.Lock()
	defer mu.Unlock()
	if cardRequests != 1 {
		t.Fatalf("Card requests = %d, want 1", cardRequests)
	}
	if len(authorizations) != 2 || authorizations[0] != "Bearer first" || authorizations[1] != "Bearer second" {
		t.Fatalf("Authorization values = %#v", authorizations)
	}
}

func TestHTTPRuntimeManagerRejectsWrongCredentialOwnerWithoutRefetch(t *testing.T) {
	cardRequests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cardRequests++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"name\":\"Remote\",\"description\":\"\",\"version\":\"1\",\"capabilities\":{},"+
			"\"defaultInputModes\":[\"text/plain\"],\"defaultOutputModes\":[\"text/plain\"],\"skills\":[],"+
			"\"supportedInterfaces\":[{\"url\":%q,\"protocolBinding\":\"JSONRPC\",\"protocolVersion\":\"1.0\"}],"+
			"\"securitySchemes\":{\"bearer\":{\"httpAuthSecurityScheme\":{\"scheme\":\"Bearer\"}}},"+
			"\"securityRequirements\":[{\"schemes\":{\"bearer\":[]}}]}", server.URL+"/a2a")
	}))
	defer server.Close()
	credentials := credential.NewManager(nil)
	if err := credentials.RegisterCredential(context.Background(), &credential.Credential{
		ID: "wrong", Type: credential.TypeAPIKey, Scope: credential.HTTPAgentCredentialScope("other"),
		Attributes: map[string]string{"api_key": "secret"},
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewHTTPRuntimeManager(nil, credentials, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "wrong")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	if _, err := manager.ResolveExecution("remote"); err == nil {
		t.Fatal("wrong-owner credential made agent executable")
	}
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	if cardRequests != 1 {
		t.Fatalf("Card requests = %d, want cached Card reuse", cardRequests)
	}
}

func TestHTTPRuntimeManagerHealthIsConditionalAndCoalesced(t *testing.T) {
	requests := 0
	conditional := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == "\"v1\"" {
			conditional++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", "\"v1\"")
		writeTestAgentCard(w, server.URL+"/a2a", false)
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	first := manager.ProbeHealth(context.Background(), "remote")
	second := manager.ProbeHealth(context.Background(), "remote")
	if !first.Healthy || !second.Healthy || first.Drift || second.Drift {
		t.Fatalf("health probes = %#v, %#v", first, second)
	}
	if requests != 2 || conditional != 1 {
		t.Fatalf("requests = %d, conditional = %d", requests, conditional)
	}
}

func testHTTPAgent(id, cardURL, authRef string) agentpkg.Agent {
	return agentpkg.Agent{
		ID: id, Name: id,
		Runtime: agentpkg.Runtime{Type: agentpkg.RuntimeTypeHTTP, HTTP: &agentpkg.HTTPRuntime{
			CardURL: cardURL, Protocol: "a2a", AuthRef: authRef,
		}},
	}
}
