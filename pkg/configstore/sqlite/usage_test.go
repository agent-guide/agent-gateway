package sqlite

import (
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/metrics/usage"
)

func TestUsageWriterQuerySummaryAndEvents(t *testing.T) {
	backend, err := Open(t.Context(), Config{SQLitePath: t.TempDir() + "/usage.db"}, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	db := backend.UsageDB()
	if db == nil {
		t.Fatal("UsageDB() = nil")
	}
	if err := MigrateUsageTables(db); err != nil {
		t.Fatalf("MigrateUsageTables() error = %v", err)
	}
	now := time.Now().UTC()
	if err := InsertLLMUsageEvent(db, usage.LLMUsageEvent{
		InteractionEvent: usage.InteractionEvent{
			EventID: "ev-1", SpanID: "span-1", StartedAt: now, FinishedAt: now.Add(10 * time.Millisecond),
			RouteID: "route-1", RouteKind: "llm", RouteProtocol: "openai", Success: true, StatusCode: 200, LatencyMS: 10,
		},
		LLMAPI: "openai", ProviderID: "provider-1", InputTokens: 3, OutputTokens: 4, TotalTokens: 7, UsageFinalized: true,
	}); err != nil {
		t.Fatalf("InsertLLMUsageEvent() error = %v", err)
	}
	q := NewUsageQueries(db)
	summary, err := q.Summary()
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}
	if summary.LLM.RequestCount != 1 || summary.LLM.TotalTokens != 7 {
		t.Fatalf("summary.LLM = %+v, want one request and 7 tokens", summary.LLM)
	}
	events, err := q.ListEvents("llm", usage.EventListOptions{
		Limit:   10,
		Filters: map[string]string{"provider_id": "provider-1"},
	})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events.Items) != 1 || events.Items[0]["event_id"] != "ev-1" {
		t.Fatalf("events = %#v, want ev-1", events.Items)
	}
	interactions, err := q.ListInteractions(usage.EventListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListInteractions() error = %v", err)
	}
	if len(interactions.Items) != 1 || interactions.Items[0]["route_kind"] != "llm" {
		t.Fatalf("interactions = %#v, want llm interaction", interactions.Items)
	}
	timeseries, err := q.LLMTimeseries(usage.TimeseriesOptions{Bucket: "hour", GroupBy: "provider_id"})
	if err != nil {
		t.Fatalf("LLMTimeseries() error = %v", err)
	}
	if len(timeseries.Items) != 1 || timeseries.Items[0]["group_value"] != "provider-1" {
		t.Fatalf("timeseries = %#v, want provider-1", timeseries.Items)
	}
	breakdown, err := q.LLMBreakdown(usage.BreakdownOptions{GroupBy: "provider_id", OrderBy: "total_tokens", Limit: 10})
	if err != nil {
		t.Fatalf("LLMBreakdown() error = %v", err)
	}
	if len(breakdown.Items) != 1 || breakdown.Items[0]["group_value"] != "provider-1" {
		t.Fatalf("breakdown = %#v, want provider-1", breakdown.Items)
	}
	if err := InsertMCPUsageEvent(db, usage.MCPUsageEvent{
		InteractionEvent: usage.InteractionEvent{EventID: "ev-2", SpanID: "span-2", StartedAt: now, FinishedAt: now, RouteID: "mcp-route", RouteKind: "mcp", RouteProtocol: "mcp", Success: true, StatusCode: 200},
		ServiceID:        "svc-1", Method: "tools/call", ToolName: "lookup",
	}); err != nil {
		t.Fatalf("InsertMCPUsageEvent() error = %v", err)
	}
	toolSummary, err := q.MCPToolsSummary(usage.SummaryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("MCPToolsSummary() error = %v", err)
	}
	if len(toolSummary.Items) != 1 || toolSummary.Items[0]["group_value"] != "lookup" {
		t.Fatalf("tool summary = %#v, want lookup", toolSummary.Items)
	}
	if err := InsertACPUsageEvent(db, usage.ACPUsageEvent{
		InteractionEvent: usage.InteractionEvent{EventID: "ev-3", SpanID: "span-3", StartedAt: now, FinishedAt: now, RouteID: "acp-route", RouteKind: "acp", RouteProtocol: "acp", Success: true, StatusCode: 200},
		ServiceID:        "acp-svc", AgentType: "codex", Operation: "turn",
	}); err != nil {
		t.Fatalf("InsertACPUsageEvent() error = %v", err)
	}
	acpSummary, err := q.ACPSummary(usage.BreakdownOptions{GroupBy: "operation", Limit: 10})
	if err != nil {
		t.Fatalf("ACPSummary() error = %v", err)
	}
	if len(acpSummary.Items) != 1 || acpSummary.Items[0]["group_value"] != "turn" {
		t.Fatalf("acp summary = %#v, want turn", acpSummary.Items)
	}
	interactionSummary, err := q.InteractionsSummary(usage.BreakdownOptions{GroupBy: "route_kind", Limit: 10})
	if err != nil {
		t.Fatalf("InteractionsSummary() error = %v", err)
	}
	if len(interactionSummary.Items) != 3 {
		t.Fatalf("interaction summary count = %d, want 3 groups: %#v", len(interactionSummary.Items), interactionSummary.Items)
	}
	old := now.Add(-40 * 24 * time.Hour)
	if err := InsertLLMUsageEvent(db, usage.LLMUsageEvent{
		InteractionEvent: usage.InteractionEvent{EventID: "ev-old", SpanID: "span-old", StartedAt: old, FinishedAt: old, RouteID: "old", RouteKind: "llm", RouteProtocol: "openai", Success: true, StatusCode: 200},
		LLMAPI:           "openai", ProviderID: "provider-old",
	}); err != nil {
		t.Fatalf("Insert old LLM event error = %v", err)
	}
	if err := CleanupUsageEvents(db, 30*24*time.Hour); err != nil {
		t.Fatalf("CleanupUsageEvents() error = %v", err)
	}
	var oldCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM llm_usage_events WHERE event_id='ev-old'`).Row().Scan(&oldCount); err != nil {
		t.Fatalf("query old count: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("old event count = %d, want 0", oldCount)
	}
}
