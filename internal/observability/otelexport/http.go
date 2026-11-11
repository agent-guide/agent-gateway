package otelexport

import (
	"context"
	"time"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
	cr "github.com/agent-guide/agent-gateway/pkg/connector/runtime"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// ObserveHTTP exports a tracing-only child through the lifecycle exporter.
// It never registers a global callback, emits usage or writes a history row.
func (e *Exporter) ObserveHTTP(ctx context.Context, h cr.HTTPExecution, start time.Time) {
	dims, ok := usage.DimensionsFromContext(ctx)
	if !ok || h.Validate() != nil {
		return
	}
	tid, err := trace.TraceIDFromHex(dims.TraceID)
	if err != nil {
		return
	}
	parent, err := trace.SpanIDFromHex(dims.SpanID)
	if err != nil {
		return
	}
	id, err := randomSpanID()
	if err != nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("http.request.method", h.Method), attribute.String("server.address", h.HostPort), attribute.String("http.route", h.PathTemplate), attribute.Int64("http.request.body.size", h.RequestBodyBytes), attribute.Int64("http.response.body.size", h.ResponseBodyBytes)}
	if h.StatusCode != nil {
		attrs = append(attrs, attribute.Int("http.response.status_code", *h.StatusCode))
	}
	if h.NetworkErrorCategory != "" {
		attrs = append(attrs, attribute.String("error.type", h.NetworkErrorCategory))
	}
	stub := tracetest.SpanStub{Name: "HTTP " + h.Method, SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: id, TraceFlags: trace.FlagsSampled}), Parent: trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: parent, TraceFlags: trace.FlagsSampled}), SpanKind: trace.SpanKindClient, StartTime: start, EndTime: start.Add(time.Duration(h.DurationMS) * time.Millisecond), Attributes: attrs}
	if h.NetworkErrorCategory != "" || h.StatusCode != nil && *h.StatusCode >= 400 {
		stub.Status = sdktrace.Status{Code: codes.Error}
	}
	e.exportComponentSpan(stub)
}
