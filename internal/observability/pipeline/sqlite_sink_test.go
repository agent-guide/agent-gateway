package pipeline

import (
	"fmt"
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

func TestSQLiteSinkFreshDatabaseCreatesAllUsageTablesAndIndexes(t *testing.T) {
	backend, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: t.TempDir() + "/fresh.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sink, _, err := NewSQLiteSink(backend, usage.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	db := backend.UsageDB()
	for table, indexes := range map[string][]string{
		"llm_usage_events":     {"idx_llm_events_started", "idx_llm_events_route", "idx_llm_events_trace"},
		"mcp_usage_events":     {"idx_mcp_events_started", "idx_mcp_events_route", "idx_mcp_events_trace"},
		"acp_usage_events":     {"idx_acp_events_started", "idx_acp_events_route", "idx_acp_events_trace"},
		"builtin_usage_events": {"idx_builtin_events_started", "idx_builtin_events_route", "idx_builtin_events_trace"},
		"a2a_usage_events":     {"idx_a2a_events_started", "idx_a2a_events_route", "idx_a2a_events_trace", "idx_a2a_events_agent"},
	} {
		var tableCount int64
		if err := db.Raw(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&tableCount).Error; err != nil {
			t.Fatal(err)
		}
		if tableCount != 1 {
			t.Fatalf("table %s count = %d, want 1", table, tableCount)
		}
		for _, index := range indexes {
			var indexCount int64
			if err := db.Raw(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name = ?`, table, index).Scan(&indexCount).Error; err != nil {
				t.Fatal(err)
			}
			if indexCount != 1 {
				t.Fatalf("index %s on %s count = %d, want 1", index, table, indexCount)
			}
		}
	}
}

func TestSQLiteSinkUpgradesPreV06DatabaseAndPreservesUsage(t *testing.T) {
	backend, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: t.TempDir() + "/pre-v0.6.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := backend.UsageDB()
	if err := configsqlite.MigrateUsageTables(db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	legacy := usage.InteractionEvent{StartedAt: now, FinishedAt: now.Add(time.Millisecond), Success: true, StatusCode: 200}
	if err := configsqlite.InsertLLMUsageEvent(db, usage.LLMUsageEvent{InteractionEvent: withLegacyID(legacy, "legacy-llm", "llm")}); err != nil {
		t.Fatal(err)
	}
	if err := configsqlite.InsertMCPUsageEvent(db, usage.MCPUsageEvent{InteractionEvent: withLegacyID(legacy, "legacy-mcp", "mcp")}); err != nil {
		t.Fatal(err)
	}
	if err := configsqlite.InsertACPUsageEvent(db, usage.ACPUsageEvent{InteractionEvent: withLegacyID(legacy, "legacy-acp", "agent")}); err != nil {
		t.Fatal(err)
	}
	if err := configsqlite.InsertBuiltinUsageEvent(db, usage.BuiltinUsageEvent{InteractionEvent: withLegacyID(legacy, "legacy-builtin", "agent")}); err != nil {
		t.Fatal(err)
	}
	// v0.5.x has the four established usage families but no A2A table.
	if err := db.Exec(`DROP TABLE a2a_usage_events`).Error; err != nil {
		t.Fatal(err)
	}

	sink, query, err := NewSQLiteSink(backend, usage.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	for table, eventID := range map[string]string{
		"llm_usage_events": "legacy-llm", "mcp_usage_events": "legacy-mcp",
		"acp_usage_events": "legacy-acp", "builtin_usage_events": "legacy-builtin",
	} {
		var got string
		if err := db.Raw(fmt.Sprintf("SELECT event_id FROM %s", table)).Scan(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != eventID {
			t.Fatalf("%s event_id = %q, want %q", table, got, eventID)
		}
	}

	a2aEvent := usage.InteractionEvent{
		EventID: "post-upgrade-a2a", TraceID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SpanID: "bbbbbbbbbbbbbbbb",
		StartedAt: now, FinishedAt: now.Add(5 * time.Millisecond), RouteID: "native-a2a",
		RouteKind: "agent", RouteProtocol: "a2a", AgentID: "remote-agent", RuntimeType: "http",
		Success: true, StatusCode: 200, LatencyMS: 5,
	}
	if err := sink.Write(a2aEvent); err != nil {
		t.Fatal(err)
	}
	summary, err := query.Summary()
	if err != nil || summary.A2A.RequestCount != 1 {
		t.Fatalf("summary A2A = %+v, error = %v", summary.A2A, err)
	}
	recent, err := query.ListEvents("a2a", usage.EventListOptions{Limit: 10, Filters: map[string]string{"trace_id": a2aEvent.TraceID}})
	if err != nil || len(recent.Items) != 1 || recent.Items[0]["event_id"] != a2aEvent.EventID {
		t.Fatalf("recent A2A events = %#v, error = %v", recent.Items, err)
	}
	interactions, err := query.ListInteractions(usage.EventListOptions{Limit: 10, Filters: map[string]string{"route_protocol": "a2a"}})
	if err != nil || len(interactions.Items) != 1 || interactions.Items[0]["event_id"] != a2aEvent.EventID {
		t.Fatalf("A2A interactions = %#v, error = %v", interactions.Items, err)
	}
}

func withLegacyID(base usage.InteractionEvent, eventID, routeKind string) usage.InteractionEvent {
	base.EventID = eventID
	base.SpanID = eventID
	base.RouteKind = routeKind
	return base
}
