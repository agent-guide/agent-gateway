// Package jsonrpc defines the A2A v1.0 JSON-RPC wire constants and validation
// shared by the gateway-owned HTTP guards.
package jsonrpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// InvalidSSEError identifies an SSE framing, size, or payload validation
// failure separately from source reads and destination writes.
type InvalidSSEError struct{ Err error }

func (e *InvalidSSEError) Error() string { return "invalid A2A SSE response: " + e.Err.Error() }
func (e *InvalidSSEError) Unwrap() error { return e.Err }

// IsInvalidSSE reports whether err represents invalid upstream SSE content.
func IsInvalidSSE(err error) bool {
	var target *InvalidSSEError
	return errors.As(err, &target)
}

// InvalidParamsError identifies a request whose JSON-RPC envelope is usable
// but whose A2A params cannot be inspected safely. Callers can still use the
// returned request metadata to preserve the id, method-specific response
// channel, notification semantics, and validation order.
type InvalidParamsError struct{ Err error }

func (e *InvalidParamsError) Error() string { return "invalid A2A params: " + e.Err.Error() }
func (e *InvalidParamsError) Unwrap() error { return e.Err }

func IsInvalidParams(err error) bool {
	var target *InvalidParamsError
	return errors.As(err, &target)
}

const (
	Version       = "2.0"
	A2AVersion    = "1.0"
	HeaderVersion = "A2A-Version"
	// Method constants are local because the SDK's constants are internal. Keep
	// these literals identical to the A2A v1.0 SDK wire method strings.
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

// InspectedRequest is the bounded, read-only view Path A needs before it
// forwards the original request bytes unchanged.
type InspectedRequest struct {
	RequestMeta
	Notification       bool
	Tenant             string
	TenantPresent      bool
	EmbeddedPushConfig bool
}

// InspectRequest validates one non-batch JSON-RPC request and extracts only
// the policy fields needed by the governed proxy. It deliberately does not
// return a typed A2A request: accepted bytes must not be re-marshaled.
func InspectRequest(body []byte) (InspectedRequest, error) {
	var raw struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      json.RawMessage `json:"id"`
		Params  json.RawMessage `json:"params"`
	}
	if err := decodeOneObject(body, &raw); err != nil {
		return InspectedRequest{}, err
	}
	if raw.JSONRPC != Version || strings.TrimSpace(raw.Method) == "" {
		return InspectedRequest{}, fmt.Errorf("invalid JSON-RPC request envelope")
	}
	inspected := InspectedRequest{RequestMeta: RequestMeta{JSONRPC: raw.JSONRPC, Method: raw.Method, ID: raw.ID}}
	inspected.Notification = len(bytes.TrimSpace(raw.ID)) == 0
	if len(bytes.TrimSpace(raw.Params)) == 0 || bytes.Equal(bytes.TrimSpace(raw.Params), []byte("null")) {
		return inspected, nil
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw.Params, &params); err != nil {
		return inspected, &InvalidParamsError{Err: fmt.Errorf("params must be an object")}
	}
	if tenant, ok := params["tenant"]; ok {
		inspected.TenantPresent = true
		if err := json.Unmarshal(tenant, &inspected.Tenant); err != nil {
			return inspected, &InvalidParamsError{Err: fmt.Errorf("tenant must be a string")}
		}
	}
	if raw.Method == MethodSendMessage || raw.Method == MethodSendStreamingMessage {
		if configuration, ok := params["configuration"]; ok && !bytes.Equal(bytes.TrimSpace(configuration), []byte("null")) {
			var cfg map[string]json.RawMessage
			if err := json.Unmarshal(configuration, &cfg); err != nil {
				return inspected, &InvalidParamsError{Err: fmt.Errorf("configuration must be an object")}
			}
			if push, ok := cfg["taskPushNotificationConfig"]; ok && !bytes.Equal(bytes.TrimSpace(push), []byte("null")) {
				inspected.EmbeddedPushConfig = true
			}
		}
	}
	return inspected, nil
}

func AllowedMethod(method string) bool {
	switch method {
	case MethodSendMessage, MethodSendStreamingMessage, MethodGetTask, MethodListTasks, MethodCancelTask, MethodSubscribeToTask:
		return true
	default:
		return false
	}
}

// ValidateTenant requires the request tenant to exactly match the selected
// interface tenant, including presence versus omission.
func ValidateTenant(req InspectedRequest, selected string) error {
	if selected == "" {
		if req.TenantPresent {
			return fmt.Errorf("tenant must be omitted")
		}
		return nil
	}
	if !req.TenantPresent || req.Tenant != selected {
		return fmt.Errorf("tenant does not match the selected interface")
	}
	return nil
}

// ValidateServiceVersion accepts A2A 1.0 from the header or any
// case-insensitive query-key spelling, rejecting duplicates and conflicts.
func ValidateServiceVersion(header http.Header, query url.Values) error {
	var values []string
	for _, value := range header.Values(HeaderVersion) {
		values = append(values, value)
	}
	for key, keyValues := range query {
		if strings.EqualFold(key, HeaderVersion) {
			values = append(values, keyValues...)
		}
	}
	if len(values) == 0 {
		return fmt.Errorf("A2A version is required")
	}
	for _, value := range values {
		if value != A2AVersion {
			return fmt.Errorf("unsupported or conflicting A2A version")
		}
	}
	return nil
}

// RemoveVersionQuery removes every case variant of A2A-Version while
// retaining the ordering of all other raw query fields.
func RemoveVersionQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	parts := strings.Split(rawQuery, "&")
	out := parts[:0]
	for _, part := range parts {
		key := part
		if index := strings.IndexByte(key, '='); index >= 0 {
			key = key[:index]
		}
		decoded, err := url.QueryUnescape(key)
		if err == nil && strings.EqualFold(decoded, HeaderVersion) {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, "&")
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

// CopyValidatedSSE copies complete SSE records without changing their bytes,
// while bounding each event and the aggregate stream. onEvent validates every
// non-heartbeat data payload before that record is written.
func CopyValidatedSSE(dst io.Writer, src io.Reader, maxEventBytes, maxStreamBytes int64, onEvent func([]byte) error) error {
	reader := bufio.NewReaderSize(src, 32<<10)
	var event bytes.Buffer
	var total int64
	var lineBytes int64
	flush := func() error {
		if event.Len() == 0 {
			return nil
		}
		raw := append([]byte(nil), event.Bytes()...)
		event.Reset()
		data, hasFields := sseData(raw)
		if len(data) == 0 && hasFields {
			return &InvalidSSEError{Err: fmt.Errorf("A2A SSE event has no data field")}
		}
		if len(data) > 0 && onEvent != nil {
			if err := onEvent(data); err != nil {
				return &InvalidSSEError{Err: err}
			}
		}
		_, err := dst.Write(raw)
		return err
	}
	for {
		fragment, err := reader.ReadSlice('\n')
		total += int64(len(fragment))
		if total > maxStreamBytes {
			return &InvalidSSEError{Err: fmt.Errorf("A2A stream exceeds %d bytes", maxStreamBytes)}
		}
		if int64(event.Len()+len(fragment)) > maxEventBytes {
			return &InvalidSSEError{Err: fmt.Errorf("A2A SSE event exceeds %d bytes", maxEventBytes)}
		}
		event.Write(fragment)
		lineBytes += int64(len(fragment))
		if err == bufio.ErrBufferFull {
			continue
		}
		blankLine := lineBytes == 1 && bytes.Equal(fragment, []byte("\n")) ||
			lineBytes == 2 && bytes.Equal(fragment, []byte("\r\n"))
		lineBytes = 0
		if blankLine {
			if flushErr := flush(); flushErr != nil {
				return flushErr
			}
		}
		if err == io.EOF {
			return flush()
		}
		if err != nil {
			return err
		}
	}
}

func sseData(raw []byte) ([]byte, bool) {
	var data []string
	hasFields := false
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		hasFields = true
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return []byte(strings.Join(data, "\n")), hasFields
}
