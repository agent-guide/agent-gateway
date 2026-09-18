package dispatcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	configschema "github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	configstoresqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/agentroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"go.uber.org/zap"
)

func TestDispatchA2APathAEndToEnd(t *testing.T) {
	ctx := t.Context()
	var mu sync.Mutex
	var forwarded [][]byte
	var upstreamHeaders []http.Header
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/card" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"name":"Remote","description":"remote","version":"1","capabilities":{"streaming":true,"pushNotifications":true,"extendedAgentCard":true},"defaultInputModes":["text/plain"],"defaultOutputModes":["text/plain"],"skills":[{"id":"verify","name":"Verify","description":"verify"}],"supportedInterfaces":[{"url":%q,"protocolBinding":"JSONRPC","protocolVersion":"1.0","tenant":"tenant-a"}]}`, upstream.URL+"/rpc")
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		forwarded = append(forwarded, append([]byte(nil), body...))
		upstreamHeaders = append(upstreamHeaders, r.Header.Clone())
		mu.Unlock()
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &envelope)
		if envelope.Method == "SendStreamingMessage" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"message\":{}}}\n\n", envelope.ID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-A2A-Result", "kept")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"ok":true}}`, envelope.ID)
	}))
	defer upstream.Close()

	backend, err := configstore.OpenBackend(ctx, "sqlite", configstoresqlite.Config{SQLitePath: t.TempDir() + "/config.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := configschema.RegisterDefaultStores(backend); err != nil {
		t.Fatal(err)
	}
	gw := gateway.NewAgentGateway()
	if err := gw.Bootstrap(ctx, gateway.BootstrapOptions{ConfigStoreBackend: backend}); err != nil {
		t.Fatal(err)
	}
	if err := gw.AgentManager().Create(ctx, agent.Agent{
		ID: "remote", Name: "Gateway Remote", Description: "gateway description",
		Runtime: agent.Runtime{Type: agent.RuntimeTypeHTTP, HTTP: &agent.HTTPRuntime{CardURL: upstream.URL + "/card", Protocol: "a2a"}},
	}); err != nil {
		t.Fatal(err)
	}
	route := agentroute.AgentRouteConfig{AgentRouteBaseConfig: agentroute.AgentRouteBaseConfig{
		Protocol:    agentroute.RouteProtocolA2A,
		MatchPolicy: agentroute.RouteMatch{Host: "gateway.example", PathPrefix: "/agents/remote", Methods: []string{"GET", "POST"}},
		AuthPolicy:  agentroute.RouteAuthPolicy{RequireVirtualKey: true},
	}, AgentID: "remote"}
	route.Normalize()
	stored, err := route.ToConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.AgentRouteResolver().CreateConfig(ctx, stored, "test"); err != nil {
		t.Fatal(err)
	}
	if err := gw.VirtualKeyManager().Create(ctx, virtualkey.VirtualKey{ID: "vk", Key: "vk-secret", AllowedRouteIDs: []string{route.ID}}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(gw, nil, zap.NewNop(), HandlerOptions{EnableAgent: true})

	// Public discovery bypasses the route's VirtualKey and rewrites ownership.
	cardReq := httptest.NewRequest(http.MethodGet, "http://gateway.example/agents/remote/.well-known/agent-card.json", nil)
	cardRec := httptest.NewRecorder()
	if err := handler.Dispatch(cardRec, cardReq, nil); err != nil {
		t.Fatal(err)
	}
	if cardRec.Code != http.StatusOK {
		t.Fatalf("Card status/body = %d/%s", cardRec.Code, cardRec.Body.String())
	}
	var card map[string]any
	if err := json.Unmarshal(cardRec.Body.Bytes(), &card); err != nil {
		t.Fatal(err)
	}
	interfaces := card["supportedInterfaces"].([]any)
	if interfaces[0].(map[string]any)["url"] != "http://gateway.example/agents/remote" || card["name"] != "Gateway Remote" || card["signatures"] != nil {
		t.Fatalf("rewritten Card = %#v", card)
	}
	capabilities := card["capabilities"].(map[string]any)
	if capabilities["pushNotifications"] != nil || capabilities["extendedAgentCard"] != nil {
		t.Fatalf("unsafe capabilities = %#v", capabilities)
	}
	if card["securitySchemes"] == nil || card["securityRequirements"] == nil {
		t.Fatalf("VirtualKey security missing: %#v", card)
	}

	body := []byte(`{ "jsonrpc":"2.0", "id":7, "method":"SendMessage", "params":{"tenant":"tenant-a"} }`)
	post := func(payload []byte, version, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://gateway.example/agents/remote?A2A-Version="+version, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rec := httptest.NewRecorder()
		if err := handler.Dispatch(rec, req, nil); err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		return rec
	}
	if rec := post(body, "1.0", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST = %d/%s", rec.Code, rec.Body.String())
	}
	rec := post(body, "1.0", "vk-secret")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) || rec.Header().Get("X-A2A-Result") != "kept" {
		t.Fatalf("POST status/body/headers = %d/%s/%#v", rec.Code, rec.Body.String(), rec.Header())
	}
	mu.Lock()
	if len(forwarded) != 1 || !bytes.Equal(forwarded[0], body) {
		t.Fatalf("forwarded bodies = %q", forwarded)
	}
	if upstreamHeaders[0].Get("Authorization") != "" || upstreamHeaders[0].Get("A2A-Version") != "1.0" || !strings.HasPrefix(upstreamHeaders[0].Get("traceparent"), "00-") {
		t.Fatalf("upstream headers = %#v", upstreamHeaders[0])
	}
	mu.Unlock()

	badVersion := post(body, "0.3", "vk-secret")
	if badVersion.Code != http.StatusOK || jsonRPCErrorCode(t, badVersion.Body.Bytes()) != -32009 {
		t.Fatalf("bad version = %d/%s", badVersion.Code, badVersion.Body.String())
	}
	push := []byte(`{"jsonrpc":"2.0","id":8,"method":"SendMessage","params":{"tenant":"tenant-a","configuration":{"taskPushNotificationConfig":{"url":"https://callback"}}}}`)
	if got := jsonRPCErrorCode(t, post(push, "1.0", "vk-secret").Body.Bytes()); got != -32602 {
		t.Fatalf("embedded push error code = %d", got)
	}
	mu.Lock()
	if len(forwarded) != 1 {
		t.Fatalf("rejected requests were forwarded: %d", len(forwarded))
	}
	mu.Unlock()

	streamBody := []byte(`{"jsonrpc":"2.0","id":9,"method":"SendStreamingMessage","params":{"tenant":"tenant-a"}}`)
	stream := post(streamBody, "1.0", "vk-secret")
	if stream.Code != http.StatusOK || stream.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(stream.Body.String(), `"id":9`) {
		t.Fatalf("stream = %d/%#v/%s", stream.Code, stream.Header(), stream.Body.String())
	}

	wrongMethod := httptest.NewRequest(http.MethodDelete, "http://gateway.example/agents/remote", nil)
	wrongRec := httptest.NewRecorder()
	if err := handler.Dispatch(wrongRec, wrongMethod, nil); err != nil {
		t.Fatal(err)
	}
	if wrongRec.Code != http.StatusMethodNotAllowed || wrongRec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("wrong method = %d/%#v", wrongRec.Code, wrongRec.Header())
	}
}

func jsonRPCErrorCode(t *testing.T, body []byte) int {
	t.Helper()
	var envelope struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode JSON-RPC error %q: %v", body, err)
	}
	return envelope.Error.Code
}
