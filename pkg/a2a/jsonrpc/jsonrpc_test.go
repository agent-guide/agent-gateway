package jsonrpc

import (
	"encoding/json"
	"testing"
)

func TestValidateResponse(t *testing.T) {
	id := json.RawMessage("\"request-1\"")
	if err := ValidateResponse([]byte("{\"jsonrpc\":\"2.0\",\"id\":\"request-1\",\"result\":{\"ok\":true}}"), id); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
	for _, body := range []string{
		"[]",
		"{\"jsonrpc\":\"2.0\",\"id\":\"other\",\"result\":{}}",
		"{\"jsonrpc\":\"2.0\",\"id\":\"request-1\"}",
		"{\"jsonrpc\":\"2.0\",\"id\":\"request-1\",\"result\":{},\"error\":{}}",
		"{\"jsonrpc\":\"2.0\",\"id\":\"request-1\",\"result\":{}} {}",
	} {
		if err := ValidateResponse([]byte(body), id); err == nil {
			t.Fatalf("invalid response accepted: %s", body)
		}
	}
}

func TestParseRequestMetaRequiresObjectAndID(t *testing.T) {
	meta, err := ParseRequestMeta([]byte("{\"jsonrpc\":\"2.0\",\"method\":\"SendMessage\",\"id\":7}"))
	if err != nil || meta.Method != MethodSendMessage {
		t.Fatalf("ParseRequestMeta() = %#v, %v", meta, err)
	}
	if _, err := ParseRequestMeta([]byte("{\"jsonrpc\":\"2.0\",\"method\":\"SendMessage\"}")); err == nil {
		t.Fatal("notification accepted")
	}
}
