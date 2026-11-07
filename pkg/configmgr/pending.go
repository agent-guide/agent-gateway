package configmgr

import (
	"errors"
	"fmt"
	"time"
)

var ErrMutationPending = errors.New("configuration mutation requires recovery")

// PendingMutation contains only bounded operation identity, never a config or
// raw persistence/listener diagnostic. It is safe to expose on Admin views.
type PendingMutation struct {
	OperationID      string         `json:"operation_id"`
	ResourceID       string         `json:"resource_id"`
	Action           MutationAction `json:"action"`
	State            string         `json:"state"`
	StartedAt        time.Time      `json:"started_at"`
	RecoveryAttempts int            `json:"recovery_attempts,omitempty"`
	RetryAfter       time.Time      `json:"retry_after,omitzero"`
}

// BackoffRecovery bounds repeated explicit recovery attempts without adding a
// background worker or replaying configuration writes.
func BackoffRecovery(state PendingMutation, now time.Time) PendingMutation {
	state.RecoveryAttempts = min(state.RecoveryAttempts+1, 32)
	delay := min(time.Second<<min(state.RecoveryAttempts-1, 5), 30*time.Second)
	state.RetryAfter = now.Add(delay)
	return state
}

type PendingMutationError struct {
	Mutation PendingMutation
	Cause    error `json:"-"`
}

func (e *PendingMutationError) Error() string {
	return fmt.Sprintf("%v: resource %q operation %s (%s); inspect pending_mutation and recover before retrying the write", ErrMutationPending, e.Mutation.ResourceID, e.Mutation.OperationID, e.Mutation.Action)
}
func (*PendingMutationError) Unwrap() error { return ErrMutationPending }
