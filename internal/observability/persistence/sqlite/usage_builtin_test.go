package sqlite

import (
	"fmt"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
)

func TestBuiltinSummaryFiltersAgentTimeAndExpiry(t *testing.T) {
	backend, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: t.TempDir() + "/usage.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	db := backend.Database()
	if err := MigrateUsageTables(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for i, tc := range []struct {
		agent, op string
		when      time.Time
		success   bool
		latency   int64
	}{
		{"a", "turn", now, true, 100}, {"a", "turn", now, false, 200}, {"a", "resume", now, true, 50},
		{"a", "permission_expire", now, false, 0}, {"b", "turn", now, true, 999}, {"a", "turn", now.Add(-2 * time.Hour), true, 999},
	} {
		err := InsertBuiltinUsageEvent(db, usage.BuiltinUsageEvent{InteractionEvent: usage.InteractionEvent{EventID: fmt.Sprint(i), SpanID: fmt.Sprint(i), AgentID: tc.agent, RouteKind: "agent", RuntimeType: "builtin", StartedAt: tc.when, FinishedAt: tc.when.Add(time.Duration(tc.latency) * time.Millisecond), Success: tc.success, LatencyMS: tc.latency}, Operation: tc.op})
		if err != nil {
			t.Fatal(err)
		}
	}
	q := NewUsageQueries(db)
	opts := usage.BreakdownOptions{GroupBy: "operation", From: now.Add(-time.Hour).Format(time.RFC3339), To: now.Add(time.Hour).Format(time.RFC3339), Attribution: &usage.AttributionFilter{AgentID: "a"}}
	got, err := q.BuiltinSummary(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items=%v", got.Items)
	}
	for _, row := range got.Items {
		switch row["group_value"] {
		case "turn":
			if row["request_count"] != int64(2) || row["turn_count"] != int64(2) || row["success_count"] != int64(1) || row["failure_count"] != int64(1) || row["avg_latency_ms"] != float64(150) {
				t.Fatalf("turn=%v", row)
			}
		case "resume":
			if row["request_count"] != int64(1) || row["turn_count"] != int64(0) {
				t.Fatalf("resume=%v", row)
			}
		default:
			t.Fatalf("unexpected operation=%v", row)
		}
	}
	opts.From = "invalid"
	if _, err := q.BuiltinSummary(opts); err == nil {
		t.Fatal("invalid time accepted")
	}
}
