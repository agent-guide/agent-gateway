package credentialmgr

import (
	"context"
	"strings"
	"time"
)

const (
	MetadataManualRefreshNameKey     = "manual_refresh_name"
	MetadataManualRefreshExpiryDelta = "manual_refresh_expiry_delta"
	manualRefreshExpiryLeeway        = 30 * time.Second
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
	leeway := manualRefreshExpiryLeeway
	if delta, ok := cred.ManualRefreshExpiryDelta(); ok {
		leeway = delta
	}
	return !expiresAt.After(now.Add(leeway))
}
