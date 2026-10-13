// Package event defines sanitized v1 DTOs and bounded sink transport. It is not
// a usage store or a second event authority. Envelope, LimiterAdmission,
// ExtensionMutation, Sink, Publisher, Validate and Error are designated protected
// surfaces. Registration, Options, Stats, Delivery, NewDelivery and
// Delivery.Publish/Flush/Close/Stats plus SchemaVersion and MaxEnvelopeBytes
// are also designated transport surfaces. Released protection requires
// the enterprise extension contract's published-tag and consumer gates.
package event

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/attribution"
)

const MaxEnvelopeBytes = 16 * 1024
const SchemaVersion = 1

type Envelope struct {
	SchemaVersion int                `json:"schema_version"`
	ID            string             `json:"event_id"`
	Family        string             `json:"family"`
	Timestamp     time.Time          `json:"timestamp"`
	Attribution   attribution.Value  `json:"attribution"`
	Outcome       string             `json:"outcome"`
	Reason        string             `json:"reason,omitempty"`
	Limiter       *LimiterAdmission  `json:"limiter_admission,omitempty"`
	Mutation      *ExtensionMutation `json:"extension_mutation,omitempty"`
}
type LimiterAdmission struct {
	Dimension  string        `json:"dimension"`
	RetryAfter time.Duration `json:"retry_after_ns"`
	BackendID  string        `json:"backend_id"`
}
type ExtensionMutation struct {
	ModuleID   string `json:"module_id"`
	Operation  string `json:"operation"`
	StateReset bool   `json:"state_reset,omitempty"`
}
type Sink interface {
	Write(context.Context, Envelope) error
	Flush(context.Context) error
	Close(context.Context) error
}
type Publisher interface {
	Publish(context.Context, Envelope) bool
}
type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }

var moduleID = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+$`)
var eventID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var reasons = map[string]bool{"": true, "rate_limited": true, "limiter_unavailable": true, "canceled": true, "invalid_config": true, "unknown_module": true, "schema_mismatch": true, "missing_dependency": true, "dependency_cycle": true, "prepare_failed": true, "persistence_failed": true, "closed": true, "unhealthy": true, "mutation_failed": true}

func Validate(e Envelope, p attribution.Policy) error {
	invalid := func() error { return &Error{Code: "invalid_event"} }
	if e.SchemaVersion != 1 || !eventID.MatchString(e.ID) || e.Timestamp.IsZero() || e.Timestamp.Location() != time.UTC || !reasons[e.Reason] {
		return invalid()
	}
	if err := p.Validate(e.Attribution); err != nil {
		return invalid()
	}
	switch e.Family {
	case "limiter.admission":
		if e.Limiter == nil || e.Mutation != nil || !(e.Outcome == "allow" || e.Outcome == "deny" || e.Outcome == "degraded") {
			return invalid()
		}
		x := e.Limiter
		if !(x.Dimension == "llm" || x.Dimension == "mcp" || x.Dimension == "agent") || x.RetryAfter < 0 || len(x.BackendID) > 128 || !moduleID.MatchString(x.BackendID) {
			return invalid()
		}
	case "extension.mutation":
		if e.Mutation == nil || e.Limiter != nil || !(e.Outcome == "success" || e.Outcome == "failure") {
			return invalid()
		}
		x := e.Mutation
		if len(x.ModuleID) > 128 || !moduleID.MatchString(x.ModuleID) || !(x.Operation == "put" || x.Operation == "delete" || x.Operation == "reset") {
			return invalid()
		}
	default:
		return invalid()
	}
	b, err := json.Marshal(e)
	if err != nil || len(b) > MaxEnvelopeBytes {
		return &Error{Code: "event_too_large"}
	}
	return nil
}

// Registration is an explicit assembly-owned sink factory. New must honor its
// context and return a fresh sink per server lifecycle (including Caddy reload).
// ModuleID must name a linked descriptor. Callbacks never enter the manifest.
type Registration struct {
	ModuleID    string
	New         func(context.Context) (Sink, error)
	QueueSize   int
	WriteBudget time.Duration
}
