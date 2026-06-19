package pipeline

// OTelExporter is a small adapter point for deployments that want to translate
// gateway usage events into OpenTelemetry spans, logs, or metrics without
// forcing an OpenTelemetry dependency into the gateway core.
type OTelExporter interface {
	ExportUsageEvent(any) error
	Close() error
}

type OpenTelemetrySink struct {
	exporter OTelExporter
}

func NewOpenTelemetrySink(exporter OTelExporter) *OpenTelemetrySink {
	return &OpenTelemetrySink{exporter: exporter}
}

func (s *OpenTelemetrySink) Write(ev any) error {
	if s == nil || s.exporter == nil {
		return nil
	}
	return s.exporter.ExportUsageEvent(ev)
}

func (s *OpenTelemetrySink) Close() error {
	if s == nil || s.exporter == nil {
		return nil
	}
	return s.exporter.Close()
}
