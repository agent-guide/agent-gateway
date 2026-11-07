package runtimeassembly

import (
	"context"

	"github.com/agent-guide/agent-gateway/internal/observability/einotap"
	"github.com/agent-guide/agent-gateway/internal/observability/otelexport"
	"github.com/agent-guide/agent-gateway/internal/observability/pipeline"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// NewUsage preserves the host degradation policy: without a persistent sink,
// the in-memory pipeline has no historical queries, Prometheus or OTLP sinks.
// The returned service owns sinks and workers, but borrows the database.
func NewUsage(backend configstore.ConfigStoreBackend, logger *zap.Logger, cfg usage.Config) *usage.UsageService {
	einotap.Register()
	dbProvider, ok := backend.(interface{ Database() *gorm.DB })
	if !ok {
		return usage.NewUsageService(pipeline.NewEventPipeline(4096), nil)
	}
	sink, query, err := pipeline.NewSQLiteSink(dbProvider.Database(), cfg)
	if err != nil {
		if logger != nil {
			logger.Warn("usage metrics sqlite sink unavailable", zap.Error(err))
		}
		return usage.NewUsageService(pipeline.NewEventPipeline(4096), nil)
	}
	promSink := pipeline.NewPrometheusSink()
	sinks := []pipeline.Sink{sink, promSink}
	if otlp := cfg.Normalized().OTLP; otlp.Enabled() {
		exporter, err := otelexport.New(context.Background(), otlp)
		if err != nil {
			if logger != nil {
				logger.Warn("otlp usage exporter unavailable", zap.Error(err))
			}
		} else {
			sinks = append(sinks, pipeline.NewOpenTelemetrySink(exporter))
			if otlp.Components {
				otelexport.EnableComponentTap(exporter)
			}
			if logger != nil {
				logger.Info("otlp usage export enabled",
					zap.String("endpoint", otlp.Endpoint), zap.String("protocol", otlp.Protocol),
					zap.Bool("components", otlp.Components))
			}
		}
	}
	svc := usage.NewUsageService(pipeline.NewEventPipeline(4096, sinks...), query)
	svc.AttachPrometheus(promSink)
	return svc
}
