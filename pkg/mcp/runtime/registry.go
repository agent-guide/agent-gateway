package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type Registry struct {
	mu         sync.Mutex
	inFlight   map[string]*InFlightRequest
	progresses map[string]ProgressNotification
}

type InFlightRequest struct {
	RouteID          string    `json:"route_id"`
	RequestID        any       `json:"request_id"`
	RequestKey       string    `json:"request_key"`
	Method           string    `json:"method"`
	ProgressToken    any       `json:"progress_token,omitempty"`
	ProgressTokenKey string    `json:"progress_token_key,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	CancelledAt      time.Time `json:"cancelled_at,omitempty"`
	CancelReason     string    `json:"cancel_reason,omitempty"`
	cancel           context.CancelFunc
}

type ProgressNotification struct {
	RouteID          string    `json:"route_id"`
	ProgressToken    any       `json:"progress_token"`
	ProgressTokenKey string    `json:"progress_token_key"`
	RequestID        any       `json:"request_id,omitempty"`
	RequestKey       string    `json:"request_key,omitempty"`
	Progress         float64   `json:"progress"`
	Total            *float64  `json:"total,omitempty"`
	Message          string    `json:"message,omitempty"`
	LastMethod       string    `json:"last_method,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func NewRegistry() *Registry {
	return &Registry{
		inFlight:   make(map[string]*InFlightRequest),
		progresses: make(map[string]ProgressNotification),
	}
}

func (r *Registry) BeginRequest(parent context.Context, routeID string, requestID any, method string, progressToken any) (context.Context, func()) {
	if requestID == nil {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	requestKey := RouteRequestKey(routeID, requestID)
	entry := &InFlightRequest{
		RouteID:       routeID,
		RequestID:     requestID,
		RequestKey:    requestKey,
		Method:        method,
		ProgressToken: progressToken,
		StartedAt:     time.Now().UTC(),
		cancel:        cancel,
	}
	if progressToken != nil {
		entry.ProgressTokenKey = RouteProgressTokenKey(routeID, progressToken)
	}

	r.mu.Lock()
	if r.inFlight == nil {
		r.inFlight = make(map[string]*InFlightRequest)
	}
	r.inFlight[requestKey] = entry
	r.mu.Unlock()

	return ctx, func() {
		cancel()
		r.mu.Lock()
		delete(r.inFlight, requestKey)
		r.mu.Unlock()
	}
}

func (r *Registry) Cancel(routeID string, requestID any, reason string) (bool, error) {
	requestKey := RouteRequestKey(routeID, requestID)
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := r.inFlight[requestKey]
	if entry == nil {
		return false, nil
	}
	if entry.Method == "initialize" {
		return false, fmt.Errorf("initialize requests cannot be cancelled")
	}
	entry.CancelReason = reason
	entry.CancelledAt = time.Now().UTC()
	if entry.cancel != nil {
		entry.cancel()
	}
	return true, nil
}

func (r *Registry) CancelReason(routeID string, requestID any) string {
	requestKey := RouteRequestKey(routeID, requestID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry := r.inFlight[requestKey]; entry != nil {
		return entry.CancelReason
	}
	return ""
}

func (r *Registry) StoreProgress(routeID string, progressToken any, progress float64, total *float64, message string) {
	tokenKey := RouteProgressTokenKey(routeID, progressToken)

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.progresses == nil {
		r.progresses = make(map[string]ProgressNotification)
	}

	record := ProgressNotification{
		RouteID:          routeID,
		ProgressToken:    progressToken,
		ProgressTokenKey: tokenKey,
		Progress:         progress,
		Total:            total,
		Message:          message,
		UpdatedAt:        time.Now().UTC(),
	}
	for _, entry := range r.inFlight {
		if entry != nil && entry.ProgressTokenKey == tokenKey {
			record.RequestID = entry.RequestID
			record.RequestKey = entry.RequestKey
			record.LastMethod = entry.Method
			break
		}
	}
	r.progresses[tokenKey] = record
}

func (r *Registry) ListInFlight() []InFlightRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]InFlightRequest, 0, len(r.inFlight))
	for _, entry := range r.inFlight {
		if entry == nil {
			continue
		}
		cloned := *entry
		cloned.cancel = nil
		out = append(out, cloned)
	}
	return out
}

func (r *Registry) ListProgress() []ProgressNotification {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ProgressNotification, 0, len(r.progresses))
	for _, record := range r.progresses {
		out = append(out, record)
	}
	return out
}

func RouteRequestKey(routeID string, requestID any) string {
	return routeID + "\x00" + CanonicalValueKey(requestID)
}

func RouteProgressTokenKey(routeID string, token any) string {
	return routeID + "\x00" + CanonicalValueKey(token)
}

func CanonicalValueKey(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%T:%v", value, value)
	}
	return string(data)
}
