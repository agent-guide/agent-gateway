package localapikey

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/internal/utils"
)

func ValidateForRoute(routeID string, requireLocalAPIKey bool, key *LocalAPIKey) (*LocalAPIKey, error) {
	if key == nil {
		if requireLocalAPIKey {
			return nil, utils.NewHTTPError(http.StatusUnauthorized, "local api key is required")
		}
		return nil, nil
	}
	if key.Disabled {
		return nil, utils.NewHTTPError(http.StatusForbidden, "local api key is disabled")
	}
	if !key.ExpiresAt.IsZero() && key.ExpiresAt.Before(time.Now()) {
		return nil, utils.NewHTTPError(http.StatusForbidden, "local api key is expired")
	}
	if len(key.AllowedRouteIDs) > 0 && !slices.Contains(key.AllowedRouteIDs, routeID) {
		return nil, utils.NewHTTPError(http.StatusForbidden, "local api key is not allowed to access this route")
	}
	return key, nil
}

// ExtractAPIKey extracts the bearer token or x-api-key value from the request.
func ExtractAPIKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	if key := strings.TrimSpace(r.Header.Get("x-api-key")); key != "" {
		return key
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

// AuthenticateRequest extracts, loads, and validates a local API key for a route.
func AuthenticateRequest(ctx context.Context, store configstoreintf.LocalAPIKeyStorer, httpReq *http.Request, routeID string, requireLocalAPIKey bool) (*LocalAPIKey, error) {
	rawKey := ExtractAPIKey(httpReq)
	if rawKey == "" {
		return ValidateForRoute(routeID, requireLocalAPIKey, nil)
	}
	if store == nil {
		return nil, utils.NewHTTPError(http.StatusServiceUnavailable, "local api key store is not configured")
	}

	item, err := store.Get(ctx, rawKey)
	if err != nil {
		return nil, utils.NewHTTPError(http.StatusUnauthorized, "invalid local api key")
	}

	key, ok := item.(*LocalAPIKey)
	if !ok || key == nil {
		return nil, utils.NewHTTPError(http.StatusUnauthorized, "invalid local api key")
	}

	return ValidateForRoute(routeID, requireLocalAPIKey, key)
}
