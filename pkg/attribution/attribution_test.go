package attribution

import (
	"context"
	"strings"
	"testing"
)

func TestBoundsAndOwnership(t *testing.T) {
	p, err := NewPolicy([]string{"example.fixture"}, []string{"example.fixture/region"})
	if err != nil {
		t.Fatal(err)
	}
	v := Value{Subject: "subject", Tenant: "tenant", RouteID: "route", AgentID: "agent", RunID: "run", VirtualKeyID: "key", TraceID: strings.Repeat("1", 32), SpanID: strings.Repeat("2", 16), ParentSpanID: strings.Repeat("3", 16), Attributes: map[string]string{"example.fixture/region": "east"}}
	ctx, err := WithContext(context.Background(), v, p)
	if err != nil {
		t.Fatal(err)
	}
	v.Attributes["example.fixture/region"] = "mutated"
	got, ok := FromContext(ctx)
	if !ok || got.Attributes["example.fixture/region"] != "east" {
		t.Fatal(got)
	}
	got.Attributes["example.fixture/region"] = "again"
	got, _ = FromContext(ctx)
	if got.Attributes["example.fixture/region"] != "east" {
		t.Fatal("retrieval aliases")
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("absence")
	}
	for _, bad := range []Value{{Subject: strings.Repeat("x", 257)}, {TraceID: strings.Repeat("0", 32)}, {Attributes: map[string]string{"example.fixture/unlisted": "x"}}, {Attributes: map[string]string{"example.fixture/region": strings.Repeat("x", 257)}}} {
		if p.Validate(bad) == nil {
			t.Fatal("accepted invalid attribution")
		}
	}
}
