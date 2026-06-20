package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/metrics/usage"
)

type agentConfigStore struct {
	mu    sync.RWMutex
	items map[string]*agentpkg.Agent
}

func newAgentConfigStore() *agentConfigStore {
	return &agentConfigStore{items: map[string]*agentpkg.Agent{}}
}

func (s *agentConfigStore) unwrap(obj any) (*agentpkg.Agent, error) {
	if u, ok := obj.(configstore.ObjectUnwrapper); ok {
		obj = u.ConfigStoreObject()
	}
	cfg, ok := obj.(*agentpkg.Agent)
	if !ok {
		return nil, fmt.Errorf("unexpected object type %T", obj)
	}
	return cfg, nil
}

func (s *agentConfigStore) Create(_ context.Context, obj any) error {
	cfg, err := s.unwrap(obj)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *cfg
	s.items[cloned.ID] = &cloned
	return nil
}

func (s *agentConfigStore) Update(_ context.Context, obj any) error {
	cfg, err := s.unwrap(obj)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *cfg
	s.items[cloned.ID] = &cloned
	return nil
}

func (s *agentConfigStore) Delete(_ context.Context, keyParts ...any) error {
	id := fmt.Sprint(keyParts[0])
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return nil
}

func (s *agentConfigStore) Get(_ context.Context, keyParts ...any) (any, error) {
	id := fmt.Sprint(keyParts[0])
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg, ok := s.items[id]
	if !ok {
		return nil, configstore.ErrNotFound
	}
	cloned := *cfg
	return &cloned, nil
}

func (s *agentConfigStore) List(_ context.Context) ([]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]any, 0, len(s.items))
	for _, cfg := range s.items {
		cloned := *cfg
		out = append(out, &cloned)
	}
	return out, nil
}

func (s *agentConfigStore) ListByTag(context.Context, string) ([]any, error) {
	return s.List(context.Background())
}

func (s *agentConfigStore) ListByTagPrefix(context.Context, string) ([]any, error) {
	return s.List(context.Background())
}

func (s *agentConfigStore) GetByIndex(context.Context, string, any) (any, error) {
	return nil, configstore.ErrNotFound
}

type recordingUsageQuery struct {
	opts          usage.EventListOptions
	seriesOpts    usage.TimeseriesOptions
	breakdownOpts usage.BreakdownOptions
	seriesErr     error
}

func (q *recordingUsageQuery) Summary() (usage.Summary, error) { return usage.Summary{}, nil }
func (q *recordingUsageQuery) ListEvents(string, usage.EventListOptions) (usage.EventListResponse, error) {
	return usage.EventListResponse{}, nil
}
func (q *recordingUsageQuery) ListInteractions(opts usage.EventListOptions) (usage.EventListResponse, error) {
	q.opts = opts
	return usage.EventListResponse{Limit: opts.Limit}, nil
}
func (q *recordingUsageQuery) LLMTimeseries(opts usage.TimeseriesOptions) (usage.SeriesResponse, error) {
	q.seriesOpts = opts
	if q.seriesErr != nil {
		return usage.SeriesResponse{}, q.seriesErr
	}
	return usage.SeriesResponse{Bucket: opts.Bucket, GroupBy: opts.GroupBy}, nil
}
func (q *recordingUsageQuery) LLMBreakdown(usage.BreakdownOptions) (usage.BreakdownResponse, error) {
	return usage.BreakdownResponse{}, nil
}
func (q *recordingUsageQuery) MCPTimeseries(usage.TimeseriesOptions) (usage.SeriesResponse, error) {
	return usage.SeriesResponse{}, nil
}
func (q *recordingUsageQuery) MCPBreakdown(usage.BreakdownOptions) (usage.BreakdownResponse, error) {
	return usage.BreakdownResponse{}, nil
}
func (q *recordingUsageQuery) MCPToolsSummary(usage.SummaryOptions) (usage.BreakdownResponse, error) {
	return usage.BreakdownResponse{}, nil
}
func (q *recordingUsageQuery) ACPTimeseries(usage.TimeseriesOptions) (usage.SeriesResponse, error) {
	return usage.SeriesResponse{}, nil
}
func (q *recordingUsageQuery) ACPBreakdown(usage.BreakdownOptions) (usage.BreakdownResponse, error) {
	return usage.BreakdownResponse{}, nil
}
func (q *recordingUsageQuery) ACPSummary(usage.BreakdownOptions) (usage.BreakdownResponse, error) {
	return usage.BreakdownResponse{}, nil
}
func (q *recordingUsageQuery) InteractionsSummary(opts usage.BreakdownOptions) (usage.BreakdownResponse, error) {
	q.breakdownOpts = opts
	return usage.BreakdownResponse{GroupBy: opts.GroupBy, Limit: opts.Limit}, nil
}

func TestAgentInteractionsAllowsDiagnosticFilters(t *testing.T) {
	store := newAgentConfigStore()
	manager := agentpkg.NewManager(store)
	if err := manager.Create(t.Context(), agentpkg.Agent{
		ID:   "coding-agent",
		Name: "Coding Agent",
		Runtime: agentpkg.Runtime{Type: agentpkg.RuntimeTypeACP, ACP: &agentpkg.ACPRuntime{
			ServiceID: "codex-main",
		}},
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	query := &recordingUsageQuery{}
	h := &Handler{agentManager: manager, usageQuery: query}
	req := httptest.NewRequest(http.MethodGet, "/admin/agents/coding-agent/interactions?trace_id=t1&parent_span_id=p1&service_id=codex-main&session_id=sess-1&agent_depth=2", nil)
	req.SetPathValue("id", "coding-agent")
	rec := httptest.NewRecorder()

	h.handleGetAgentInteractions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for key, want := range map[string]string{
		"trace_id":       "t1",
		"parent_span_id": "p1",
		"service_id":     "codex-main",
		"session_id":     "sess-1",
		"agent_depth":    "2",
	} {
		if got := query.opts.Filters[key]; got != want {
			t.Fatalf("filter %s = %q, want %q; filters=%#v", key, got, want, query.opts.Filters)
		}
	}
}

func TestMetricInteractionsAllowsServiceAndSessionFilters(t *testing.T) {
	query := &recordingUsageQuery{}
	h := &Handler{usageQuery: query}
	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/interactions?service_id=codex-main&session_id=sess-1", nil)
	rec := httptest.NewRecorder()

	h.handleListMetricInteractions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if query.opts.Filters["service_id"] != "codex-main" || query.opts.Filters["session_id"] != "sess-1" {
		t.Fatalf("interaction filters = %#v, want service_id and session_id preserved", query.opts.Filters)
	}
}

func TestMetricInteractionsSummaryAllowsServiceAndSessionFilters(t *testing.T) {
	query := &recordingUsageQuery{}
	h := &Handler{usageQuery: query}
	req := httptest.NewRequest(http.MethodGet, "/admin/metrics/interactions/summary?group_by=route_kind&service_id=codex-main&session_id=sess-1", nil)
	rec := httptest.NewRecorder()

	h.handleMetricInteractionsSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if query.breakdownOpts.Filters["service_id"] != "codex-main" || query.breakdownOpts.Filters["session_id"] != "sess-1" {
		t.Fatalf("summary filters = %#v, want service_id and session_id preserved", query.breakdownOpts.Filters)
	}
}

func TestSuccessFromAnyHandlesNormalizedBoolRows(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  bool
	}{
		{true, true},
		{false, false},
		{int64(1), true},
		{int64(0), false},
	} {
		if got := successFromAny(tc.value); got != tc.want {
			b, _ := json.Marshal(tc.value)
			t.Fatalf("successFromAny(%s) = %v, want %v", string(b), got, tc.want)
		}
	}
}

func TestAgentUsageIncludesAttributedLLMTimeseries(t *testing.T) {
	store := newAgentConfigStore()
	manager := agentpkg.NewManager(store)
	if err := manager.Create(t.Context(), agentpkg.Agent{
		ID:   "coding-agent",
		Name: "Coding Agent",
		Runtime: agentpkg.Runtime{Type: agentpkg.RuntimeTypeACP, ACP: &agentpkg.ACPRuntime{
			ServiceID: "codex-main",
		}},
		Routes: agentpkg.Routes{LLMRouteIDs: []string{"llm-route"}},
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	query := &recordingUsageQuery{}
	h := &Handler{agentManager: manager, usageQuery: query}
	req := httptest.NewRequest(http.MethodGet, "/admin/agents/coding-agent/usage?bucket=day", nil)
	req.SetPathValue("id", "coding-agent")
	rec := httptest.NewRecorder()

	h.handleGetAgentUsage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if query.seriesOpts.Bucket != "day" || query.seriesOpts.GroupBy != "route_id" {
		t.Fatalf("series opts = %+v, want bucket=day group_by=route_id", query.seriesOpts)
	}
	if query.seriesOpts.Attribution == nil || query.seriesOpts.Attribution.AgentID != "coding-agent" {
		t.Fatalf("series attribution = %+v, want coding-agent", query.seriesOpts.Attribution)
	}
	if len(query.seriesOpts.Attribution.RouteIDs) != 1 || query.seriesOpts.Attribution.RouteIDs[0] != "llm-route" {
		t.Fatalf("series route fallback = %#v, want llm-route", query.seriesOpts.Attribution.RouteIDs)
	}
}

func TestAgentUsageReturnsBadRequestOnMetricQueryError(t *testing.T) {
	store := newAgentConfigStore()
	manager := agentpkg.NewManager(store)
	if err := manager.Create(t.Context(), agentpkg.Agent{
		ID:   "coding-agent",
		Name: "Coding Agent",
		Runtime: agentpkg.Runtime{Type: agentpkg.RuntimeTypeACP, ACP: &agentpkg.ACPRuntime{
			ServiceID: "codex-main",
		}},
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	h := &Handler{agentManager: manager, usageQuery: &recordingUsageQuery{seriesErr: errors.New("bucket must be hour or day")}}
	req := httptest.NewRequest(http.MethodGet, "/admin/agents/coding-agent/usage?bucket=bad", nil)
	req.SetPathValue("id", "coding-agent")
	rec := httptest.NewRecorder()

	h.handleGetAgentUsage(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", rec.Code, rec.Body.String())
	}
}
