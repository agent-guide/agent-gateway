package sqlite

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	configsqlite "github.com/agent-guide/agent-gateway/pkg/configstore/sqlite"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
)

func TestHTTPExecutionSummaryRoundTripAndLegacyNull(t *testing.T) {
	creator, err := configsqlite.Open(t.Context(), configsqlite.Config{SQLitePath: t.TempDir() + "/events.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer creator.Close()
	db := creator.Database()
	if err := MigrateUsageTables(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	status := 200
	event := usage.MCPUsageEvent{InteractionEvent: usage.InteractionEvent{EventID: "http-tool", SpanID: "span", RouteKind: "mcp", StartedAt: now, FinishedAt: now}, ConnectorType: "http_api", HTTPExecution: &cr.HTTPExecution{Method: "POST", HostPort: "api.example.com:443", PathTemplate: "/orders/{id}", StatusCode: &status, DurationMS: 3, RequestBodyBytes: 20, ResponseBodyBytes: 30}, ServiceID: "orders", Method: "tools/call"}
	if err := InsertMCPUsageEvent(db, event); err != nil {
		t.Fatal(err)
	}
	event.EventID = "legacy"
	event.HTTPExecution = nil
	event.ConnectorType = ""
	if err := InsertMCPUsageEvent(db, event); err != nil {
		t.Fatal(err)
	}
	query := NewUsageQueries(db)
	rows, err := query.ListEvents("mcp", usage.EventListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Items) != 2 {
		t.Fatalf("rows: %#v", rows)
	}
	for _, row := range rows.Items {
		switch row["event_id"] {
		case "http-tool":
			summary, ok := row["http_execution"].(*cr.HTTPExecution)
			if !ok || summary.PathTemplate != "/orders/{id}" || summary.StatusCode == nil || *summary.StatusCode != 200 || row["connector_type"] != "http_api" {
				t.Fatalf("summary lost: %#v", row)
			}
		case "legacy":
			if row["connector_type"] != nil || row["http_execution"] != nil {
				t.Fatal("historical types inferred")
			}
		}
	}
}
func TestHTTPExecutionRejectsUnknownAndMissingFields(t *testing.T) {
	for _, raw := range []string{`{"method":"GET","host_port":"example.com:443","path_template":"/","status_code":null,"duration_ms":0,"request_body_bytes":0,"response_body_bytes":0,"body":"secret"}`, `{"method":"GET","host_port":"example.com:443","path_template":"/","duration_ms":0,"request_body_bytes":0,"response_body_bytes":0}`, `{"method":"GET","host_port":"example.com:443","path_template":"/","status_code":null,"duration_ms":-1,"request_body_bytes":0,"response_body_bytes":0}`} {
		var h cr.HTTPExecution
		if json.Unmarshal([]byte(raw), &h) == nil {
			t.Fatal("invalid summary accepted")
		}
	}
}
