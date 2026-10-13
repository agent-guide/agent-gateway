package event

import (
	"errors"
	"fmt"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"strings"
	"testing"
	"time"
)

func TestDTOBoundary(t *testing.T) {
	e := Envelope{SchemaVersion: 1, ID: "test", Timestamp: time.Now().UTC(), Family: "extension.mutation", Outcome: "success", Mutation: &ExtensionMutation{ModuleID: "example.fixture", Operation: "put"}}
	if err := Validate(e, attribution.Policy{}); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Envelope){func(e *Envelope) { e.Reason = "secret-value" }, func(e *Envelope) { e.Mutation.ModuleID = strings.Repeat("x", 129) }, func(e *Envelope) { e.Limiter = &LimiterAdmission{} }, func(e *Envelope) { e.Family = "usage" }, func(e *Envelope) { e.Attribution.Attributes = map[string]string{"unlisted": "prompt"} }} {
		copy := e
		x := *e.Mutation
		copy.Mutation = &x
		change(&copy)
		if Validate(copy, attribution.Policy{}) == nil {
			t.Fatal("invalid DTO accepted")
		}
	}
}

func TestEncodedBoundsIncludeEscaping(t *testing.T) {
	keys := make([]string, 16)
	attrs := map[string]string{}
	for i := range keys {
		keys[i] = fmt.Sprintf("example.fixture/key%d", i)
		attrs[keys[i]] = strings.Repeat("\x00", 200)
	}
	p, err := attribution.NewPolicy([]string{"example.fixture"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	e := fixture("bounded")
	e.Attribution.Attributes = attrs
	if err := p.Validate(e.Attribution); err != nil {
		t.Fatal(err)
	}
	err = Validate(e, p)
	var bounded *Error
	if !errors.As(err, &bounded) || bounded.Code != "event_too_large" {
		t.Fatalf("JSON escape bound: %v", err)
	}
}
