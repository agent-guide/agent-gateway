package extensionconsumer

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/extension/contracttest"
	"strings"
	"testing"
	"time"
)

func TestPublicAttributionAndEventContracts(t *testing.T) {
	t.Setenv("AGW_FIXTURE_EVENT_FILE", "")
	p, err := attribution.NewPolicy([]string{"example.fixture.entries"}, []string{"example.fixture.entries/region"})
	if err != nil {
		t.Fatal(err)
	}
	contracttest.Attribution(t, p, attribution.Value{Subject: "subject", Tenant: "tenant", RouteID: "route", AgentID: "agent", RunID: "run", VirtualKeyID: "key", TraceID: strings.Repeat("1", 32), SpanID: strings.Repeat("2", 16), ParentSpanID: strings.Repeat("3", 16), Attributes: map[string]string{"example.fixture.entries/region": "east"}})
	contracttest.EventSink(t, newFixtureSink)
}

type nonconformingSink struct{ release chan struct{} }

func (s *nonconformingSink) Write(context.Context, event.Envelope) error { <-s.release; return nil }
func (s *nonconformingSink) Flush(context.Context) error                 { return nil }
func (s *nonconformingSink) Close(context.Context) error                 { return nil }
func TestStalledSinkFailsConformance(t *testing.T) {
	s := &nonconformingSink{release: make(chan struct{})}
	defer close(s.release)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if contracttest.CheckSinkCancellation(ctx, s) == nil {
		t.Fatal("stalled sink passed conformance")
	}
}
