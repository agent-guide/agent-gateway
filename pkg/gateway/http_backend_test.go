package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	agentruntime "github.com/agent-guide/agent-gateway/pkg/agent/runtime"
)

func TestHTTPBackendAllocatesSessionAndResumesContext(t *testing.T) {
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
	for turn := 0; turn < 2; turn++ {
		var events []agentruntime.TurnEvent
		err := backend.ServeTurn(context.Background(), agent, agentruntime.TurnRequest{
			Input: "hello", SessionID: sessionID,
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
		if turn == 0 {
			sessionID = events[0].SessionID
			if sessionID == "" {
				t.Fatal("omitted session_id did not allocate a gateway session id")
			}
		} else if events[0].SessionID != sessionID {
			t.Fatalf("resumed session id = %q, want %q", events[0].SessionID, sessionID)
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

func TestHTTPBackendDirectMessageRetainsReturnedContext(t *testing.T) {
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
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"message":{"messageId":"m1","role":"ROLE_AGENT","parts":[{"text":"answer"}],"contextId":"ctx-direct"}}}`, id)
	}))
	defer server.Close()

	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(t.Context(), []agentpkg.Agent{agent})
	backend := NewHTTPBackend(manager)
	for turn := 0; turn < 2; turn++ {
		var events []agentruntime.TurnEvent
		if err := backend.ServeTurn(t.Context(), agent, agentruntime.TurnRequest{
			Input: "hello", SessionID: "session-direct",
		}, func(event agentruntime.TurnEvent) error {
			events = append(events, event)
			return nil
		}); err != nil {
			t.Fatalf("ServeTurn(%d) error = %v", turn, err)
		}
		if len(events) != 3 || events[0].Event != agentruntime.EventSession || events[1].Text != "answer" || events[2].Event != agentruntime.EventDone {
			t.Fatalf("events(%d) = %#v", turn, events)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inboundContexts) != 2 || inboundContexts[0] != "" || inboundContexts[1] != "ctx-direct" {
		t.Fatalf("inbound contexts = %#v", inboundContexts)
	}
}

func TestHTTPBackendRejectsStreamAfterDirectMessage(t *testing.T) {
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
		id, _ := request["id"].(string)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"message\":{\"messageId\":\"m1\",\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"answer\"}],\"contextId\":\"ctx-direct\"}}}\n\n", id)
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"statusUpdate\":{\"taskId\":\"task-1\",\"contextId\":\"ctx-direct\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}}}\n\n", id)
	}))
	defer server.Close()

	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(t.Context(), []agentpkg.Agent{agent})
	err := NewHTTPBackend(manager).ServeTurn(t.Context(), agent, agentruntime.TurnRequest{
		Input: "hello", SessionID: "session-direct",
	}, func(agentruntime.TurnEvent) error { return nil })
	if !errors.Is(err, agentruntime.ErrTurnFailed) || !strings.Contains(err.Error(), "continued after a terminal event") {
		t.Fatalf("ServeTurn() error = %v, want terminal continuation rejection", err)
	}
}

func TestHTTPBackendEnforcesTotalTurnTimeout(t *testing.T) {
	release := make(chan struct{})
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/card" {
			writeTestAgentCard(w, server.URL+"/a2a", false)
			return
		}
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	agent.Runtime.HTTP.TimeoutSeconds = 1
	manager.RefreshRuntimeConfigs(t.Context(), []agentpkg.Agent{agent})
	started := time.Now()
	err := NewHTTPBackend(manager).ServeTurn(t.Context(), agent, agentruntime.TurnRequest{Input: "hello"}, func(agentruntime.TurnEvent) error { return nil })
	if !errors.Is(err, agentruntime.ErrBackendTimeout) {
		t.Fatalf("ServeTurn() error = %v, want backend_timeout", err)
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("turn elapsed = %s, want total timeout near 1s", elapsed)
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

func TestHTTPSessionBindingsEnforceLRUCap(t *testing.T) {
	bindings := newHTTPSessionBindings()
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	bindings.now = func() time.Time { return now }
	for i := 0; i <= httpBindingCap; i++ {
		sessionID := fmt.Sprintf("session-%04d", i)
		claim, err := bindings.claim(sessionID, false)
		if err != nil {
			t.Fatal(err)
		}
		bindings.finish(claim, fmt.Sprintf("context-%04d", i), "")
		now = now.Add(time.Second)
	}
	if len(bindings.entries) != httpBindingCap {
		t.Fatalf("binding count = %d, want %d", len(bindings.entries), httpBindingCap)
	}
	if _, ok := bindings.entries["session-0000"]; ok {
		t.Fatal("oldest binding was not evicted")
	}
	if _, ok := bindings.entries[fmt.Sprintf("session-%04d", httpBindingCap)]; !ok {
		t.Fatal("newest binding was evicted")
	}
}

func TestEmitA2AStatusCoversNonterminalAuthAndUnknownStates(t *testing.T) {
	var events []agentruntime.TurnEvent
	emit := func(event agentruntime.TurnEvent) error {
		events = append(events, event)
		return nil
	}
	binding, terminal, err := emitA2AStatus(a2a.TaskStatus{State: a2a.TaskStateSubmitted}, "task-1", "ctx-1", emit)
	if err != nil || terminal || binding.contextID != "ctx-1" || len(events) != 0 {
		t.Fatalf("SUBMITTED = %#v, terminal=%v, events=%#v, err=%v", binding, terminal, events, err)
	}
	statusMessage := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("queued"))
	binding, terminal, err = emitA2AStatus(a2a.TaskStatus{State: a2a.TaskStateSubmitted, Message: statusMessage}, "task-1", "ctx-1", emit)
	if err != nil || terminal || binding.contextID != "ctx-1" || len(events) != 1 || events[0].Text != "queued" {
		t.Fatalf("SUBMITTED with content = %#v, terminal=%v, events=%#v, err=%v", binding, terminal, events, err)
	}
	binding, terminal, err = emitA2AStatus(a2a.TaskStatus{State: a2a.TaskStateWorking}, "task-1", "ctx-1", emit)
	if err != nil || terminal || binding.contextID != "ctx-1" || len(events) != 1 {
		t.Fatalf("WORKING = %#v, terminal=%v, events=%#v, err=%v", binding, terminal, events, err)
	}
	binding, terminal, err = emitA2AStatus(a2a.TaskStatus{State: a2a.TaskStateAuthRequired}, "task-1", "ctx-1", emit)
	if err == nil || !terminal || !binding.suppressCancel || binding.contextID != "ctx-1" || binding.interruptedTaskID != "" {
		t.Fatalf("AUTH_REQUIRED = %#v, terminal=%v, err=%v", binding, terminal, err)
	}
	if _, terminal, err = emitA2AStatus(a2a.TaskStatus{State: a2a.TaskState("TASK_STATE_FUTURE")}, "task-1", "ctx-1", emit); err == nil || terminal {
		t.Fatalf("unknown state terminal=%v, err=%v", terminal, err)
	}
}

func TestHTTPBackendAuthRequiredRetainsContextWithoutCancelOrTask(t *testing.T) {
	var mu sync.Mutex
	cancelCalls := 0
	var messages []map[string]any
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
		method, _ := request["method"].(string)
		id, _ := request["id"].(string)
		w.Header().Set("Content-Type", "application/json")
		if method == "CancelTask" {
			mu.Lock()
			cancelCalls++
			mu.Unlock()
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"id":"task-1","contextId":"ctx-1","status":{"state":"TASK_STATE_CANCELED"}}}`, id)
			return
		}
		params, _ := request["params"].(map[string]any)
		message, _ := params["message"].(map[string]any)
		mu.Lock()
		messages = append(messages, message)
		call := len(messages)
		mu.Unlock()
		if call == 1 {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"task":{"id":"task-1","contextId":"ctx-1","status":{"state":"TASK_STATE_AUTH_REQUIRED"}}}}`, id)
			return
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"task":{"id":"task-2","contextId":"ctx-1","status":{"state":"TASK_STATE_COMPLETED"}}}}`, id)
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	backend := NewHTTPBackend(manager)
	if err := backend.ServeTurn(context.Background(), agent, agentruntime.TurnRequest{Input: "start", SessionID: "session-1"}, func(agentruntime.TurnEvent) error { return nil }); err == nil {
		t.Fatal("AUTH_REQUIRED turn succeeded")
	}
	if err := backend.ServeTurn(context.Background(), agent, agentruntime.TurnRequest{Input: "continue", SessionID: "session-1"}, func(agentruntime.TurnEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if cancelCalls != 0 {
		t.Fatalf("CancelTask calls = %d, want 0", cancelCalls)
	}
	if len(messages) != 2 || messages[1]["contextId"] != "ctx-1" {
		t.Fatalf("resumed messages = %#v", messages)
	}
	if _, ok := messages[1]["taskId"]; ok {
		t.Fatalf("AUTH_REQUIRED retained resumable task id: %#v", messages[1])
	}
}

func TestHTTPBackendSynchronousSubmittedCancelsAndFails(t *testing.T) {
	var mu sync.Mutex
	cancelCalls := 0
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
		method, _ := request["method"].(string)
		id, _ := request["id"].(string)
		w.Header().Set("Content-Type", "application/json")
		if method == "CancelTask" {
			mu.Lock()
			cancelCalls++
			mu.Unlock()
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"id":"task-1","contextId":"ctx-1","status":{"state":"TASK_STATE_CANCELED"}}}`, id)
			return
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"task":{"id":"task-1","contextId":"ctx-1","status":{"state":"TASK_STATE_SUBMITTED"}}}}`, id)
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	err := NewHTTPBackend(manager).ServeTurn(context.Background(), agent, agentruntime.TurnRequest{Input: "start"}, func(agentruntime.TurnEvent) error { return nil })
	if err == nil || !errors.Is(err, agentruntime.ErrTurnFailed) {
		t.Fatalf("ServeTurn() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if cancelCalls != 1 {
		t.Fatalf("CancelTask calls = %d, want 1", cancelCalls)
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

func TestHTTPBackendPreSendFailureRestoresInterruptedBinding(t *testing.T) {
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
	request := agentruntime.TurnRequest{Input: "continue", SessionID: "session-1"}
	if err := backend.ServeTurn(context.Background(), agent, agentruntime.TurnRequest{Input: "start", SessionID: request.SessionID}, func(agentruntime.TurnEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	emitErr := errors.New("northbound stream closed")
	if err := backend.ServeTurn(context.Background(), agent, request, func(event agentruntime.TurnEvent) error {
		if event.Event == agentruntime.EventSession {
			return emitErr
		}
		return nil
	}); !errors.Is(err, emitErr) {
		t.Fatalf("pre-send ServeTurn() error = %v, want %v", err, emitErr)
	}
	if err := backend.ServeTurn(context.Background(), agent, request, func(agentruntime.TurnEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 2 {
		t.Fatalf("southbound messages = %d, want 2", len(messages))
	}
	if messages[1]["taskId"] != "task-1" || messages[1]["contextId"] != "ctx-1" {
		t.Fatalf("binding was not restored after pre-send failure: %#v", messages[1])
	}
}

func TestHTTPBackendStreamsArtifactUpdateAndRejectsIdentityDrift(t *testing.T) {
	for _, test := range []struct {
		name         string
		artifactTask string
		wantError    bool
	}{
		{name: "matching identity", artifactTask: "task-1"},
		{name: "task identity drift", artifactTask: "task-other", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
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
				method, _ := request["method"].(string)
				id, _ := request["id"].(string)
				if method == "CancelTask" {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%q,"result":{"id":"task-1","contextId":"ctx-1","status":{"state":"TASK_STATE_CANCELED"}}}`, id)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"task\":{\"id\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_WORKING\"}}}}\n\n", id)
				fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"artifactUpdate\":{\"taskId\":%q,\"contextId\":\"ctx-1\",\"artifact\":{\"artifactId\":\"artifact-1\",\"parts\":[{\"text\":\"artifact text\"}]}}}}\n\n", id, test.artifactTask)
				if !test.wantError {
					fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"statusUpdate\":{\"taskId\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_COMPLETED\"}}}}\n\n", id)
				}
			}))
			defer server.Close()
			manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
			agent := testHTTPAgent("remote", server.URL+"/card", "")
			manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
			var events []agentruntime.TurnEvent
			err := NewHTTPBackend(manager).ServeTurn(context.Background(), agent, agentruntime.TurnRequest{Input: "start"}, func(event agentruntime.TurnEvent) error {
				events = append(events, event)
				return nil
			})
			if test.wantError {
				if err == nil || !errors.Is(err, agentruntime.ErrTurnFailed) {
					t.Fatalf("ServeTurn() error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 3 || events[1].Event != agentruntime.EventContent || events[1].Text != "artifact text" || events[2].Event != agentruntime.EventDone {
				t.Fatalf("events = %#v", events)
			}
		})
	}
}

func TestHTTPBackendForceCancellationIsExactlyOnce(t *testing.T) {
	bound := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	cancelCalls := 0
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
		method, _ := request["method"].(string)
		id, _ := request["id"].(string)
		if method == "CancelTask" {
			mu.Lock()
			cancelCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"id\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_CANCELED\"}}}", id)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"task\":{\"id\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_WORKING\",\"message\":{\"messageId\":\"m1\",\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"working\"}]}}}}}\n\n", id)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	runs := agentruntime.NewRunRegistry()
	backend := NewHTTPBackend(manager, RuntimeControls{Runs: runs})
	runID, err := agentruntime.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	turnDone := make(chan error, 1)
	go func() {
		turnDone <- backend.ServeTurn(context.Background(), agent, agentruntime.TurnRequest{RunID: runID, Input: "start", SessionID: "session-1"}, func(event agentruntime.TurnEvent) error {
			if event.Event == agentruntime.EventContent {
				once.Do(func() { close(bound) })
			}
			return nil
		})
	}()
	<-bound
	result, err := backend.CancelRun(context.Background(), agent, agentruntime.CancelRequest{RunID: runID, Mode: agentruntime.CancelModeForce})
	if err != nil || result.State != agentruntime.RunStateCancelled {
		t.Fatalf("CancelRun() = %#v, %v", result, err)
	}
	if _, err := backend.CancelRun(context.Background(), agent, agentruntime.CancelRequest{RunID: runID, Mode: agentruntime.CancelModeForce}); err != nil {
		t.Fatalf("repeated CancelRun() error = %v", err)
	}
	<-turnDone
	mu.Lock()
	defer mu.Unlock()
	if cancelCalls != 1 {
		t.Fatalf("CancelTask calls = %d, want 1", cancelCalls)
	}
}

func TestHTTPBackendPreBindCancellationArmsRun(t *testing.T) {
	runStarted := make(chan struct{})
	releaseTask := make(chan struct{})
	var startedOnce sync.Once
	var mu sync.Mutex
	cancelCalls := 0
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
		method, _ := request["method"].(string)
		id, _ := request["id"].(string)
		if method == "CancelTask" {
			mu.Lock()
			cancelCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"id\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_CANCELED\"}}}", id)
			return
		}
		startedOnce.Do(func() { close(runStarted) })
		<-releaseTask
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"task\":{\"id\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_WORKING\",\"message\":{\"messageId\":\"m1\",\"role\":\"ROLE_AGENT\",\"parts\":[{\"text\":\"working\"}]}}}}}\n\n", id)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	runs := agentruntime.NewRunRegistry()
	backend := NewHTTPBackend(manager, RuntimeControls{Runs: runs})
	runID, _ := agentruntime.NewRunID()
	turnDone := make(chan error, 1)
	go func() {
		turnDone <- backend.ServeTurn(context.Background(), agent, agentruntime.TurnRequest{RunID: runID, Input: "start", SessionID: "session-1"}, func(agentruntime.TurnEvent) error { return nil })
	}()
	<-runStarted
	_, err := backend.CancelRun(context.Background(), agent, agentruntime.CancelRequest{RunID: runID, Mode: agentruntime.CancelModeForce})
	if !errors.Is(err, agentruntime.ErrBackendUnavailable) {
		t.Fatalf("pre-bind CancelRun() error = %v", err)
	}
	close(releaseTask)
	<-turnDone
	mu.Lock()
	defer mu.Unlock()
	if cancelCalls != 1 {
		t.Fatalf("CancelTask calls = %d, want 1", cancelCalls)
	}
}

func TestHTTPBackendDisconnectBeforeTaskRunsBoundedCleanup(t *testing.T) {
	runStarted := make(chan struct{})
	releaseTask := make(chan struct{})
	var startedOnce sync.Once
	var mu sync.Mutex
	cancelCalls := 0
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
		method, _ := request["method"].(string)
		id, _ := request["id"].(string)
		if method == "CancelTask" {
			mu.Lock()
			cancelCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"id\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_CANCELED\"}}}", id)
			return
		}
		startedOnce.Do(func() { close(runStarted) })
		<-releaseTask
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%q,\"result\":{\"task\":{\"id\":\"task-1\",\"contextId\":\"ctx-1\",\"status\":{\"state\":\"TASK_STATE_WORKING\"}}}}\n\n", id)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	manager := NewHTTPRuntimeManager(nil, nil, server.Client(), nil)
	agent := testHTTPAgent("remote", server.URL+"/card", "")
	manager.RefreshRuntimeConfigs(context.Background(), []agentpkg.Agent{agent})
	backend := NewHTTPBackend(manager)
	ctx, disconnect := context.WithCancel(context.Background())
	turnDone := make(chan error, 1)
	go func() {
		turnDone <- backend.ServeTurn(ctx, agent, agentruntime.TurnRequest{Input: "start", SessionID: "session-1"}, func(agentruntime.TurnEvent) error { return nil })
	}()
	<-runStarted
	disconnect()
	close(releaseTask)
	select {
	case <-turnDone:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnected turn did not finish after task binding")
	}
	mu.Lock()
	defer mu.Unlock()
	if cancelCalls != 1 {
		t.Fatalf("CancelTask calls = %d, want 1", cancelCalls)
	}
}

func writeTestAgentCard(w http.ResponseWriter, endpoint string, streaming bool) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "{\"name\":\"Remote\",\"description\":\"\",\"version\":\"1\",\"capabilities\":{\"streaming\":%t},"+
		"\"defaultInputModes\":[\"text/plain\"],\"defaultOutputModes\":[\"text/plain\"],\"skills\":[],"+
		"\"supportedInterfaces\":[{\"url\":%q,\"protocolBinding\":\"JSONRPC\",\"protocolVersion\":\"1.0\"}]}", streaming, endpoint)
}
