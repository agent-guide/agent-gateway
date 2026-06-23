package routecore

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidRouteID marks a route id that cannot be addressed as a single Admin
// API path segment. It is a client-correctable input error; callers should map
// it to a 400, not a 500.
var ErrInvalidRouteID = errors.New("invalid route id")

// GenerateRouteID builds a deterministic, slash-free route id of the form
// "<prefix>:<service_id>:<path-slug>".
//
// The id is fully predictable from (prefix, serviceID, pathPrefix) so callers
// can compute it by hand and cross-reference a route before it is applied (e.g.
// in a virtual key's allowed_route_ids). prefix is the protocol tag (e.g. "acp"
// or "mcp"). Both prefix and serviceID are assumed slash-free (service ids are
// already constrained to a single Admin API path segment); ValidateRouteID
// guards the final result regardless.
//
// Because no disambiguating suffix is appended, two routes on the same service
// whose path prefixes slugify to the same value collide on id; that collision
// surfaces as a duplicate-id error at create/validate time, at which point the
// operator sets an explicit id.
func GenerateRouteID(prefix, serviceID, pathPrefix string) string {
	return prefix + ":" + serviceID + ":" + slugifyPath(pathPrefix)
}

// slugifyPath renders a path prefix as a lowercase, slash-free slug. Any run of
// characters outside [a-z0-9] collapses to a single "-"; an empty or root path
// becomes "root".
func slugifyPath(path string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(path)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "root"
	}
	return slug
}

// ValidateRouteID rejects route ids that cannot be addressed as a single Admin
// API path segment. Route ids are used verbatim in "/admin/<proto>/routes/{id}",
// so a slash (raw or escaped) would break GET/PUT/DELETE routing.
func ValidateRouteID(id string) error {
	if strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("%w: %q must not contain slash characters", ErrInvalidRouteID, id)
	}
	return nil
}
