package caddymgr

import "errors"

// ErrNotFound is returned when a Caddy resource does not exist.
var ErrNotFound = errors.New("not found")

// ErrReadOnly is returned when a protected Caddy resource cannot be mutated.
var ErrReadOnly = errors.New("read-only")

// ServerRequest is the Web UI payload for creating or updating an HTTP server.
type ServerRequest struct {
	ID     string   `json:"id"`
	Listen []string `json:"listen"`
	TLS    *TLSConf `json:"tls,omitempty"`
}

// TLSConf describes TLS settings in simplified form.
type TLSConf struct {
	// Auto enables automatic HTTPS via ACME.
	Auto     bool   `json:"auto,omitempty"`
	CertFile string `json:"cert_file,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`
}

// ServerResponse is the Web UI representation of a running HTTP server.
type ServerResponse struct {
	ID        string          `json:"id"`
	Listen    []string        `json:"listen"`
	Routes    []RouteResponse `json:"routes,omitempty"`
	ReadOnly  bool            `json:"readonly,omitempty"`
	Source    string          `json:"source,omitempty"`
	PublicURL string          `json:"public_url,omitempty"`
}

// RouteRequest is the Web UI payload for adding or updating a route within a server.
type RouteRequest struct {
	// ID is a logical identifier stored in Caddy's route group field.
	// Must be non-empty and unique within the server.
	ID string `json:"id"`
	// Order controls insertion position when adding a route (0 = first).
	// Ignored on update; to reorder, delete and re-add.
	Order   int         `json:"order"`
	Match   MatchConf   `json:"match"`
	Handler HandlerConf `json:"handler"`
}

// MatchConf describes which requests a route matches.
type MatchConf struct {
	Paths []string `json:"paths,omitempty"` // e.g. ["/v1/*"]
	Hosts []string `json:"hosts,omitempty"`
}

// HandlerConf describes which handler to mount and its key parameters.
// The Type field selects the handler; other fields are type-specific.
type HandlerConf struct {
	// Type is one of: "openai", "anthropic", "admin", "reverse_proxy", "file_server".
	Type string `json:"type"`
	// RouteID binds an openai/anthropic handler to a gateway route.
	RouteID string `json:"route_id,omitempty"`
	// Upstream is the dial address for a reverse_proxy handler (e.g. "10.0.0.1:8080").
	Upstream string `json:"upstream,omitempty"`
	// Root is the file system root for a file_server handler.
	Root string `json:"root,omitempty"`
}

// RouteResponse is the Web UI representation of a single route inside a server.
// Routes without an ID were defined in the Caddyfile and are read-only.
// Handlers contains all handlers in the route's Handle array in order.
// Web-UI-managed routes have exactly one handler; Caddyfile routes may have more.
type RouteResponse struct {
	// ID is empty for Caddyfile-defined routes.
	ID       string        `json:"id"`
	Order    int           `json:"order"`
	Match    MatchConf     `json:"match"`
	Handlers []HandlerConf `json:"handlers"`
}
