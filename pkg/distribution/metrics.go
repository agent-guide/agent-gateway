package distribution

// MetricsConfig is the public metrics configuration value for entry options.
type MetricsConfig struct {
	RetentionDays int        `json:"retention_days,omitempty"`
	MaxAgentDepth int        `json:"max_agent_depth,omitempty"`
	OTLP          OTLPConfig `json:"otlp,omitzero"`
}

// OTLPConfig configures the existing usage exporter. Empty Endpoint disables it.
type OTLPConfig struct {
	Endpoint       string            `json:"endpoint,omitempty"`
	Protocol       string            `json:"protocol,omitempty"`
	Insecure       bool              `json:"insecure,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	ServiceName    string            `json:"service_name,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	Components     bool              `json:"components,omitempty"`
}
