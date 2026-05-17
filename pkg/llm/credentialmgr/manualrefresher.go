package credentialmgr

import (
	"context"
	"strings"
	"time"
)

const (
	MetadataRefreshNameKey        = "refresh_name"
	MetadataRefreshExpiryDeltaKey = "refresh_expiry_delta"
	refreshExpiryLeeway           = 30 * time.Second
)

type ManualRefresher interface {
	Refresh(ctx context.Context, cred *Credential) (*Credential, error)
}

func normalizeManualRefreshName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func credentialNeedsManualRefresh(cred *Credential, now time.Time) bool {
	if cred == nil {
		return false
	}
	expiresAt, ok := cred.ExpirationTime()
	if !ok || expiresAt.IsZero() {
		return false
	}
	delta, ok := cred.RefreshExpiryDelta()
	if !ok {
		return false
	}
	leeway := refreshExpiryLeeway
	if delta >= 0 {
		leeway = delta
	}
	return !expiresAt.After(now.Add(leeway))
}
