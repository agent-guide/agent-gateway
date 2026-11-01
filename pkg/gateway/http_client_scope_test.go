package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	agentruntime "github.com/agent-guide/agent-gateway/pkg/agent/runtime"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestHTTPForeignKeyCannotResumeSession(t *testing.T) {
	var mu sync.Mutex
	var inboundContexts []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/card" {
			writeTestAgentCard(w, server.URL+"/a2a", false)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		params, _ := request["params"].(map[string]any)
		message, _ := params["message"].(map[string]any)
		contextID, _ := message["contextId"].(string)
		mu.Lock()
		inboundContexts = append(inboundContexts, contextID)
		mu.Unlock()
		id, _ := request["id"].(string)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"task\":{\"id\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\",\"message\":{\"messageId\":\"m1\",\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"answer\"}]}},\"artifacts\":[{\"artifactId\":\"a1\",\"parts\":[{\"text\":\"artifact\"}]}]}}}", id)
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	backend := NewHTTPBackend(manager)
	capabilities, err := backend.Capabilities(context.Background(), agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range capabilities.Events {
		if event == agentruntime.EventDelta {
			t.Fatal("HTTP backend advertises delta events that it never emits")
		}
	}

	sessionID := ""
	client := func(key string) context.Context {
		return usage.ContextWithDimensions(t.Context(), usage.InteractionDimensions{RouteID: "route", VirtualKeyID: key})
	}
	if err := backend.ServeTurn(client("key-one"), agent, agentruntime.TurnRequest{Input: "first"}, func(ev agentruntime.TurnEvent) error {
		if ev.Event == agentruntime.EventSession {
			sessionID = ev.SessionID
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err = backend.ServeTurn(client("key-two"), agent, agentruntime.TurnRequest{Input: "foreign", SessionID: sessionID}, func(agentruntime.TurnEvent) error { return nil })
	if !errors.Is(err, agentruntime.ErrSessionNotFound) {
		t.Fatalf("foreign key resumed another client's session: %v", err)
	}
}
