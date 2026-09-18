package pipeline

import (
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
)

func TestSQLiteSinkPersistsA2AInteraction(t *testing.T) {
	backend, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: t.TempDir() + "/usage.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sink, query, err := NewSQLiteSink(backend, usage.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	now := time.Now().UTC()
	ev := usage.InteractionEvent{
		EventID: "a2a-1", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
		StartedAt: now, FinishedAt: now.Add(10 * time.Millisecond), RouteID: "remote-agent",
		RouteKind: "agent", RouteProtocol: "a2a", AgentID: "agent-1", RuntimeType: "http",
		Success: true, StatusCode: 200, LatencyMS: 10,
	}
	if err := sink.Write(ev); err != nil {
		t.Fatal(err)
	}

	summary, err := query.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if summary.A2A.RequestCount != 1 || summary.A2A.SuccessCount != 1 || summary.A2A.FailureCount != 0 {
		t.Fatalf("A2A summary = %+v", summary.A2A)
	}
	events, err := query.ListEvents("a2a", usage.EventListOptions{Filters: map[string]string{"agent_id": "agent-1"}})
	if err != nil || len(events.Items) != 1 || events.Items[0]["event_id"] != "a2a-1" {
		t.Fatalf("A2A events = %#v, error = %v", events.Items, err)
	}
	interactions, err := query.ListInteractions(usage.EventListOptions{Filters: map[string]string{"runtime_type": "http"}})
	if err != nil || len(interactions.Items) != 1 || interactions.Items[0]["event_id"] != "a2a-1" {
		t.Fatalf("A2A interactions = %#v, error = %v", interactions.Items, err)
	}
	breakdown, err := query.InteractionsSummary(usage.BreakdownOptions{GroupBy: "route_protocol", Limit: 10})
	if err != nil || len(breakdown.Items) != 1 || breakdown.Items[0]["group_value"] != "a2a" {
		t.Fatalf("A2A interaction summary = %#v, error = %v", breakdown.Items, err)
	}
}
