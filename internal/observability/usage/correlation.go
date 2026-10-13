package usage

import (
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"unicode/utf8"
)

// CorrelationFromDimensions projects runtime identity into the bounded public
// context. Invalid individual identifiers are absent; other valid correlation
// still advances. The original dimensions remain unchanged for usage accounting.
func CorrelationFromDimensions(d InteractionDimensions) attribution.Value {
	bounded := func(s string) string {
		if len(s) > 256 || !utf8.ValidString(s) {
			return ""
		}
		return s
	}
	v := attribution.Value{RouteID: bounded(d.RouteID), AgentID: bounded(d.AgentID), RunID: bounded(d.RunID), VirtualKeyID: bounded(d.VirtualKeyID)}
	if ValidTraceID(d.TraceID) {
		v.TraceID = d.TraceID
	}
	if ValidSpanID(d.SpanID) {
		v.SpanID = d.SpanID
	}
	if ValidSpanID(d.ParentSpanID) {
		v.ParentSpanID = d.ParentSpanID
	}
	return v
}
