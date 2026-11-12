package admin

import (
	"net/http"

	"github.com/agent-guide/agent-gateway/pkg/event"

	"github.com/agent-guide/agent-gateway/internal/httpjson"
	"github.com/agent-guide/agent-gateway/internal/observability/usage"
)

type metricsResponse struct {
	usage.Summary
	Pipeline pipelineStats `json:"pipeline"`
}

type pipelineStats struct {
	RejectedEvents uint64        `json:"rejected_events"`
	PublicSinks    []event.Stats `json:"public_sinks,omitempty"`
	DroppedEvents  uint64        `json:"dropped_events"`
	WriteFailures  uint64        `json:"write_failures"`
}

func (h *Handler) pipelineStats() pipelineStats {
	if h.usageStats == nil {
		return pipelineStats{}
	}
	stats := pipelineStats{
		DroppedEvents: h.usageStats.DroppedEvents(),
		WriteFailures: h.usageStats.WriteFailures(),
	}
	if source, ok := h.usageStats.(interface {
		RejectedEvents() uint64
		PublicStats() []event.Stats
	}); ok {
		stats.RejectedEvents = source.RejectedEvents()
		stats.PublicSinks = source.PublicStats()
	}
	return stats
}

func (h *Handler) handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	var snap usage.PrometheusSnapshot
	if h.usagePrometheus != nil {
		snap = h.usagePrometheus.PrometheusSnapshot()
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(usage.RenderPrometheus(snap, h.usageStats) + h.limiterPrometheus()))
}

func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	resp := metricsResponse{Pipeline: h.pipelineStats()}
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, resp)
		return
	}
	summary, err := h.usageQuery.Summary()
	if err != nil {
		_ = httpjson.Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Summary = summary
	_ = httpjson.Write(w, http.StatusOK, resp)
}

func (h *Handler) handleListLLMMetricsEvents(w http.ResponseWriter, r *http.Request) {
	h.handleListMetricsEvents(w, r, "llm", []string{
		"route_id", "provider_id", "virtual_key_id", "logical_model", "upstream_model", "llm_api", "api_operation",
		"request_tool_name", "has_tool_use",
	})
}

func (h *Handler) handleLLMMetricsTimeseries(w http.ResponseWriter, r *http.Request) {
	opts, err := metricTimeseriesOptions(r, []string{"route_id", "provider_id", "virtual_key_id", "upstream_model", "llm_api"})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	attribution, ok := h.agentAttributionFromRequest(w, r)
	if !ok {
		return
	}
	opts.Attribution = attribution
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.SeriesResponse{Bucket: opts.Bucket, GroupBy: opts.GroupBy})
		return
	}
	resp, err := h.usageQuery.LLMTimeseries(opts)
	writeMetricResult(w, resp, err)
}

func (h *Handler) handleLLMMetricsBreakdown(w http.ResponseWriter, r *http.Request) {
	opts, err := metricBreakdownOptions(r, []string{"route_id", "provider_id", "virtual_key_id", "upstream_model", "llm_api"})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	attribution, ok := h.agentAttributionFromRequest(w, r)
	if !ok {
		return
	}
	opts.Attribution = attribution
	if opts.GroupBy == "" {
		opts.GroupBy = "route_id"
	}
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.BreakdownResponse{GroupBy: opts.GroupBy, Limit: opts.Limit})
		return
	}
	resp, err := h.usageQuery.LLMBreakdown(opts)
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if opts.GroupBy == "" {
		opts.GroupBy = "route_kind"
	}
	_ = httpjson.Write(w, http.StatusOK, resp)
}

func (h *Handler) handleListMCPMetricsEvents(w http.ResponseWriter, r *http.Request) {
	h.handleListMetricsEvents(w, r, "mcp", []string{
		"connector_type", "route_id", "service_id", "virtual_key_id", "method", "tool_name", "resource_uri", "prompt_name", "result_status",
		"completion_ref_type", "completion_argument",
	})
}

func (h *Handler) handleMCPMetricsTimeseries(w http.ResponseWriter, r *http.Request) {
	opts, err := metricTimeseriesOptions(r, []string{"connector_type", "route_id", "service_id", "virtual_key_id", "method", "tool_name", "result_status"})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	attribution, ok := h.agentAttributionFromRequest(w, r)
	if !ok {
		return
	}
	opts.Attribution = attribution
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.SeriesResponse{Bucket: opts.Bucket, GroupBy: opts.GroupBy})
		return
	}
	resp, err := h.usageQuery.MCPTimeseries(opts)
	writeMetricResult(w, resp, err)
}

func (h *Handler) handleMCPMetricsBreakdown(w http.ResponseWriter, r *http.Request) {
	opts, err := metricBreakdownOptions(r, []string{"connector_type", "route_id", "service_id", "virtual_key_id", "method", "tool_name", "result_status"})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	attribution, ok := h.agentAttributionFromRequest(w, r)
	if !ok {
		return
	}
	opts.Attribution = attribution
	if opts.GroupBy == "" {
		opts.GroupBy = "tool_name"
	}
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.BreakdownResponse{GroupBy: opts.GroupBy, Limit: opts.Limit})
		return
	}
	resp, err := h.usageQuery.MCPBreakdown(opts)
	writeMetricResult(w, resp, err)
}

func (h *Handler) handleMCPMetricsToolsSummary(w http.ResponseWriter, r *http.Request) {
	opts, err := metricSummaryOptions(r, []string{"route_id", "service_id"})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.BreakdownResponse{GroupBy: "tool_name", Limit: opts.Limit})
		return
	}
	resp, err := h.usageQuery.MCPToolsSummary(opts)
	writeMetricResult(w, resp, err)
}

func (h *Handler) handleListACPMetricsEvents(w http.ResponseWriter, r *http.Request) {
	h.handleListMetricsEvents(w, r, "acp", []string{
		"route_id", "route_protocol", "virtual_key_id", "agent_type", "operation", "thread_id", "session_id",
	})
}

func (h *Handler) handleACPMetricsTimeseries(w http.ResponseWriter, r *http.Request) {
	opts, err := metricTimeseriesOptions(r, []string{"route_id", "route_protocol", "virtual_key_id", "agent_type", "operation"})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	attribution, ok := h.agentAttributionFromRequest(w, r)
	if !ok {
		return
	}
	opts.Attribution = attribution
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.SeriesResponse{Bucket: opts.Bucket, GroupBy: opts.GroupBy})
		return
	}
	resp, err := h.usageQuery.ACPTimeseries(opts)
	writeMetricResult(w, resp, err)
}

func (h *Handler) handleACPMetricsBreakdown(w http.ResponseWriter, r *http.Request) {
	opts, err := metricBreakdownOptions(r, []string{"route_id", "route_protocol", "virtual_key_id", "agent_type", "operation"})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	attribution, ok := h.agentAttributionFromRequest(w, r)
	if !ok {
		return
	}
	opts.Attribution = attribution
	if opts.GroupBy == "" {
		opts.GroupBy = "operation"
	}
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.BreakdownResponse{GroupBy: opts.GroupBy, Limit: opts.Limit})
		return
	}
	resp, err := h.usageQuery.ACPBreakdown(opts)
	writeMetricResult(w, resp, err)
}

func (h *Handler) handleACPMetricsSummary(w http.ResponseWriter, r *http.Request) {
	opts, err := metricBreakdownOptions(r, []string{"route_id", "route_protocol", "agent_type", "operation"})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.BreakdownResponse{GroupBy: opts.GroupBy, Limit: opts.Limit})
		return
	}
	resp, err := h.usageQuery.ACPSummary(opts)
	writeMetricResult(w, resp, err)
}

func (h *Handler) handleListMetricInteractions(w http.ResponseWriter, r *http.Request) {
	opts, err := metricEventListOptions(r, []string{
		"route_kind", "route_protocol", "route_id", "virtual_key_id", "trace_id", "parent_span_id", "agent_depth",
		"service_id", "session_id",
	})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	// agent_id resolves to the agent's full attribution (durable tag OR owned
	// routes/ACP service), matching the per-agent interactions/usage reads — not a
	// literal tag filter, so untagged-but-mappable spans still surface.
	attribution, ok := h.agentAttributionFromRequest(w, r)
	if !ok {
		return
	}
	opts.Attribution = attribution
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.EventListResponse{Limit: opts.Limit})
		return
	}
	resp, err := h.usageQuery.ListInteractions(opts)
	writeMetricResult(w, resp, err)
}

func (h *Handler) handleMetricInteractionsSummary(w http.ResponseWriter, r *http.Request) {
	opts, err := metricBreakdownOptions(r, []string{"route_kind", "route_protocol", "route_id", "virtual_key_id", "runtime_type", "service_id", "session_id", "run_id"})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	attribution, ok := h.agentAttributionFromRequest(w, r)
	if !ok {
		return
	}
	opts.Attribution = attribution
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.BreakdownResponse{GroupBy: opts.GroupBy, Limit: opts.Limit})
		return
	}
	resp, err := h.usageQuery.InteractionsSummary(opts)
	writeMetricResult(w, resp, err)
}

func (h *Handler) handleMetricInteractionsTimeseries(w http.ResponseWriter, r *http.Request) {
	opts, err := metricTimeseriesOptions(r, []string{
		"route_kind", "route_protocol", "route_id", "virtual_key_id", "runtime_type", "service_id", "session_id", "run_id",
	})
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	attribution, ok := h.agentAttributionFromRequest(w, r)
	if !ok {
		return
	}
	opts.Attribution = attribution
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.SeriesResponse{Bucket: opts.Bucket, GroupBy: opts.GroupBy})
		return
	}
	resp, err := h.usageQuery.InteractionsTimeseries(opts)
	writeMetricResult(w, resp, err)
}

func (h *Handler) handleListMetricsEvents(w http.ResponseWriter, r *http.Request, kind string, filterKeys []string) {
	opts, err := metricEventListOptions(r, filterKeys)
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	attribution, ok := h.agentAttributionFromRequest(w, r)
	if !ok {
		return
	}
	opts.Attribution = attribution
	if h.usageQuery == nil {
		_ = httpjson.Write(w, http.StatusOK, usage.EventListResponse{Limit: opts.Limit})
		return
	}
	resp, err := h.usageQuery.ListEvents(kind, opts)
	writeMetricResult(w, resp, err)
}

// writeMetricResult preserves concrete result DTOs and the shared query-error
// status, without erasing protocol-specific query or filter contracts.
func writeMetricResult[T any](w http.ResponseWriter, result T, err error) {
	if err != nil {
		_ = httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = httpjson.Write(w, http.StatusOK, result)
}
