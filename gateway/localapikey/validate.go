package localapikey

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

func (key LocalAPIKey) Validate() error {
	if key.Disabled {
		return fmt.Errorf("local api key is disabled")
	}
	if !key.ExpiresAt.IsZero() && key.ExpiresAt.Before(time.Now()) {
		return fmt.Errorf("local api key is expired")
	}

	return nil
}

func (key LocalAPIKey) ValidateForRoute(routeID string) error {
	err := key.Validate()
	if err != nil {
		return err
	}
	if len(key.AllowedRouteIDs) > 0 && !slices.Contains(key.AllowedRouteIDs, routeID) {
		return fmt.Errorf("local api key is not allowed to access this route")
	}
	return nil
}

// func ValidateForRoute(routeID string, requireLocalAPIKey bool, key *LocalAPIKey) (*LocalAPIKey, error) {
// 	if key == nil {
// 		if requireLocalAPIKey {
// 			return nil, statuserr.New(http.StatusUnauthorized, "local api key is required")
// 		}
// 		return nil, nil
// 	}
// 	if key.Disabled {
// 		return nil, statuserr.New(http.StatusForbidden, "local api key is disabled")
// 	}
// 	if !key.ExpiresAt.IsZero() && key.ExpiresAt.Before(time.Now()) {
// 		return nil, statuserr.New(http.StatusForbidden, "local api key is expired")
// 	}
// 	if len(key.AllowedRouteIDs) > 0 && !slices.Contains(key.AllowedRouteIDs, routeID) {
// 		return nil, statuserr.New(http.StatusForbidden, "local api key is not allowed to access this route")
// 	}
// 	return key, nil
// }

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
