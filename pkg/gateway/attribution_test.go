package gateway

import (
	"context"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"strings"
	"testing"
)

func TestTrustedAttributionCoreAndFallback(t *testing.T) {
	g := NewAgentGateway()
	g.attributionPolicy, _ = attribution.NewPolicy([]string{"example.fixture"}, []string{"example.fixture/region"})
	d := usage.InteractionDimensions{RouteID: "route", AgentID: "agent", RunID: "run", VirtualKeyID: "key", TraceID: strings.Repeat("1", 32), SpanID: strings.Repeat("2", 16), ParentSpanID: strings.Repeat("3", 16)}
	v, _ := attribution.FromContext(g.AttributionContext(context.Background(), d))
	if v.Subject != "virtualkey:key" || v.Tenant != "" || v.AgentID != "agent" || v.RunID != "run" {
		t.Fatal(v)
	}
	g.attributionProducer = func(context.Context) (attribution.Value, error) {
		return attribution.Value{Subject: "trusted", Tenant: "tenant", RouteID: "forged", TraceID: "bad", Attributes: map[string]string{"example.fixture/region": "east"}}, nil
	}
	v, _ = attribution.FromContext(g.AttributionContext(context.Background(), d))
	if v.Subject != "trusted" || v.Tenant != "tenant" || v.RouteID != "route" || v.TraceID != d.TraceID {
		t.Fatal(v)
	}
	g.attributionProducer = func(context.Context) (attribution.Value, error) {
		return attribution.Value{Subject: strings.Repeat("x", 257), Tenant: "untrusted"}, nil
	}
	v, _ = attribution.FromContext(g.AttributionContext(context.Background(), d))
	if v.Subject != "virtualkey:key" || v.Tenant != "" || v.TraceID != d.TraceID {
		t.Fatal(v)
	}
	g.attributionProducer = func(context.Context) (attribution.Value, error) { panic("private") }
	v, _ = attribution.FromContext(g.AttributionContext(context.Background(), d))
	if v.Subject != "virtualkey:key" {
		t.Fatal(v)
	}
}

func TestIngressPublicCorrelationBoundsApplyPerField(t *testing.T) {
	g := NewAgentGateway()
	d := usage.InteractionDimensions{RouteID: strings.Repeat("x", 257), AgentID: "agent", RunID: "run", VirtualKeyID: "key", TraceID: strings.Repeat("1", 32), SpanID: strings.Repeat("2", 16)}
	v, ok := attribution.FromContext(g.AttributionContext(context.Background(), d))
	if !ok || v.RouteID != "" || v.AgentID != "agent" || v.RunID != "run" || v.Subject != "virtualkey:key" || v.TraceID != d.TraceID {
		t.Fatal(v)
	}
}
