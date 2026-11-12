package connector_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/connector"
	connectormcp "github.com/agent-guide/agent-gateway/pkg/connector/mcp"
)

func TestConnectorIdentityIsImmutableAndNameIsDisplayOnly(t *testing.T) {
	for _, kind := range []string{"mcp", "http_api"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			manager := connector.NewManager(newTestConfigStore(t))
			t.Cleanup(manager.Close)
			cfg := connector.ConnectorConfig{ID: "Orders", Name: "Order system", Type: "mcp", MCP: &connectormcp.Config{Transport: connector.TransportStdio, Command: "unused"}}
			if kind == "http_api" {
				cfg = httpConnector(t, "https://orders.example.com", "/lookup")
				cfg.ID = "Orders"
			}
			if err := manager.Create(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			original, err := manager.Get(ctx, "Orders")
			if err != nil {
				t.Fatal(err)
			}
			generation := manager.GenerationVector([]string{"Orders"})
			for _, id := range []string{"orders", "other"} {
				changed := original
				changed.ID = id
				changed.Name = "Rejected update"
				if err := manager.Update(ctx, "Orders", changed); err == nil || !strings.Contains(err.Error(), "id is immutable") {
					t.Fatalf("ID %q update: %v", id, err)
				}
				stored, err := manager.Get(ctx, "Orders")
				if err != nil || stored.Name != original.Name || !stored.UpdatedAt.Equal(original.UpdatedAt) {
					t.Fatalf("rejected update changed original: %#v %v", stored, err)
				}
				if _, err := manager.Get(ctx, id); !errors.Is(err, connector.ErrConnectorNotConfigured) {
					t.Fatalf("ID %q unexpectedly exists: %v", id, err)
				}
				if generation != manager.GenerationVector([]string{"Orders"}) {
					t.Fatal("rejected update published a generation")
				}
				if _, pending := manager.PendingMutation("Orders"); pending {
					t.Fatal("invalid ID created pending mutation")
				}
			}
			for _, bodyID := range []string{"Orders", ""} {
				changed := original
				changed.ID = bodyID
				changed.Name = "订单中心"
				if err := manager.Update(ctx, "Orders", changed); err != nil {
					t.Fatal(err)
				}
				stored, err := manager.Get(ctx, "Orders")
				if err != nil || stored.ID != "Orders" || stored.Name != "订单中心" || !stored.CreatedAt.Equal(original.CreatedAt) {
					t.Fatalf("display rename: %#v %v", stored, err)
				}
			}
			// Display names may repeat. A second ID denotes a separate connector.
			other := original
			other.ID = "other"
			other.Name = "订单中心"
			if err := manager.Create(ctx, other); err != nil {
				t.Fatalf("display names treated as unique identifiers: %v", err)
			}
		})
	}
}
