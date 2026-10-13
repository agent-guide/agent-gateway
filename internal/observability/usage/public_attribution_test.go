package usage

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"strings"
	"testing"
)

func TestRuntimeDimensionsPropagatePublicCorrelationAndRetainTrustedIdentity(t *testing.T) {
	p, err := attribution.NewPolicy([]string{"example.fixture"}, []string{"example.fixture/region"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := attribution.WithContext(context.Background(), attribution.Value{Subject: "trusted-subject", Tenant: "trusted-tenant", Attributes: map[string]string{"example.fixture/region": "east"}}, p)
	if err != nil {
		t.Fatal(err)
	}
	d := InteractionDimensions{RouteID: "nested-route", AgentID: "agent", RunID: "run", VirtualKeyID: "key", TraceID: strings.Repeat("1", 32), SpanID: strings.Repeat("2", 16), ParentSpanID: strings.Repeat("3", 16)}
	_, ctx = NewObserver(&InMemorySink{}).Begin(ctx, d)
	v, _ := attribution.FromContext(ctx)
	if v.Subject != "trusted-subject" || v.Tenant != "trusted-tenant" || v.Attributes["example.fixture/region"] != "east" || v.RouteID != d.RouteID || v.AgentID != d.AgentID || v.RunID != d.RunID || v.VirtualKeyID != d.VirtualKeyID || v.TraceID != d.TraceID || v.SpanID != d.SpanID || v.ParentSpanID != d.ParentSpanID {
		t.Fatalf("public correlation %#v", v)
	}
	ctx = ContextWithDimensions(ctx, InteractionDimensions{RouteID: "direct-route"})
	v, _ = attribution.FromContext(ctx)
	if v.AgentID != "" || v.RunID != "" || v.ParentSpanID != "" || v.RouteID != "direct-route" || v.Tenant != "trusted-tenant" {
		t.Fatalf("explicit absence %#v", v)
	}
}

func TestInvalidRuntimeFieldDoesNotRetainStalePublicCorrelation(t *testing.T) {
	for _, bad := range []string{strings.Repeat("x", 257), string([]byte{0xff})} {
		ctx, err := attribution.WithContext(context.Background(), attribution.Value{Subject: "trusted", Tenant: "tenant", RouteID: "old-route", AgentID: "old-agent", RunID: "old-run", TraceID: strings.Repeat("1", 32)}, attribution.Policy{})
		if err != nil {
			t.Fatal(err)
		}
		d := InteractionDimensions{RouteID: bad, AgentID: "new-agent", RunID: "new-run", TraceID: strings.Repeat("2", 32), SpanID: strings.Repeat("3", 16), ParentSpanID: "invalid-parent"}
		ctx = ContextWithDimensions(ctx, d)
		v, _ := attribution.FromContext(ctx)
		if v.RouteID != "" || v.AgentID != d.AgentID || v.RunID != d.RunID || v.TraceID != d.TraceID || v.SpanID != d.SpanID || v.ParentSpanID != "" || v.Subject != "trusted" || v.Tenant != "tenant" {
			t.Fatalf("stale public correlation %+v", v)
		}
		raw, _ := DimensionsFromContext(ctx)
		if raw.RouteID != bad {
			t.Fatal("public bounds changed usage accounting")
		}
	}
}
