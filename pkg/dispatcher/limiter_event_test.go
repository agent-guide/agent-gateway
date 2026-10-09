package dispatcher

import (
	"context"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/gateway"
	"github.com/agent-guide/agent-gateway/pkg/gateway/llmroute"
	"github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"net/http"
	"net/http/httptest"
	"testing"
)

type admissionPublisher struct{ events []event.Envelope }

func (p *admissionPublisher) Publish(_ context.Context, e event.Envelope) bool {
	p.events = append(p.events, e)
	return true
}
func TestConfiguredLimiterPublishesAllowDenyAndUnlimitedPublishesNothing(t *testing.T) {
	for _, limited := range []bool{true, false} {
		name := "unlimited"
		var limits *virtualkey.VirtualKeyRateLimits
		if limited {
			name = "limited"
			limits = &virtualkey.VirtualKeyRateLimits{LLM: &virtualkey.RateLimit{RequestsPerMinute: 1, Burst: 1}}
		}
		t.Run(name, func(t *testing.T) {
			p := &admissionPublisher{}
			g := gateway.NewAgentGateway()
			if err := g.Bootstrap(context.Background(), gateway.BootstrapOptions{
				EventPublisher:     p,
				ConfigStoreBackend: &testConfigStoreBackend{store: singleVirtualKeyStore{keyID: "vk-1", key: "fixture-bearer", route: "broad-route", rateLimits: limits}},
				StaticLLMRoutes:    mustRouteConfigs(t, []llmroute.LLMRoute{{AgentRouteConfig: llmroute.AgentRouteConfig{ID: "broad-route", Protocol: "local-stub", MatchPolicy: llmroute.RouteMatchPolicy{PathPrefix: "/"}, AuthPolicy: llmroute.RouteAuthPolicy{RequireVirtualKey: true}}, TargetPolicy: &llmroute.RouteDirectProviderPolicy{ProviderTarget: llmroute.DirectProviderTarget{ProviderID: "unused"}}}}),
			}); err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			h := NewHandler(g, map[string]LLMApiHandler{"local-stub": &localLLMApiHandler{}}, nil, HandlerOptions{})
			for i := 0; i < 2; i++ {
				req := admitRequest("fixture-bearer")
				req.Header.Set("X-Tenant-ID", "forged")
				req.Header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
				rec := httptest.NewRecorder()
				if err := h.Dispatch(rec, req, nil); err != nil {
					t.Fatal(err)
				}
				want := http.StatusOK
				if limited && i == 1 {
					want = http.StatusTooManyRequests
				}
				if rec.Code != want {
					t.Fatalf("request %d status %d want %d", i, rec.Code, want)
				}
				if !limited {
					if len(p.events) != 0 {
						t.Fatal("unlimited admission emitted limiter event")
					}
					continue
				}
				if len(p.events) != i+1 {
					t.Fatalf("events %d want %d", len(p.events), i+1)
				}
				e := p.events[i]
				if err := event.Validate(e, attribution.Policy{}); err != nil {
					t.Fatal(err)
				}
				outcome, reason := "allow", ""
				if i == 1 {
					outcome, reason = "deny", "rate_limited"
				}
				if e.Family != "limiter.admission" || e.Outcome != outcome || e.Reason != reason || e.Limiter.Dimension != "llm" || e.Limiter.BackendID != "online.agentguide.community.limiter" {
					t.Fatalf("invalid admission DTO %+v", e)
				}
				if e.Attribution.Subject != "virtualkey:vk-1" || e.Attribution.VirtualKeyID != "vk-1" || e.Attribution.RouteID != "broad-route" || e.Attribution.Tenant != "" || e.Attribution.TraceID != "11111111111111111111111111111111" || e.Attribution.SpanID != rec.Header().Get("X-Span-ID") || e.Attribution.ParentSpanID != "2222222222222222" {
					t.Fatalf("invalid trusted attribution %+v", e.Attribution)
				}
				if i == 0 && e.Limiter.RetryAfter != 0 || i == 1 && e.Limiter.RetryAfter <= 0 {
					t.Fatal("retry-after does not match outcome")
				}
			}
		})
	}
}
