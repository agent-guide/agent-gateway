// Package caddymgr provides a high-level management layer over the Caddy admin API.
// It translates simple Web UI requests into Caddy's internal JSON config format,
// applies changes via HTTP calls to the Caddy admin endpoint (default localhost:2019),
// and translates Caddy's responses back into the simplified format.
package caddymgr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// allowedPrefixes is the whitelist of Caddy config paths this manager may touch.
// Prevents accidental modification of Caddy's admin, logging, or PKI config.
var allowedPrefixes = []string{
	"/apps/http/servers",
}

// CaddyManager translates Web UI requests into Caddy admin API calls.
// All mutations are applied live to the running Caddy instance via its
// admin HTTP endpoint; no persistent storage is needed because Caddy
// persists config itself when "persist" is enabled in the admin block.
type CaddyManager struct {
	adminAddr string // e.g. "http://localhost:2019"
	client    *http.Client
}

// New creates a CaddyManager that communicates with the Caddy admin API at adminAddr.
// adminAddr defaults to "http://localhost:2019" when empty.
func New(adminAddr string) *CaddyManager {
	if adminAddr == "" {
		adminAddr = "http://localhost:2019"
	}
	return &CaddyManager{
		adminAddr: adminAddr,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// ── Server operations ─────────────────────────────────────────────────────────

// ListServers returns all HTTP servers currently registered in Caddy.
func (m *CaddyManager) ListServers(ctx context.Context) ([]*ServerResponse, error) {
	raw, err := m.caddyGET(ctx, "/apps/http/servers")
	if err != nil {
		return nil, err
	}

	var servers map[string]caddyServer
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, fmt.Errorf("parse caddy servers: %w", err)
	}

	result := make([]*ServerResponse, 0, len(servers))
	for id, srv := range servers {
		s := srv
		result = append(result, m.fromCaddyServer(id, &s))
	}
	return result, nil
}

// GetServer returns the server with the given ID, or ErrNotFound if absent.
func (m *CaddyManager) GetServer(ctx context.Context, id string) (*ServerResponse, error) {
	raw, err := m.caddyGET(ctx, "/apps/http/servers/"+id)
	if err != nil {
		return nil, err
	}
	var srv caddyServer
	if err := json.Unmarshal(raw, &srv); err != nil {
		return nil, fmt.Errorf("parse caddy server: %w", err)
	}
	return m.fromCaddyServer(id, &srv), nil
}

// CreateServer creates a new HTTP server. The server starts with no routes.
func (m *CaddyManager) CreateServer(ctx context.Context, req *ServerRequest) error {
	if req.ID == "" {
		return fmt.Errorf("server id is required")
	}
	if len(req.Listen) == 0 {
		return fmt.Errorf("at least one listen address is required")
	}
	srv := m.toCaddyServer(req, nil)
	return m.caddyPUT(ctx, "/apps/http/servers/"+req.ID, srv)
}

// UpdateServer updates the listen addresses and TLS config of an existing server
// while preserving all existing routes.
func (m *CaddyManager) UpdateServer(ctx context.Context, req *ServerRequest) error {
	if req.ID == "" {
		return fmt.Errorf("server id is required")
	}
	if len(req.Listen) == 0 {
		return fmt.Errorf("at least one listen address is required")
	}
	if err := m.ensureServerMutable(ctx, req.ID); err != nil {
		return err
	}

	// Read raw existing routes so opaque Caddyfile/subroute structure is preserved.
	existingRoutes, err := m.listCaddyRoutes(ctx, req.ID)
	if err != nil {
		return err
	}

	srv := m.toCaddyServer(req, existingRoutes)
	return m.caddyPUT(ctx, "/apps/http/servers/"+req.ID, srv)
}

// DeleteServer removes the HTTP server with the given ID from Caddy.
func (m *CaddyManager) DeleteServer(ctx context.Context, id string) error {
	if err := m.ensureServerMutable(ctx, id); err != nil {
		return err
	}
	return m.caddyDELETE(ctx, "/apps/http/servers/"+id)
}

// ── Route operations ──────────────────────────────────────────────────────────

// ListRoutes returns all routes for the given server.
// Routes defined in the Caddyfile will have an empty ID field.
func (m *CaddyManager) ListRoutes(ctx context.Context, serverID string) ([]*RouteResponse, error) {
	raw, err := m.caddyGET(ctx, "/apps/http/servers/"+serverID+"/routes")
	if err != nil {
		return nil, err
	}
	var routes []caddyRoute
	if err := json.Unmarshal(raw, &routes); err != nil {
		return nil, fmt.Errorf("parse caddy routes: %w", err)
	}
	result := make([]*RouteResponse, 0, len(routes))
	for i, r := range routes {
		rr := m.fromCaddyRoute(i, &r)
		result = append(result, rr)
	}
	return result, nil
}

// AddRoute inserts a new route into the server at the position given by req.Order.
// req.ID must be non-empty and not already present in the server.
func (m *CaddyManager) AddRoute(ctx context.Context, serverID string, req *RouteRequest) error {
	if req.ID == "" {
		return fmt.Errorf("route id is required")
	}
	if err := m.ensureServerMutable(ctx, serverID); err != nil {
		return err
	}

	existing, err := m.listCaddyRoutes(ctx, serverID)
	if err != nil {
		return err
	}

	// Reject duplicate IDs (only check managed routes with a group set).
	for _, r := range existing {
		if r.Group == req.ID {
			return fmt.Errorf("route %q already exists in server %q", req.ID, serverID)
		}
	}

	newRoute := m.toCaddyRoute(req)

	// Insert at req.Order, clamped to [0, len(existing)].
	pos := req.Order
	if pos < 0 {
		pos = 0
	}
	if pos > len(existing) {
		pos = len(existing)
	}
	updated := make([]caddyRoute, 0, len(existing)+1)
	updated = append(updated, existing[:pos]...)
	updated = append(updated, newRoute)
	updated = append(updated, existing[pos:]...)

	return m.caddyPUT(ctx, "/apps/http/servers/"+serverID+"/routes", updated)
}

// UpdateRoute replaces the handler and match config of the route identified by routeID.
// The route keeps its current position in the array.
func (m *CaddyManager) UpdateRoute(ctx context.Context, serverID, routeID string, req *RouteRequest) error {
	if err := m.ensureServerMutable(ctx, serverID); err != nil {
		return err
	}

	existing, err := m.listCaddyRoutes(ctx, serverID)
	if err != nil {
		return err
	}

	found := false
	for i, r := range existing {
		if r.Group == routeID {
			existing[i] = m.toCaddyRoute(req)
			existing[i].Group = routeID // keep original ID
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("route %q not found in server %q: %w", routeID, serverID, ErrNotFound)
	}

	return m.caddyPUT(ctx, "/apps/http/servers/"+serverID+"/routes", existing)
}

// DeleteRoute removes the route identified by routeID from the server.
func (m *CaddyManager) DeleteRoute(ctx context.Context, serverID, routeID string) error {
	if err := m.ensureServerMutable(ctx, serverID); err != nil {
		return err
	}

	existing, err := m.listCaddyRoutes(ctx, serverID)
	if err != nil {
		return err
	}

	filtered := existing[:0]
	found := false
	for _, r := range existing {
		if r.Group == routeID {
			found = true
			continue
		}
		filtered = append(filtered, r)
	}
	if !found {
		return fmt.Errorf("route %q not found in server %q: %w", routeID, serverID, ErrNotFound)
	}

	return m.caddyPUT(ctx, "/apps/http/servers/"+serverID+"/routes", filtered)
}

// ── Caddy internal JSON types ─────────────────────────────────────────────────

// caddyServer mirrors the relevant fields of Caddy's http.Server JSON.
type caddyServer struct {
	Listen []string     `json:"listen"`
	Routes []caddyRoute `json:"routes,omitempty"`
	TLS    *caddyTLS    `json:"tls,omitempty"`
}

// caddyRoute mirrors the relevant fields of Caddy's http.Route JSON.
// We use the Group field as our logical route ID.
type caddyRoute struct {
	Group    string         `json:"group,omitempty"`
	Match    []caddyMatch   `json:"match,omitempty"`
	Handle   []caddyHandler `json:"handle"`
	Terminal bool           `json:"terminal,omitempty"`
}

type caddyMatch struct {
	Path []string `json:"path,omitempty"`
	Host []string `json:"host,omitempty"`
}

// caddyHandler is a dynamic map because Caddy handler JSON varies by type.
type caddyHandler map[string]any

type caddyTLS struct {
	Automation *caddyTLSAutomation `json:"automation,omitempty"`
}

type caddyTLSAutomation struct {
	Policies []map[string]any `json:"policies"`
}

// ── Translation: Web UI ↔ Caddy JSON ─────────────────────────────────────────

func (m *CaddyManager) toCaddyServer(req *ServerRequest, routes []caddyRoute) caddyServer {
	srv := caddyServer{
		Listen: req.Listen,
		Routes: routes,
	}
	if req.TLS != nil {
		if req.TLS.Auto {
			srv.TLS = &caddyTLS{
				Automation: &caddyTLSAutomation{
					Policies: []map[string]any{{}},
				},
			}
		}
		// Manual cert/key TLS is managed via Caddy's tls app; skip here.
	}
	return srv
}

func (m *CaddyManager) toCaddyRoute(req *RouteRequest) caddyRoute {
	r := caddyRoute{
		Group:    req.ID,
		Terminal: true,
		Handle:   []caddyHandler{m.toCaddyHandler(req.Handler)},
	}
	if len(req.Match.Paths) > 0 || len(req.Match.Hosts) > 0 {
		r.Match = []caddyMatch{{
			Path: req.Match.Paths,
			Host: req.Match.Hosts,
		}}
	}
	return r
}

// toCaddyHandler translates our simple HandlerConf into Caddy's handler JSON.
func (m *CaddyManager) toCaddyHandler(h HandlerConf) caddyHandler {
	switch h.Type {
	case "openai":
		return caddyHandler{
			"handler":  "openai",
			"route_id": h.RouteID,
		}
	case "anthropic":
		return caddyHandler{
			"handler":  "anthropic",
			"route_id": h.RouteID,
		}
	case "admin":
		return caddyHandler{
			"handler": "agent_gateway_admin",
		}
	case "reverse_proxy":
		return caddyHandler{
			"handler": "reverse_proxy",
			"upstreams": []map[string]any{
				{"dial": h.Upstream},
			},
		}
	case "file_server":
		return caddyHandler{
			"handler": "file_server",
			"root":    h.Root,
		}
	default:
		return caddyHandler{"handler": h.Type}
	}
}

func (m *CaddyManager) fromCaddyServer(id string, srv *caddyServer) *ServerResponse {
	readonly := m.isProtectedServer(srv)
	resp := &ServerResponse{
		ID:       id,
		Listen:   srv.Listen,
		ReadOnly: readonly,
	}
	if readonly {
		resp.Source = "system"
		resp.PublicURL = deriveServerPublicURL(srv.Listen, srv.TLS != nil)
	}
	for i, r := range srv.Routes {
		rr := r
		resp.Routes = append(resp.Routes, *m.fromCaddyRoute(i, &rr))
	}
	return resp
}

func (m *CaddyManager) fromCaddyRoute(idx int, r *caddyRoute) *RouteResponse {
	resp := &RouteResponse{
		ID:    r.Group,
		Order: idx,
	}
	resp.Match = m.extractMatchFromRoute(*r)
	resp.Handlers = m.extractHandlersFromRoute(*r)
	return resp
}

func (m *CaddyManager) extractMatchFromRoute(r caddyRoute) MatchConf {
	var match MatchConf
	for _, rm := range r.Match {
		match.Paths = appendUnique(match.Paths, rm.Path...)
		match.Hosts = appendUnique(match.Hosts, rm.Host...)
	}
	for _, h := range r.Handle {
		nested := m.extractMatchFromHandler(h)
		match.Paths = appendUnique(match.Paths, nested.Paths...)
		match.Hosts = appendUnique(match.Hosts, nested.Hosts...)
	}
	return match
}

func (m *CaddyManager) extractMatchFromHandler(h caddyHandler) MatchConf {
	handlerType, _ := h["handler"].(string)
	if handlerType != "subroute" {
		return MatchConf{}
	}

	rawRoutes, ok := h["routes"].([]any)
	if !ok {
		return MatchConf{}
	}

	var match MatchConf
	for _, rawRoute := range rawRoutes {
		routeMap, ok := rawRoute.(map[string]any)
		if !ok {
			continue
		}

		data, err := json.Marshal(routeMap)
		if err != nil {
			continue
		}

		var nested caddyRoute
		if err := json.Unmarshal(data, &nested); err != nil {
			continue
		}

		nestedMatch := m.extractMatchFromRoute(nested)
		match.Paths = appendUnique(match.Paths, nestedMatch.Paths...)
		match.Hosts = appendUnique(match.Hosts, nestedMatch.Hosts...)
	}

	return match
}

func (m *CaddyManager) extractHandlersFromRoute(r caddyRoute) []HandlerConf {
	var handlers []HandlerConf
	for _, h := range r.Handle {
		handlers = append(handlers, m.extractHandlersFromHandler(h)...)
	}
	return handlers
}

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, v := range dst {
		seen[v] = struct{}{}
	}
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		dst = append(dst, v)
		seen[v] = struct{}{}
	}
	return dst
}

func (m *CaddyManager) extractHandlersFromHandler(h caddyHandler) []HandlerConf {
	handlerType, _ := h["handler"].(string)
	if handlerType != "subroute" {
		return []HandlerConf{m.fromCaddyHandler(h)}
	}

	rawRoutes, ok := h["routes"].([]any)
	if !ok {
		return nil
	}

	var handlers []HandlerConf
	for _, rawRoute := range rawRoutes {
		routeMap, ok := rawRoute.(map[string]any)
		if !ok {
			continue
		}

		data, err := json.Marshal(routeMap)
		if err != nil {
			continue
		}

		var nested caddyRoute
		if err := json.Unmarshal(data, &nested); err != nil {
			continue
		}
		handlers = append(handlers, m.extractHandlersFromRoute(nested)...)
	}

	return handlers
}

// fromCaddyHandler translates Caddy's dynamic handler map back to our HandlerConf.
func (m *CaddyManager) fromCaddyHandler(h caddyHandler) HandlerConf {
	handlerType, _ := h["handler"].(string)
	switch handlerType {
	case "openai", "anthropic":
		routeID, _ := h["route_id"].(string)
		return HandlerConf{Type: handlerType, RouteID: routeID}
	case "agent_gateway_admin":
		return HandlerConf{Type: "admin"}
	case "reverse_proxy":
		upstream := ""
		if ups, ok := h["upstreams"].([]any); ok && len(ups) > 0 {
			if up, ok := ups[0].(map[string]any); ok {
				upstream, _ = up["dial"].(string)
			}
		}
		return HandlerConf{Type: "reverse_proxy", Upstream: upstream}
	case "file_server":
		root, _ := h["root"].(string)
		return HandlerConf{Type: "file_server", Root: root}
	default:
		return HandlerConf{Type: handlerType}
	}
}

func (m *CaddyManager) ensureServerMutable(ctx context.Context, serverID string) error {
	srv, err := m.getRawServer(ctx, serverID)
	if err != nil {
		return err
	}
	if m.isProtectedServer(srv) {
		return fmt.Errorf("server %q is managed by system config and is read-only: %w", serverID, ErrReadOnly)
	}
	return nil
}

// listCaddyRoutes returns the raw caddyRoute slice for a server.
func (m *CaddyManager) listCaddyRoutes(ctx context.Context, serverID string) ([]caddyRoute, error) {
	srv, err := m.getRawServer(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return srv.Routes, nil
}

func (m *CaddyManager) getRawServer(ctx context.Context, serverID string) (*caddyServer, error) {
	raw, err := m.caddyGET(ctx, "/apps/http/servers/"+serverID)
	if err != nil {
		return nil, err
	}
	var srv caddyServer
	if err := json.Unmarshal(raw, &srv); err != nil {
		return nil, fmt.Errorf("parse caddy server: %w", err)
	}
	return &srv, nil
}

func (m *CaddyManager) isProtectedServer(srv *caddyServer) bool {
	if srv == nil {
		return false
	}
	for _, route := range srv.Routes {
		if routeContainsAdminHandler(route) {
			return true
		}
	}
	return false
}

func routeContainsAdminHandler(route caddyRoute) bool {
	for _, handler := range route.Handle {
		if handlerContainsAdmin(handler) {
			return true
		}
	}
	return false
}

func handlerContainsAdmin(h caddyHandler) bool {
	handlerType, _ := h["handler"].(string)
	if handlerType == "agent_gateway_admin" {
		return true
	}
	if handlerType != "subroute" {
		return false
	}

	rawRoutes, ok := h["routes"].([]any)
	if !ok {
		return false
	}
	for _, rawRoute := range rawRoutes {
		routeMap, ok := rawRoute.(map[string]any)
		if !ok {
			continue
		}
		data, err := json.Marshal(routeMap)
		if err != nil {
			continue
		}
		var nested caddyRoute
		if err := json.Unmarshal(data, &nested); err != nil {
			continue
		}
		if routeContainsAdminHandler(nested) {
			return true
		}
	}
	return false
}

func deriveServerPublicURL(listen []string, hasTLS bool) string {
	if len(listen) == 0 {
		return ""
	}
	addr := normalizeListenAddress(listen[0])
	if addr == "" {
		return ""
	}
	scheme := "http"
	if hasTLS {
		scheme = "https"
	}
	u := url.URL{Scheme: scheme, Host: addr, Path: "/"}
	return u.String()
}

func normalizeListenAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

// ── Caddy admin HTTP client ───────────────────────────────────────────────────

func (m *CaddyManager) caddyGET(ctx context.Context, path string) (json.RawMessage, error) {
	if err := m.checkAllowed(path); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.adminAddr+"/config"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("caddy admin unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read caddy response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("caddy admin error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (m *CaddyManager) caddyPUT(ctx context.Context, path string, val any) error {
	if err := m.checkAllowed(path); err != nil {
		return err
	}
	data, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("marshal caddy payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, m.adminAddr+"/config"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("caddy admin unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy admin error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (m *CaddyManager) caddyDELETE(ctx context.Context, path string) error {
	if err := m.checkAllowed(path); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, m.adminAddr+"/config"+path, nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("caddy admin unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy admin error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// checkAllowed rejects any config path not in the allowedPrefixes whitelist.
func (m *CaddyManager) checkAllowed(path string) error {
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return nil
		}
	}
	return fmt.Errorf("config path %q is not allowed", path)
}
