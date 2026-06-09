package opencode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/acp/agentspi"
	"github.com/agent-guide/agent-gateway/pkg/acp/transport"
)

// recordingTransport captures Request calls for assertions.
type recordingTransport struct {
	method string
	params any
}

func (r *recordingTransport) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	r.method = method
	r.params = params
	return json.RawMessage(`{"configOptions":[]}`), nil
}
func (r *recordingTransport) Notify(string, any) error { return nil }
func (r *recordingTransport) Updates(int) (<-chan transport.Message, func()) {
	return make(chan transport.Message), func() {}
}
func (r *recordingTransport) Alive() bool  { return true }
func (r *recordingTransport) Close() error { return nil }

func TestAgentImplementsSessionModelSelector(t *testing.T) {
	var _ agentspi.SessionModelSelector = (*Agent)(nil)
}

func TestSelectSessionModelSetsModelConfigOption(t *testing.T) {
	a := &Agent{cwd: "/tmp"}
	tr := &recordingTransport{}
	if _, err := a.SelectSessionModel(context.Background(), tr, "sess-1", "anthropic/claude-3-5-haiku-latest", nil); err != nil {
		t.Fatalf("SelectSessionModel: %v", err)
	}
	if tr.method != "session/set_config_option" {
		t.Fatalf("method = %q, want session/set_config_option", tr.method)
	}
	m, ok := tr.params.(map[string]any)
	if !ok {
		t.Fatalf("params is %T, want map", tr.params)
	}
	if m["configId"] != "model" {
		t.Fatalf("configId = %v, want model", m["configId"])
	}
	if m["value"] != "anthropic/claude-3-5-haiku-latest" {
		t.Fatalf("value = %v, want the model id", m["value"])
	}
	if m["sessionId"] != "sess-1" {
		t.Fatalf("sessionId = %v, want sess-1", m["sessionId"])
	}
}

func TestSelectSessionModelNoopOnEmptyModel(t *testing.T) {
	a := &Agent{cwd: "/tmp"}
	tr := &recordingTransport{}
	if _, err := a.SelectSessionModel(context.Background(), tr, "sess-1", "", nil); err != nil {
		t.Fatalf("SelectSessionModel: %v", err)
	}
	if tr.method != "" {
		t.Fatalf("expected no request for an empty model, got %q", tr.method)
	}
}
