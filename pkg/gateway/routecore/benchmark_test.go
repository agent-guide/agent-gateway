package routecore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
)

func BenchmarkRouteMatching(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			routes := make([]RouteConfig, count)
			for i := range routes {
				routes[i] = RouteConfig{ID: fmt.Sprint(i), Kind: RouteKindLLM, Protocol: RouteProtocolOpenAI,
					MatchPolicy:  RouteMatchPolicy{PathPrefix: fmt.Sprintf("/routes/%04d/", i), Methods: []string{"POST"}},
					TargetPolicy: json.RawMessage(`{"type":"models","default_model":"gpt-4.1","model_targets":[{"name":"gpt-4.1","kind":"provider-model","provider_id":"main","upstream_model":"gpt-4.1"}]}`)}
			}
			m := NewRouteConfigManager(nil)
			m.InitStaticRoutes(routes)
			ctx := context.Background()
			if err := m.Refresh(ctx); err != nil {
				b.Fatal(err)
			}
			req := httptest.NewRequest("POST", fmt.Sprintf("http://example.org/routes/%04d/chat", count-1), nil)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, ok, err := m.Match(ctx, req); err != nil || !ok {
					b.Fatalf("match: %v, %v", ok, err)
				}
			}
		})
	}
}
