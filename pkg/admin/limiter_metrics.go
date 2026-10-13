package admin

import (
	"fmt"
	vk "github.com/agent-guide/agent-gateway/pkg/gateway/virtualkey"
	"sort"
	"strings"
)

func (h *Handler) limiterPrometheus() string {
	if h.virtualKeyManager == nil {
		return ""
	}
	counters := h.virtualKeyManager.LimiterCounters()
	labels := make([]vk.LimiterLabels, 0, len(counters))
	for k := range counters {
		labels = append(labels, k)
	}
	sort.Slice(labels, func(i, j int) bool {
		a, b := labels[i], labels[j]
		if a.Dimension != b.Dimension {
			return a.Dimension < b.Dimension
		}
		if a.Outcome != b.Outcome {
			return a.Outcome < b.Outcome
		}
		return a.BackendType < b.BackendType
	})
	var out strings.Builder
	out.WriteString("# HELP agentgateway_limiter_admissions_total Limiter outcomes by dimension and bounded backend type.\n# TYPE agentgateway_limiter_admissions_total counter\n")
	for _, l := range labels {
		fmt.Fprintf(&out, "agentgateway_limiter_admissions_total{dimension=%q,outcome=%q,backend_type=%q} %d\n", l.Dimension, l.Outcome, l.BackendType, counters[l])
	}
	return out.String()
}
