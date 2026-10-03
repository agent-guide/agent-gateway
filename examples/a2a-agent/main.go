// Command a2a-agent runs the development-only A2A Protocol 1.0 service used
// by the HTTP Agent quick start and release smoke tests.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	cardPath  = "/.well-known/agent-card.json"
	rpcPath   = "/a2a"
	contextID = "example-context"
	taskID    = "example-task"
)

type requestEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
}

type exampleHandler struct{}

func (exampleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	case r.Method == http.MethodGet && r.URL.Path == cardPath:
		serveCard(w, r)
	case r.Method == http.MethodPost && r.URL.Path == rpcPath:
		serveRPC(w, r)
	default:
		http.NotFound(w, r)
	}
}

func serveCard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":               "Agent Gateway A2A Example",
		"description":        "Development-only deterministic A2A service",
		"version":            "1.0.0",
		"capabilities":       map[string]any{"streaming": true},
		"defaultInputModes":  []string{"text/plain"},
		"defaultOutputModes": []string{"text/plain"},
		"skills": []map[string]any{{
			"id": "echo", "name": "Echo", "description": "Returns a deterministic response",
		}},
		"supportedInterfaces": []map[string]any{{
			"url": "http://" + r.Host + rpcPath, "protocolBinding": "JSONRPC", "protocolVersion": "1.0",
		}},
	})
}

func serveRPC(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("A2A-Version") != "1.0" {
		http.Error(w, "A2A-Version 1.0 is required", http.StatusBadRequest)
		return
	}
	// This fixture is anonymous. Rejecting ingress credential carriers makes
	// the smoke test prove that the gateway terminates VirtualKeys.
	if r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("Cookie") != "" {
		http.Error(w, "unexpected credential header", http.StatusBadRequest)
		return
	}
	var request requestEnvelope
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := decoder.Decode(&request); err != nil {
		writeJSONError(w, nil, -32700, "Parse error")
		return
	}
	if request.JSONRPC != "2.0" || len(request.ID) == 0 || strings.TrimSpace(request.Method) == "" {
		writeJSONError(w, request.ID, -32600, "Invalid Request")
		return
	}
	switch request.Method {
	case "SendMessage":
		w.Header().Set("Content-Type", "application/json")
		writeEnvelope(w, request.ID, map[string]any{"message": agentMessage("example-message")})
	case "SendStreamingMessage":
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		writeSSE(w, request.ID, map[string]any{"task": map[string]any{
			"id": taskID, "contextId": contextID,
			"status": map[string]any{
				"state": "TASK_STATE_WORKING", "message": agentMessage("example-stream-message"),
			},
		}})
		writeSSE(w, request.ID, map[string]any{"statusUpdate": map[string]any{
			"taskId": taskID, "contextId": contextID,
			"status": map[string]any{"state": "TASK_STATE_COMPLETED"},
			"final":  true,
		}})
	default:
		writeJSONError(w, request.ID, -32601, "Method not found")
	}
}

func agentMessage(id string) map[string]any {
	return map[string]any{
		"messageId": id, "role": "ROLE_AGENT", "parts": []map[string]any{{"text": "hello from the local A2A example"}},
		"contextId": contextID,
	}
}

func writeEnvelope(w http.ResponseWriter, id json.RawMessage, result any) {
	_, _ = w.Write(envelopeBytes(id, result))
}

func writeSSE(w http.ResponseWriter, id json.RawMessage, result any) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", envelopeBytes(id, result))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func envelopeBytes(id json.RawMessage, result any) []byte {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%s,"result":%s}`, id, resultJSON)
}

func writeJSONError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":%q}}`, id, code, message)
}

func main() {
	listenAddress := flag.String("listen", "127.0.0.1:8090", "loopback listen address")
	addressFile := flag.String("write-address", "", "write the selected base URL after listening")
	flag.Parse()

	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		log.Fatal(err)
	}
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		log.Fatal(err)
	}
	baseURL := "http://" + net.JoinHostPort(host, port)
	if *addressFile != "" {
		if err := os.WriteFile(*addressFile, []byte(baseURL+"\n"), 0o600); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("A2A example listening at %s", baseURL)

	server := &http.Server{Handler: exampleHandler{}, ReadHeaderTimeout: 5 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("shutdown: %v", err)
		}
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
