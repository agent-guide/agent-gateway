package scheduler

import (
	"fmt"
	"strings"
	"time"
)

// CredentialSelector picks a credential from a pre-filtered, priority-sorted ReadyBucket.
// Implement this interface to provide a custom selection algorithm.
type CredentialSelector interface {
	PickFromBucket(bucket *ReadyBucket, predicate func(*Credential) bool) *Credential
}

// ReadyBucket holds credentials at one priority level that are ready for selection.
type ReadyBucket struct {
	creds  []*Credential
	cursor int
}

// RoundRobinSelector distributes requests evenly across available credentials.
type RoundRobinSelector struct{}

// FillFirstSelector exhausts one credential before moving to the next, useful
// for staggering rolling-window subscription caps.
type FillFirstSelector struct{}

// PickFromBucket picks the next credential using round-robin within the bucket.
func (s *RoundRobinSelector) PickFromBucket(bucket *ReadyBucket, predicate func(*Credential) bool) *Credential {
	n := len(bucket.creds)
	if n == 0 {
		return nil
	}
	start := bucket.cursor % n
	for offset := 0; offset < n; offset++ {
		index := (start + offset) % n
		cred := bucket.creds[index]
		if predicate != nil && !predicate(cred) {
			continue
		}
		bucket.cursor = index + 1
		return cred
	}
	return nil
}

// PickFromBucket picks the first matching credential in the bucket.
func (s *FillFirstSelector) PickFromBucket(bucket *ReadyBucket, predicate func(*Credential) bool) *Credential {
	for _, cred := range bucket.creds {
		if predicate == nil || predicate(cred) {
			return cred
		}
	}
	return nil
}

type blockReason int

const (
	blockReasonNone     blockReason = iota
	blockReasonCooldown             // quota exhausted with known reset time
	blockReasonDisabled             // intentionally disabled
	blockReasonOther                // temporarily unavailable but not cooldown
)

// isCredentialBlockedForModel reports whether a credential is blocked for the given model.
// Returns (blocked, reason, nextRetry).
func isCredentialBlockedForModel(cred *Credential, model string, now time.Time) (bool, blockReason, time.Time) {
	if cred == nil {
		return true, blockReasonOther, time.Time{}
	}
	if cred.IsDisabled() {
		return true, blockReasonDisabled, time.Time{}
	}

	// Check per-model state first.
	if model != "" && len(cred.ModelStates) > 0 {
		state, ok := cred.ModelStates[model]
		if !ok {
			baseModel := canonicalModelKey(model)
			if baseModel != "" && baseModel != model {
				state, ok = cred.ModelStates[baseModel]
			}
		}
		if ok && state != nil {
			if state.Disabled {
				return true, blockReasonDisabled, time.Time{}
			}
			if state.Unavailable && !state.NextRetryAfter.IsZero() && state.NextRetryAfter.After(now) {
				next := state.NextRetryAfter
				if !state.Quota.NextRecoverAt.IsZero() && state.Quota.NextRecoverAt.After(next) {
					next = state.Quota.NextRecoverAt
				}
				if state.Quota.Exceeded {
					return true, blockReasonCooldown, next
				}
				return true, blockReasonOther, next
			}
			return false, blockReasonNone, time.Time{}
		}
		// No model state entry; fall through to credential-level check.
		return false, blockReasonNone, time.Time{}
	}

	// Credential-level availability check.
	if cred.Unavailable && cred.NextRetryAfter.After(now) {
		next := cred.NextRetryAfter
		if !cred.Quota.NextRecoverAt.IsZero() && cred.Quota.NextRecoverAt.After(next) {
			next = cred.Quota.NextRecoverAt
		}
		if cred.Quota.Exceeded {
			return true, blockReasonCooldown, next
		}
		return true, blockReasonOther, next
	}
	return false, blockReasonNone, time.Time{}
}

// canonicalModelKey strips variant suffixes for consistent model key lookup.
func canonicalModelKey(model string) string {
	model = strings.TrimSpace(model)
	if idx := strings.LastIndexByte(model, ':'); idx > 0 {
		return strings.TrimSpace(model[:idx])
	}
	return model
}

// credentialPriority returns the scheduling priority for a credential.
func credentialPriority(cred *Credential) int {
	if cred == nil {
		return 0
	}
	return cred.Priority()
}

// cooldownError is returned when all credentials for a model are in cooldown.
type cooldownError struct {
	model    string
	provider string
	resetIn  string // formatted duration
}

func (e *cooldownError) Error() string {
	if e == nil {
		return ""
	}
	msg := fmt.Sprintf("all credentials for model %s are cooling down", e.model)
	if e.provider != "" {
		msg += fmt.Sprintf(" via provider %s", e.provider)
	}
	if e.resetIn != "" {
		msg += fmt.Sprintf(", retry after %s", e.resetIn)
	}
	return msg
}

func (e *cooldownError) StatusCode() int {
	return 429
}
