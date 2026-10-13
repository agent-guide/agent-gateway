package event_test

import (
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"testing"
	"time"
)

func TestEventsAcceptRegistryModuleIdentityGrammar(t *testing.T) {
	for _, id := range []string{"example.-fixture", "example.fixture-", "-.fixture", "example.-", "example.fixture"} {
		t.Run(id, func(t *testing.T) {
			_, err := extension.NewRegistry(extension.Registration{Descriptor: extension.Descriptor{ID: id, Version: "1.0.0", SchemaVersion: 1, Roles: []string{"runtime"}}})
			if err != nil {
				t.Fatal(err)
			}
			e := event.Envelope{SchemaVersion: 1, ID: "test", Timestamp: time.Now().UTC(), Family: "extension.mutation", Outcome: "success", Mutation: &event.ExtensionMutation{ModuleID: id, Operation: "put"}}
			if err := event.Validate(e, attribution.Policy{}); err != nil {
				t.Fatalf("registry identity rejected in mutation: %v", err)
			}
			e.Family = "limiter.admission"
			e.Outcome = "allow"
			e.Mutation = nil
			e.Limiter = &event.LimiterAdmission{BackendID: id, Dimension: "llm"}
			if err := event.Validate(e, attribution.Policy{}); err != nil {
				t.Fatalf("registry identity rejected in admission: %v", err)
			}
		})
	}
}
