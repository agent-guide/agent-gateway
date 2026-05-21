package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestBeginRequestAppendsHistoryOnSuccess(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	_, finish := r.BeginRequest(context.Background(), "route-1", "req-1", "tools/call", nil)
	finish(nil)

	history := r.ListHistory("")
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	h := history[0]
	if h.RouteID != "route-1" {
		t.Fatalf("unexpected RouteID: %q", h.RouteID)
	}
	if h.Method != "tools/call" {
		t.Fatalf("unexpected Method: %q", h.Method)
	}
	if h.Cancelled {
		t.Fatal("expected Cancelled=false for successful request")
	}
	if h.Error != "" {
		t.Fatalf("expected no error, got %q", h.Error)
	}
	if h.CompletedAt.IsZero() {
		t.Fatal("expected CompletedAt to be set")
	}
}

func TestBeginRequestRecordsUpstreamError(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	_, finish := r.BeginRequest(context.Background(), "route-1", "req-2", "tools/call", nil)
	finish(errors.New("upstream timeout"))

	history := r.ListHistory("")
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Error != "upstream timeout" {
		t.Fatalf("unexpected Error: %q", history[0].Error)
	}
}

func TestBeginRequestRecordsCancellation(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	_, finish := r.BeginRequest(context.Background(), "route-1", "req-3", "tools/call", nil)
	_, _ = r.Cancel("route-1", "req-3", "user cancelled")
	finish(context.Canceled)

	history := r.ListHistory("")
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	h := history[0]
	if !h.Cancelled {
		t.Fatal("expected Cancelled=true")
	}
	if h.CancelReason != "user cancelled" {
		t.Fatalf("unexpected CancelReason: %q", h.CancelReason)
	}
	if h.Error != "" {
		t.Fatalf("expected no Error for cancelled request, got %q", h.Error)
	}
}

func TestListHistoryFiltersByRouteID(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	for _, pair := range [][2]string{
		{"route-a", "req-1"},
		{"route-b", "req-2"},
		{"route-a", "req-3"},
	} {
		_, finish := r.BeginRequest(context.Background(), pair[0], pair[1], "ping", nil)
		finish(nil)
	}

	all := r.ListHistory("")
	if len(all) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(all))
	}
	filtered := r.ListHistory("route-a")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered entries, got %d", len(filtered))
	}
	for _, h := range filtered {
		if h.RouteID != "route-a" {
			t.Fatalf("unexpected RouteID in filtered results: %q", h.RouteID)
		}
	}
}

func TestHistoryRingBufferWrapsAtCapacity(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	// fill beyond capacity
	for i := 0; i < historyCapacity+10; i++ {
		_, finish := r.BeginRequest(context.Background(), "route", i, "ping", nil)
		finish(nil)
	}

	history := r.ListHistory("")
	if len(history) != historyCapacity {
		t.Fatalf("expected %d history entries, got %d", historyCapacity, len(history))
	}
	// oldest entry should have requestID 10 (first 10 were evicted)
	if history[0].RequestID != 10 {
		t.Fatalf("expected oldest RequestID=10, got %v", history[0].RequestID)
	}
	// newest entry should have requestID historyCapacity+9
	if history[historyCapacity-1].RequestID != historyCapacity+9 {
		t.Fatalf("expected newest RequestID=%d, got %v", historyCapacity+9, history[historyCapacity-1].RequestID)
	}
}

func TestNilRequestIDSkipsHistory(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	_, finish := r.BeginRequest(context.Background(), "route-1", nil, "tools/call", nil)
	finish(nil)

	history := r.ListHistory("")
	if len(history) != 0 {
		t.Fatalf("expected no history for nil requestID, got %d", len(history))
	}
}
