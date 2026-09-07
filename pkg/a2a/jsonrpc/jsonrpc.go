// Package jsonrpc defines the A2A v1.0 JSON-RPC wire constants and validation
// shared by the gateway-owned HTTP guards.
package jsonrpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	Version       = "2.0"
	A2AVersion    = "1.0"
	HeaderVersion = "A2A-Version"
	// Method constants define Path A's planned allowlist as well as the Path B
	// wire vocabulary; not every method is used by the translating backend.
	MethodSendMessage          = "SendMessage"
	MethodSendStreamingMessage = "SendStreamingMessage"
	MethodGetTask              = "GetTask"
	MethodListTasks            = "ListTasks"
	MethodCancelTask           = "CancelTask"
	MethodSubscribeToTask      = "SubscribeToTask"
)

type RequestMeta struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	ID      json.RawMessage `json:"id"`
}

func ParseRequestMeta(body []byte) (RequestMeta, error) {
	var meta RequestMeta
	if err := decodeOneObject(body, &meta); err != nil {
		return meta, fmt.Errorf("invalid JSON-RPC request: %w", err)
	}
	if meta.JSONRPC != Version || meta.Method == "" || len(meta.ID) == 0 || bytes.Equal(meta.ID, []byte("null")) {
		return meta, fmt.Errorf("invalid JSON-RPC request envelope")
	}
	return meta, nil
}

func ValidateResponse(body []byte, requestID json.RawMessage) error {
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := decodeOneObject(body, &envelope); err != nil {
		return err
	}
	if envelope.JSONRPC != Version || !jsonEqual(envelope.ID, requestID) {
		return fmt.Errorf("JSON-RPC version or response id mismatch")
	}
	hasResult := len(envelope.Result) != 0
	hasError := len(envelope.Error) != 0
	if hasResult == hasError {
		return fmt.Errorf("JSON-RPC response must contain exactly one of result or error")
	}
	return nil
}

func decodeOneObject(body []byte, out any) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("expected one JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing input: %w", err)
	}
	return nil
}

func jsonEqual(a, b json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
}
