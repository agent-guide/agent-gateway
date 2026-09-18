package jsonrpc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
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

func TestInspectRequestPolicyFields(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":"r1","method":"SendMessage","params":{"tenant":"tenant-a","configuration":{"taskPushNotificationConfig":{"url":"https://callback"}}}}`)
	got, err := InspectRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if got.Notification || got.Tenant != "tenant-a" || !got.TenantPresent || !got.EmbeddedPushConfig || !AllowedMethod(got.Method) {
		t.Fatalf("inspection = %#v", got)
	}
	if err := ValidateTenant(got, "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTenant(got, "other"); err == nil {
		t.Fatal("tenant mismatch accepted")
	}
}

func TestInspectRequestInvalidParamsPreservesEnvelope(t *testing.T) {
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":7,"method":"SendStreamingMessage","params":[]}`,
		`{"jsonrpc":"2.0","id":7,"method":"SendStreamingMessage","params":{"tenant":5}}`,
		`{"jsonrpc":"2.0","id":7,"method":"SendStreamingMessage","params":{"configuration":[]}}`,
	} {
		got, err := InspectRequest([]byte(body))
		if !IsInvalidParams(err) {
			t.Fatalf("InspectRequest(%s) error = %v, want InvalidParamsError", body, err)
		}
		if got.Method != MethodSendStreamingMessage || string(got.ID) != "7" || got.Notification {
			t.Fatalf("InspectRequest(%s) metadata = %#v", body, got)
		}
	}
}

func TestServiceVersionAndQueryRemoval(t *testing.T) {
	header := http.Header{}
	query := url.Values{"a2a-version": {"1.0"}, "A2A-Extensions": {"urn:x"}}
	if err := ValidateServiceVersion(header, query); err != nil {
		t.Fatal(err)
	}
	header.Set(HeaderVersion, "0.3")
	if err := ValidateServiceVersion(header, query); err == nil {
		t.Fatal("conflicting version accepted")
	}
	if got := RemoveVersionQuery("x=1&a2a-version=1.0&A2A-Extensions=urn%3Ax"); got != "x=1&A2A-Extensions=urn%3Ax" {
		t.Fatalf("RemoveVersionQuery() = %q", got)
	}
}

func TestCopyValidatedSSEPreservesBytes(t *testing.T) {
	raw := []byte(": heartbeat\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
	var out bytes.Buffer
	if err := CopyValidatedSSE(&out, bytes.NewReader(raw), 1<<20, 1<<20, func(data []byte) error {
		return ValidateResponse(data, json.RawMessage("1"))
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), raw) {
		t.Fatalf("copied bytes = %q", out.Bytes())
	}
}

func TestCopyValidatedSSEDoesNotSplitBufferAlignedLine(t *testing.T) {
	const readerBufferSize = 32 << 10
	firstLine := "data: " + strings.Repeat(" ", readerBufferSize-len("data: "))
	raw := []byte(firstLine + "\n" + `data: {"jsonrpc":"2.0","id":1,"result":{}}` + "\n\n")
	var out bytes.Buffer
	if err := CopyValidatedSSE(&out, bytes.NewReader(raw), 1<<20, 1<<20, func(data []byte) error {
		return ValidateResponse(data, json.RawMessage("1"))
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), raw) {
		t.Fatalf("copied %d bytes, want %d", out.Len(), len(raw))
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
