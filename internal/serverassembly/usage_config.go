package serverassembly

import (
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"maps"
)

// UsageConfig converts public metrics configuration into internal usage settings.
// It copies headers so the returned configuration owns its mutable values.
func UsageConfig(c distribution.MetricsConfig) usage.Config {
	return usage.Config{RetentionDays: c.RetentionDays, MaxAgentDepth: c.MaxAgentDepth, OTLP: usage.OTLPConfig{
		Endpoint: c.OTLP.Endpoint, Protocol: c.OTLP.Protocol, Insecure: c.OTLP.Insecure, Headers: maps.Clone(c.OTLP.Headers), ServiceName: c.OTLP.ServiceName, TimeoutSeconds: c.OTLP.TimeoutSeconds, Components: c.OTLP.Components, HTTPClientSpans: c.OTLP.HTTPClientSpans,
	}}
}
