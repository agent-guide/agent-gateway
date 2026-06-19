package dispatcher

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
)

type traceContext struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	TraceState   string
	AgentDepth   int
}

func extractTraceContext(r *http.Request) traceContext {
	ctx := traceContext{SpanID: randomHex(8)}
	if r == nil {
		ctx.TraceID = randomHex(16)
		return ctx
	}
	if tp := strings.TrimSpace(r.Header.Get("traceparent")); tp != "" {
		parts := strings.Split(tp, "-")
		if len(parts) >= 4 && len(parts[1]) == 32 && len(parts[2]) == 16 {
			ctx.TraceID = strings.ToLower(parts[1])
			ctx.ParentSpanID = strings.ToLower(parts[2])
			ctx.TraceState = strings.TrimSpace(r.Header.Get("tracestate"))
		}
	}
	if ctx.TraceID == "" {
		ctx.TraceID = strings.TrimSpace(r.Header.Get("X-Trace-ID"))
		ctx.ParentSpanID = strings.TrimSpace(r.Header.Get("X-Span-ID"))
	}
	if ctx.TraceID == "" {
		ctx.TraceID = randomHex(16)
	}
	if depth, err := strconv.Atoi(strings.TrimSpace(r.Header.Get("X-Agent-Depth"))); err == nil && depth > 0 {
		ctx.AgentDepth = depth
	}
	return ctx
}

func writeTraceHeaders(w http.ResponseWriter, tc traceContext) {
	if w == nil {
		return
	}
	w.Header().Set("traceparent", "00-"+tc.TraceID+"-"+tc.SpanID+"-01")
	if tc.TraceState != "" {
		w.Header().Set("tracestate", tc.TraceState)
	}
	w.Header().Set("X-Trace-ID", tc.TraceID)
	w.Header().Set("X-Span-ID", tc.SpanID)
	w.Header().Set("X-Agent-Depth", strconv.Itoa(tc.AgentDepth+1))
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("0", bytes*2)
	}
	return hex.EncodeToString(buf)
}
