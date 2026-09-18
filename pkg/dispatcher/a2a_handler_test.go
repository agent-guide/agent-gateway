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

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
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
		if string(envelope.ID) == "12" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if string(envelope.ID) == "13" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":99,"result":{}}`)
			return
		}
		if string(envelope.ID) == "15" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":99,\"result\":{}}\n\n")
			return
		}
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
	sink := &builtinCaptureSink{}
	gw := gateway.NewAgentGateway()
	if err := gw.Bootstrap(ctx, gateway.BootstrapOptions{
		ConfigStoreBackend: backend,
		UsageObserver:      usage.NewObserver(sink),
	}); err != nil {
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
		req.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-00")
		req.Header.Set("tracestate", "vendor=value")
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
	if upstreamHeaders[0].Get("Authorization") != "" || upstreamHeaders[0].Get("A2A-Version") != "1.0" || !strings.HasSuffix(upstreamHeaders[0].Get("traceparent"), "-00") || upstreamHeaders[0].Get("tracestate") != "vendor=value" {
		t.Fatalf("upstream headers = %#v", upstreamHeaders[0])
	}
	mu.Unlock()

	badVersion := post(body, "0.3", "vk-secret")
	if badVersion.Code != http.StatusOK || jsonRPCErrorCode(t, badVersion.Body.Bytes()) != -32009 {
		t.Fatalf("bad version = %d/%s", badVersion.Code, badVersion.Body.String())
	}
	badVersionEvents := eventsOfType[usage.InteractionEvent](sink.events)
	badVersionEvent := badVersionEvents[len(badVersionEvents)-1]
	if badVersionEvent.Success || badVersionEvent.StatusCode != http.StatusOK || badVersionEvent.ErrorType != "a2a_version_not_supported" {
		t.Fatalf("bad version interaction = %+v", badVersionEvent)
	}
	push := []byte(`{"jsonrpc":"2.0","id":8,"method":"SendMessage","params":{"tenant":"tenant-a","configuration":{"taskPushNotificationConfig":{"url":"https://callback"}}}}`)
	if got := jsonRPCErrorCode(t, post(push, "1.0", "vk-secret").Body.Bytes()); got != -32602 {
		t.Fatalf("embedded push error code = %d", got)
	}
	malformedStreamParams := []byte(`{"jsonrpc":"2.0","id":16,"method":"SendStreamingMessage","params":{"tenant":5}}`)
	malformedStreamRec := post(malformedStreamParams, "1.0", "vk-secret")
	if got := malformedStreamRec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("malformed streaming params Content-Type = %q", got)
	}
	malformedStreamPayload := bytes.TrimSpace(bytes.TrimPrefix(malformedStreamRec.Body.Bytes(), []byte("data:")))
	if got := jsonRPCErrorCode(t, malformedStreamPayload); got != -32602 {
		t.Fatalf("malformed streaming params error code = %d", got)
	}
	var malformedStreamEnvelope struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(malformedStreamPayload, &malformedStreamEnvelope); err != nil || string(malformedStreamEnvelope.ID) != "16" {
		t.Fatalf("malformed streaming params id = %s, error = %v", malformedStreamEnvelope.ID, err)
	}
	malformedVersionRec := post(malformedStreamParams, "0.3", "vk-secret")
	malformedVersionPayload := bytes.TrimSpace(bytes.TrimPrefix(malformedVersionRec.Body.Bytes(), []byte("data:")))
	if got := jsonRPCErrorCode(t, malformedVersionPayload); got != -32009 {
		t.Fatalf("version must precede malformed params, got code %d", got)
	}
	if got := jsonRPCErrorCode(t, post([]byte(`{`), "1.0", "vk-secret").Body.Bytes()); got != -32700 {
		t.Fatalf("parse error code = %d", got)
	}
	if got := jsonRPCErrorCode(t, post([]byte(`[]`), "1.0", "vk-secret").Body.Bytes()); got != -32600 {
		t.Fatalf("batch error code = %d", got)
	}
	unknown := []byte(`{"jsonrpc":"2.0","id":10,"method":"GetExtendedAgentCard","params":{"tenant":"tenant-a"}}`)
	if got := jsonRPCErrorCode(t, post(unknown, "1.0", "vk-secret").Body.Bytes()); got != -32601 {
		t.Fatalf("denied method error code = %d", got)
	}
	wrongTenant := []byte(`{"jsonrpc":"2.0","id":11,"method":"GetTask","params":{"tenant":"other"}}`)
	if got := jsonRPCErrorCode(t, post(wrongTenant, "1.0", "vk-secret").Body.Bytes()); got != -32602 {
		t.Fatalf("tenant error code = %d", got)
	}
	subscribeWrongTenant := []byte(`{"jsonrpc":"2.0","id":14,"method":"SubscribeToTask","params":{"tenant":"other"}}`)
	subscribeTenantRec := post(subscribeWrongTenant, "1.0", "vk-secret")
	if got := subscribeTenantRec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("SubscribeToTask tenant rejection Content-Type = %q", got)
	}
	if got := jsonRPCErrorCode(t, subscribeTenantRec.Body.Bytes()); got != -32602 {
		t.Fatalf("SubscribeToTask tenant error code = %d", got)
	}
	subscribeVersionRec := post(subscribeWrongTenant, "", "vk-secret")
	if got := subscribeVersionRec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("SubscribeToTask version rejection Content-Type = %q", got)
	}
	if got := jsonRPCErrorCode(t, subscribeVersionRec.Body.Bytes()); got != -32009 {
		t.Fatalf("SubscribeToTask version error code = %d", got)
	}
	notification := []byte(`{"jsonrpc":"2.0","method":"SendMessage","params":{"tenant":"tenant-a"}}`)
	if notificationRec := post(notification, "1.0", "vk-secret"); notificationRec.Code != http.StatusNoContent || notificationRec.Body.Len() != 0 {
		t.Fatalf("notification = %d/%q", notificationRec.Code, notificationRec.Body.String())
	}
	malformedNotification := []byte(`{"jsonrpc":"2.0","method":"SendMessage","params":{"tenant":5}}`)
	if malformedNotificationRec := post(malformedNotification, "1.0", "vk-secret"); malformedNotificationRec.Code != http.StatusNoContent || malformedNotificationRec.Body.Len() != 0 {
		t.Fatalf("malformed notification = %d/%q", malformedNotificationRec.Code, malformedNotificationRec.Body.String())
	}
	events := eventsOfType[usage.InteractionEvent](sink.events)
	if len(events) == 0 {
		t.Fatal("notification rejection did not emit an interaction event")
	}
	notificationEvent := events[len(events)-1]
	if notificationEvent.Success || notificationEvent.StatusCode != http.StatusNoContent || notificationEvent.ErrorType != "a2a_notification_rejected" {
		t.Fatalf("notification interaction = %+v", notificationEvent)
	}
	upstreamUnavailable := []byte(`{"jsonrpc":"2.0","id":12,"method":"GetTask","params":{"tenant":"tenant-a"}}`)
	if got := jsonRPCErrorCode(t, post(upstreamUnavailable, "1.0", "vk-secret").Body.Bytes()); got != -32000 {
		t.Fatalf("upstream status error code = %d", got)
	}
	invalidUpstream := []byte(`{"jsonrpc":"2.0","id":13,"method":"GetTask","params":{"tenant":"tenant-a"}}`)
	if got := jsonRPCErrorCode(t, post(invalidUpstream, "1.0", "vk-secret").Body.Bytes()); got != -32006 {
		t.Fatalf("invalid upstream error code = %d", got)
	}
	mu.Lock()
	if len(forwarded) != 3 {
		t.Fatalf("unexpected forwarded request count: %d", len(forwarded))
	}
	mu.Unlock()

	streamBody := []byte(`{"jsonrpc":"2.0","id":9,"method":"SendStreamingMessage","params":{"tenant":"tenant-a"}}`)
	stream := post(streamBody, "1.0", "vk-secret")
	if stream.Code != http.StatusOK || stream.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(stream.Body.String(), `"id":9`) {
		t.Fatalf("stream = %d/%#v/%s", stream.Code, stream.Header(), stream.Body.String())
	}
	invalidStreamBody := []byte(`{"jsonrpc":"2.0","id":15,"method":"SendStreamingMessage","params":{"tenant":"tenant-a"}}`)
	invalidStream := post(invalidStreamBody, "1.0", "vk-secret")
	if invalidStream.Code != http.StatusOK || invalidStream.Header().Get("Content-Type") != "text/event-stream" || invalidStream.Header().Get("Content-Length") != "" {
		t.Fatalf("invalid stream = %d/%#v/%s", invalidStream.Code, invalidStream.Header(), invalidStream.Body.String())
	}
	invalidStreamPayload := bytes.TrimSpace(bytes.TrimPrefix(invalidStream.Body.Bytes(), []byte("data:")))
	if got := jsonRPCErrorCode(t, invalidStreamPayload); got != -32006 {
		t.Fatalf("invalid stream error code = %d", got)
	}
	invalidStreamEvents := eventsOfType[usage.InteractionEvent](sink.events)
	invalidStreamEvent := invalidStreamEvents[len(invalidStreamEvents)-1]
	if invalidStreamEvent.Success || invalidStreamEvent.ErrorType != "a2a_invalid_agent_response" {
		t.Fatalf("invalid stream interaction = %+v", invalidStreamEvent)
	}

	wrongMethod := httptest.NewRequest(http.MethodDelete, "http://gateway.example/agents/remote", nil)
	wrongRec := httptest.NewRecorder()
	if err := handler.Dispatch(wrongRec, wrongMethod, nil); err != nil {
		t.Fatal(err)
	}
	if wrongRec.Code != http.StatusMethodNotAllowed || wrongRec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("wrong method = %d/%#v", wrongRec.Code, wrongRec.Header())
	}
	wrongPath := httptest.NewRequest(http.MethodGet, "http://gateway.example/agents/remote/other", nil)
	wrongPathRec := httptest.NewRecorder()
	if err := handler.Dispatch(wrongPathRec, wrongPath, nil); err != nil {
		t.Fatal(err)
	}
	if wrongPathRec.Code != http.StatusNotFound {
		t.Fatalf("wrong path = %d/%s", wrongPathRec.Code, wrongPathRec.Body.String())
	}
	wrongMIME := httptest.NewRequest(http.MethodPost, "http://gateway.example/agents/remote?A2A-Version=1.0", strings.NewReader("{}"))
	wrongMIME.Header.Set("Content-Type", "text/plain")
	wrongMIME.Header.Set("Authorization", "Bearer vk-secret")
	wrongMIMERec := httptest.NewRecorder()
	if err := handler.Dispatch(wrongMIMERec, wrongMIME, nil); err != nil {
		t.Fatal(err)
	}
	if wrongMIMERec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong MIME = %d/%s", wrongMIMERec.Code, wrongMIMERec.Body.String())
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
