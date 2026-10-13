// Package distributionmetrics adapts public entry values to internal usage wiring.
package distributionmetrics

import (
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"maps"
)

func From(c distribution.MetricsConfig) usage.Config {
	return usage.Config{RetentionDays: c.RetentionDays, MaxAgentDepth: c.MaxAgentDepth, OTLP: usage.OTLPConfig{
		Endpoint: c.OTLP.Endpoint, Protocol: c.OTLP.Protocol, Insecure: c.OTLP.Insecure, Headers: maps.Clone(c.OTLP.Headers), ServiceName: c.OTLP.ServiceName, TimeoutSeconds: c.OTLP.TimeoutSeconds, Components: c.OTLP.Components,
	}}
}
