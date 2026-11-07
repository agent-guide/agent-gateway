package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/observability/usage"
)

func metricEventListOptions(r *http.Request, filterKeys []string) (usage.EventListOptions, error) {
	q := r.URL.Query()
	limit, err := metricLimit(q.Get("limit"))
	if err != nil {
		return usage.EventListOptions{}, err
	}
	var success *bool
	if raw := strings.TrimSpace(q.Get("success")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return usage.EventListOptions{}, fmt.Errorf("success must be a boolean")
		}
		success = &parsed
	}
	filters := metricFilters(q, filterKeys)
	return usage.EventListOptions{
		From:    strings.TrimSpace(q.Get("from")),
		To:      strings.TrimSpace(q.Get("to")),
		Limit:   limit,
		Filters: filters,
		Success: success,
	}, nil
}

func metricTimeseriesOptions(r *http.Request, filterKeys []string) (usage.TimeseriesOptions, error) {
	q := r.URL.Query()
	bucket, _, err := usage.ResolveBucket(q.Get("bucket"))
	if err != nil {
		return usage.TimeseriesOptions{}, err
	}
	filters := metricFilters(q, filterKeys)
	return usage.TimeseriesOptions{
		From:    strings.TrimSpace(q.Get("from")),
		To:      strings.TrimSpace(q.Get("to")),
		Bucket:  bucket,
		GroupBy: strings.TrimSpace(q.Get("group_by")),
		Filters: filters,
	}, nil
}

func metricBreakdownOptions(r *http.Request, filterKeys []string) (usage.BreakdownOptions, error) {
	q := r.URL.Query()
	limit, err := metricLimit(q.Get("limit"))
	if err != nil {
		return usage.BreakdownOptions{}, err
	}
	filters := metricFilters(q, filterKeys)
	return usage.BreakdownOptions{
		From:    strings.TrimSpace(q.Get("from")),
		To:      strings.TrimSpace(q.Get("to")),
		GroupBy: strings.TrimSpace(q.Get("group_by")),
		OrderBy: strings.TrimSpace(q.Get("order_by")),
		Limit:   limit,
		Filters: filters,
	}, nil
}

func metricSummaryOptions(r *http.Request, filterKeys []string) (usage.SummaryOptions, error) {
	q := r.URL.Query()
	limit, err := metricLimit(q.Get("limit"))
	if err != nil {
		return usage.SummaryOptions{}, err
	}
	filters := metricFilters(q, filterKeys)
	return usage.SummaryOptions{
		From:    strings.TrimSpace(q.Get("from")),
		To:      strings.TrimSpace(q.Get("to")),
		Limit:   limit,
		Filters: filters,
	}, nil
}

func metricLimit(raw string) (int, error) {
	limit := 100
	if raw = strings.TrimSpace(raw); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("limit must be an integer")
		}
		limit = parsed
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return limit, nil
}

// metricFilters applies each endpoint's explicit allowlist; unsupported dimensions
// never become an untyped pass-through to persistence.
func metricFilters(q url.Values, keys []string) map[string]string {
	filters := map[string]string{}
	for _, key := range keys {
		if value := strings.TrimSpace(q.Get(key)); value != "" {
			filters[key] = value
		}
	}
	return filters
}
