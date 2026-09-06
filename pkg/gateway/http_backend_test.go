package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	agentruntime "github.com/agent-guide/agent-gateway/pkg/agent/runtime"
)

func TestHTTPBackendNonStreamingTaskAndContextResume(t *testing.T) {
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

	for turn := 0; turn < 2; turn++ {
		var events []agentruntime.TurnEvent
		err := backend.ServeTurn(context.Background(), agent, agentruntime.TurnRequest{
			Input: "hello", SessionID: "session-1",
		}, func(event agentruntime.TurnEvent) error {
			events = append(events, event)
			return nil
		})
		if err != nil {
			t.Fatalf("ServeTurn(%d) error = %v", turn, err)
		}
		if len(events) != 4 || events[0].Event != agentruntime.EventSession ||
			events[1].Text != "answer" || events[2].Text != "artifact" ||
			events[3].Event != agentruntime.EventDone {
			t.Fatalf("events(%d) = %#v", turn, events)
		}
		var session map[string]any
		if err := json.Unmarshal(events[0].Data, &session); err != nil {
			t.Fatal(err)
		}
		if resumed, _ := session["resumed"].(bool); resumed != (turn == 1) {
			t.Fatalf("turn %d resumed = %#v", turn, session["resumed"])
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inboundContexts) != 2 || inboundContexts[0] != "" || inboundContexts[1] != "ctx-1" {
		t.Fatalf("inbound contexts = %#v", inboundContexts)
	}
}

func TestHTTPSessionBindingsClaimAndExpiry(t *testing.T) {
	bindings := newHTTPSessionBindings()
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	bindings.now = func() time.Time { return now }
	first, err := bindings.claim("session-1", true)
	if err != nil || first.resetReason != "binding_missing" {
		t.Fatalf("first claim = %#v, %v", first, err)
	}
	if _, err := bindings.claim("session-1", true); !errors.Is(err, agentruntime.ErrSessionBusy) {
		t.Fatalf("concurrent claim error = %v", err)
	}
	bindings.finish(first, "ctx-1", "task-1")
	resumed, err := bindings.claim("session-1", true)
	if err != nil || !resumed.resumed || resumed.original.taskID != "task-1" {
		t.Fatalf("resumed claim = %#v, %v", resumed, err)
	}
	bindings.finish(resumed, "ctx-1", "")
	now = now.Add(httpBindingTTL + time.Second)
	expired, err := bindings.claim("session-1", true)
	if err != nil || expired.resetReason != "binding_expired" || expired.resumed {
		t.Fatalf("expired claim = %#v, %v", expired, err)
	}
}

func TestHTTPBackendInputRequiredResumesTask(t *testing.T) {
	var mu sync.Mutex
	var messages []map[string]any
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/card" {
			writeTestAgentCard(w, server.URL+"/a2a", true)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		params, _ := request["params"].(map[string]any)
		message, _ := params["message"].(map[string]any)
		mu.Lock()
		messages = append(messages, message)
		call := len(messages)
		mu.Unlock()
		id, _ := request["id"].(string)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"task\":{\"id\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_INPUT_REQUIRED\"}}}}\n\n", id)
			return
		}
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"message\":{\"messageId\":\"m2\",\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"continued\"}],\"taskId\":\"task-1\",\"contextId\":\"ctx-1\"}}}\n\n", id)
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	backend := NewHTTPBackend(manager)

	var first []agentruntime.TurnEvent
	if err := backend.ServeTurn(context.Background(), agent, agentruntime.TurnRequest{Input: "start", SessionID: "session-1"}, func(event agentruntime.TurnEvent) error {
		first = append(first, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[1].Event != agentruntime.EventDone {
		t.Fatalf("first events = %#v", first)
	}
	var done map[string]any
	_ = json.Unmarshal(first[1].Data, &done)
	if done["stop_reason"] != "input_required" || done["task_id"] != "task-1" {
		t.Fatalf("done = %#v", done)
	}
	if err := backend.ServeTurn(context.Background(), agent, agentruntime.TurnRequest{Input: "more", SessionID: "session-1"}, func(agentruntime.TurnEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 2 || messages[1]["taskId"] != "task-1" || messages[1]["contextId"] != "ctx-1" {
		t.Fatalf("follow-up message = %#v", messages)
	}
}

func writeTestAgentCard(w http.ResponseWriter, endpoint string, streaming bool) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "{\"name\":\"Remote\",\"description\":\"\",\"version\":\"1\",\"capabilities\":{\"streaming\":%t},"+
		"\"defaultInputModes\":[\"text/plain\"],\"defaultOutputModes\":[\"text/plain\"],\"skills\":[],"+
		"\"supportedInterfaces\":[{\"url\":%q,\"protocolBinding\":\"JSONRPC\",\"protocolVersion\":\"1.0\"}]}", streaming, endpoint)
}
