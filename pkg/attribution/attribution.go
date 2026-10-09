// Package attribution transports bounded trusted identity and runtime correlation.
// Value, Policy, NewPolicy, Policy.Validate, WithContext, FromContext, WithCorrelation, Producer
// and Error are candidate protected surfaces. This package performs no authentication.
package attribution

import (
	"context"
	"maps"
	"strings"
	"unicode/utf8"
)

type Value struct {
	Subject      string            `json:"subject,omitempty"`
	Tenant       string            `json:"tenant,omitempty"`
	RouteID      string            `json:"route_id,omitempty"`
	AgentID      string            `json:"agent_id,omitempty"`
	RunID        string            `json:"run_id,omitempty"`
	VirtualKeyID string            `json:"virtual_key_id,omitempty"`
	TraceID      string            `json:"trace_id,omitempty"`
	SpanID       string            `json:"span_id,omitempty"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

// Producer is explicitly injected by a trusted distribution, never chosen by a header.
// It may establish subject/tenant and allowlisted attributes; core correlation wins.
type Producer func(context.Context) (Value, error)

type Error struct{ Field string }

func (e *Error) Error() string { return "invalid_attribution: " + e.Field }

type Policy struct{ allowed map[string]bool }

// NewPolicy requires exact attribute keys owned by an explicitly linked module.
func NewPolicy(modules, keys []string) (Policy, error) {
	p := Policy{allowed: map[string]bool{}}
	linked := map[string]bool{}
	for _, m := range modules {
		linked[m] = true
	}
	for _, k := range keys {
		owner, suffix, ok := strings.Cut(k, "/")
		if !ok || !linked[owner] || suffix == "" || len(k) > 128 || !utf8.ValidString(k) || p.allowed[k] {
			return Policy{}, &Error{Field: "attribute_key"}
		}
		p.allowed[k] = true
	}
	return p, nil
}
func (p Policy) Validate(v Value) error {
	fields := []struct{ name, value string }{{"subject", v.Subject}, {"tenant", v.Tenant}, {"route_id", v.RouteID}, {"agent_id", v.AgentID}, {"run_id", v.RunID}, {"virtual_key_id", v.VirtualKeyID}, {"trace_id", v.TraceID}, {"span_id", v.SpanID}, {"parent_span_id", v.ParentSpanID}}
	for _, f := range fields {
		if len(f.value) > 256 || !utf8.ValidString(f.value) {
			return &Error{Field: f.name}
		}
	}
	for _, f := range []struct {
		name, value string
		length      int
	}{{"trace_id", v.TraceID, 32}, {"span_id", v.SpanID, 16}, {"parent_span_id", v.ParentSpanID, 16}} {
		if f.value != "" && !validID(f.value, f.length) {
			return &Error{Field: f.name}
		}
	}
	if len(v.Attributes) > 16 {
		return &Error{Field: "attributes"}
	}
	total := 0
	for k, x := range v.Attributes {
		if !p.allowed[k] || len(k) > 128 || len(x) > 256 || !utf8.ValidString(x) {
			return &Error{Field: "attributes"}
		}
		total += len(k) + len(x)
	}
	if total > 4096 {
		return &Error{Field: "attributes"}
	}
	return nil
}
func validID(s string, n int) bool {
	if len(s) != n || strings.Trim(s, "0") == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type contextKey struct{}
type entry struct {
	value  Value
	policy Policy
}

func WithContext(ctx context.Context, v Value, p Policy) (context.Context, error) {
	if err := p.Validate(v); err != nil {
		return ctx, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	v.Attributes = maps.Clone(v.Attributes)
	return context.WithValue(ctx, contextKey{}, entry{v, p}), nil
}
func FromContext(ctx context.Context) (Value, bool) {
	if ctx == nil {
		return Value{}, false
	}
	e, ok := ctx.Value(contextKey{}).(entry)
	v := e.value
	v.Attributes = maps.Clone(v.Attributes)
	return v, ok
}

// WithCorrelation replaces runtime-owned correlation while preserving trusted
// subject/tenant and attributes. It uses the policy stored by WithContext.
func WithCorrelation(ctx context.Context, core Value) (context.Context, error) {
	if ctx == nil {
		return ctx, nil
	}
	old, ok := ctx.Value(contextKey{}).(entry)
	if !ok {
		return ctx, nil
	}
	core.Subject = old.value.Subject
	core.Tenant = old.value.Tenant
	core.Attributes = old.value.Attributes
	return WithContext(ctx, core, old.policy)
}
