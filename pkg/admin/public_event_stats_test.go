package admin

import (
	"context"
	"github.com/agent-guide/agent-gateway/internal/observability/pipeline"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"testing"
)

type statsSink struct{}

func (statsSink) Write(context.Context, event.Envelope) error { return nil }
func (statsSink) Flush(context.Context) error                 { return nil }
func (statsSink) Close(context.Context) error                 { return nil }
func TestPublicEventHealthVisibleInMetrics(t *testing.T) {
	p := pipeline.NewEventPipeline(1)
	if err := p.ConfigurePublic(attribution.Policy{}, []event.Registration{{New: func(context.Context) (event.Sink, error) { return statsSink{}, nil }}}); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	svc := usage.NewUsageService(p, nil)
	svc.Publish(context.Background(), event.Envelope{})
	h := &Handler{usageStats: svc}
	stats := h.pipelineStats()
	if stats.RejectedEvents != 1 || len(stats.PublicSinks) != 1 || !stats.PublicSinks[0].Healthy {
		t.Fatal(stats)
	}
}
