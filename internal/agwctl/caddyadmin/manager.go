package caddyadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/internal/agwctl/model"
)

var allowedPrefixes = []string{
	"/apps/http/servers",
}

type Manager struct {
	adminAddr         string
	client            *http.Client
	readOnlyServerIDs map[string]struct{}
}

func NewManager(adminAddr string) *Manager {
	if adminAddr == "" {
		adminAddr = "http://localhost:2019"
	}
	return &Manager{
		adminAddr: strings.TrimRight(adminAddr, "/"),
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (m *Manager) SetReadOnlyServerIDs(ids []string) {
	if len(ids) == 0 {
		m.readOnlyServerIDs = nil
		return
	}
	m.readOnlyServerIDs = make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			m.readOnlyServerIDs[id] = struct{}{}
		}
	}
}

func (m *Manager) ListServers(ctx context.Context) ([]*model.ServerResponse, error) {
	raw, err := m.caddyGET(ctx, "/apps/http/servers")
	if err != nil {
		return nil, err
	}
	var servers map[string]caddyServer
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, fmt.Errorf("parse caddy servers: %w", err)
	}
	result := make([]*model.ServerResponse, 0, len(servers))
	for id, srv := range servers {
		s := srv
		result = append(result, m.fromCaddyServer(id, &s))
	}
	return result, nil
}

func (m *Manager) GetServer(ctx context.Context, id string) (*model.ServerResponse, error) {
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

func (m *Manager) CreateServer(ctx context.Context, req *model.ServerRequest) error {
	if req.ID == "" {
		return fmt.Errorf("server id is required")
	}
	if len(req.Listen) == 0 {
		return fmt.Errorf("at least one listen address is required")
	}
	if m.isReadOnlyServerID(req.ID) {
		return fmt.Errorf("server %q is managed by Caddyfile config and is read-only: %w", req.ID, model.ErrReadOnly)
	}
	if existing, err := m.getRawServer(ctx, req.ID); err == nil {
		if !isEmptyCaddyServer(existing) {
			return fmt.Errorf("server %q already exists: %w", req.ID, model.ErrConflict)
		}
	} else if !errors.Is(err, model.ErrNotFound) {
		return err
	}
	srv := m.toCaddyServer(req, nil)
	return m.caddyPUT(ctx, "/apps/http/servers/"+req.ID, srv)
}

func (m *Manager) UpdateServer(ctx context.Context, req *model.ServerRequest) error {
	if req.ID == "" {
		return fmt.Errorf("server id is required")
	}
	if len(req.Listen) == 0 {
		return fmt.Errorf("at least one listen address is required")
	}
	if err := m.ensureServerMutable(ctx, req.ID); err != nil {
		return err
	}
	existingRoutes, err := m.listCaddyRoutes(ctx, req.ID)
	if err != nil {
		return err
	}
	srv := m.toCaddyServer(req, existingRoutes)
	return m.caddyPUT(ctx, "/apps/http/servers/"+req.ID, srv)
}

func (m *Manager) DeleteServer(ctx context.Context, id string) error {
	if err := m.ensureServerMutable(ctx, id); err != nil {
		return err
	}
	return m.caddyDELETE(ctx, "/apps/http/servers/"+id)
}

func (m *Manager) ListRoutes(ctx context.Context, serverID string) ([]*model.RouteResponse, error) {
	raw, err := m.caddyGET(ctx, "/apps/http/servers/"+serverID+"/routes")
	if err != nil {
		if !errors.Is(err, model.ErrNotFound) {
			return nil, err
		}
		if _, serverErr := m.getRawServer(ctx, serverID); serverErr != nil {
			return nil, serverErr
		}
		return []*model.RouteResponse{}, nil
	}
	var routes []caddyRoute
	if err := json.Unmarshal(raw, &routes); err != nil {
		return nil, fmt.Errorf("parse caddy routes: %w", err)
	}
	result := make([]*model.RouteResponse, 0, len(routes))
	for i, r := range routes {
		rr := r
		result = append(result, m.fromCaddyRoute(i, &rr))
	}
	return result, nil
}

func (m *Manager) AddRoute(ctx context.Context, serverID string, req *model.RouteRequest) error {
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
	for _, r := range existing {
		if r.Group == req.ID {
			return fmt.Errorf("route %q already exists in server %q: %w", req.ID, serverID, model.ErrConflict)
		}
	}
	newRoute := m.toCaddyRoute(req)
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

func (m *Manager) UpdateRoute(ctx context.Context, serverID, routeID string, req *model.RouteRequest) error {
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
			existing[i].Group = routeID
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("route %q not found in server %q: %w", routeID, serverID, model.ErrNotFound)
	}
	return m.caddyPUT(ctx, "/apps/http/servers/"+serverID+"/routes", existing)
}

func (m *Manager) DeleteRoute(ctx context.Context, serverID, routeID string) error {
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
		return fmt.Errorf("route %q not found in server %q: %w", routeID, serverID, model.ErrNotFound)
	}
	return m.caddyPUT(ctx, "/apps/http/servers/"+serverID+"/routes", filtered)
}

type caddyServer struct {
	Listen []string     `json:"listen"`
	Routes []caddyRoute `json:"routes,omitempty"`
	TLS    *caddyTLS    `json:"tls,omitempty"`
}

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

type caddyHandler map[string]any

type caddyTLS struct {
	Automation *caddyTLSAutomation `json:"automation,omitempty"`
}

type caddyTLSAutomation struct {
	Policies []map[string]any `json:"policies"`
}

func (m *Manager) toCaddyServer(req *model.ServerRequest, routes []caddyRoute) caddyServer {
	srv := caddyServer{Listen: req.Listen, Routes: routes}
	if req.TLS != nil && req.TLS.Auto {
		srv.TLS = &caddyTLS{
			Automation: &caddyTLSAutomation{Policies: []map[string]any{{}}},
		}
	}
	return srv
}

func (m *Manager) toCaddyRoute(req *model.RouteRequest) caddyRoute {
	handles := make([]caddyHandler, 0, len(req.Handlers))
	for _, h := range req.Handlers {
		handles = append(handles, m.toCaddyHandler(h))
	}
	r := caddyRoute{Group: req.ID, Terminal: true, Handle: handles}
	if len(req.Match.Paths) > 0 || len(req.Match.Hosts) > 0 {
		r.Match = []caddyMatch{{Path: req.Match.Paths, Host: req.Match.Hosts}}
	}
	return r
}

func (m *Manager) toCaddyHandler(h model.HandlerConf) caddyHandler {
	switch h.Type {
	case "agent_route_dispatcher":
		apiHandlers := map[string]any{}
		for _, apiName := range h.APIs {
			if apiName != "" {
				apiHandlers[apiName] = map[string]any{}
			}
		}
		return caddyHandler{"handler": "agent_route_dispatcher", "api_handlers": apiHandlers}
	case "admin":
		return caddyHandler{"handler": "agent_gateway_admin"}
	case "reverse_proxy":
		return caddyHandler{
			"handler":   "reverse_proxy",
			"upstreams": []map[string]any{{"dial": h.Upstream}},
		}
	case "file_server":
		return caddyHandler{"handler": "file_server", "root": h.Root}
	default:
		return caddyHandler{"handler": h.Type}
	}
}

func (m *Manager) fromCaddyServer(id string, srv *caddyServer) *model.ServerResponse {
	readonly := m.isProtectedServer(id, srv)
	listen := append([]string(nil), srv.Listen...)
	if listen == nil {
		listen = []string{}
	}
	resp := &model.ServerResponse{ID: id, Listen: listen, ReadOnly: readonly}
	if readonly {
		switch {
		case m.isReadOnlyServerID(id):
			resp.Source = "caddyfile"
		case serverHasCaddyfileRoutes(srv):
			resp.Source = "caddyfile"
		default:
			resp.Source = "system"
		}
		resp.PublicURL = deriveServerPublicURL(srv.Listen, srv.TLS != nil)
	}
	for i, r := range srv.Routes {
		rr := r
		resp.Routes = append(resp.Routes, *m.fromCaddyRoute(i, &rr))
	}
	return resp
}

func isEmptyCaddyServer(srv *caddyServer) bool {
	return srv != nil && len(srv.Listen) == 0 && len(srv.Routes) == 0 && srv.TLS == nil
}

func (m *Manager) fromCaddyRoute(idx int, r *caddyRoute) *model.RouteResponse {
	resp := &model.RouteResponse{ID: r.Group, Order: idx}
	resp.Match = m.extractMatchFromRoute(*r)
	resp.Handlers = m.extractHandlersFromRoute(*r)
	return resp
}

func (m *Manager) extractMatchFromRoute(r caddyRoute) model.MatchConf {
	var match model.MatchConf
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

func (m *Manager) extractMatchFromHandler(h caddyHandler) model.MatchConf {
	if handlerType, _ := h["handler"].(string); handlerType != "subroute" {
		return model.MatchConf{}
	}
	rawRoutes, ok := h["routes"].([]any)
	if !ok {
		return model.MatchConf{}
	}
	var match model.MatchConf
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

func (m *Manager) extractHandlersFromRoute(r caddyRoute) []model.HandlerConf {
	var handlers []model.HandlerConf
	for _, h := range r.Handle {
		handlers = append(handlers, m.extractHandlersFromHandler(h)...)
	}
	return handlers
}

func (m *Manager) extractHandlersFromHandler(h caddyHandler) []model.HandlerConf {
	handlerType, _ := h["handler"].(string)
	if handlerType != "subroute" {
		return []model.HandlerConf{m.fromCaddyHandler(h)}
	}
	rawRoutes, ok := h["routes"].([]any)
	if !ok {
		return nil
	}
	var handlers []model.HandlerConf
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

func (m *Manager) fromCaddyHandler(h caddyHandler) model.HandlerConf {
	handlerType, _ := h["handler"].(string)
	switch handlerType {
	case "agent_route_dispatcher":
		var apis []string
		if apiHandlers, ok := h["api_handlers"].(map[string]any); ok {
			for apiName := range apiHandlers {
				apis = append(apis, apiName)
			}
		}
		slices.Sort(apis)
		return model.HandlerConf{Type: "agent_route_dispatcher", APIs: apis}
	case "agent_gateway_admin":
		return model.HandlerConf{Type: "admin"}
	case "reverse_proxy":
		upstream := ""
		if ups, ok := h["upstreams"].([]any); ok && len(ups) > 0 {
			if up, ok := ups[0].(map[string]any); ok {
				upstream, _ = up["dial"].(string)
			}
		}
		return model.HandlerConf{Type: "reverse_proxy", Upstream: upstream}
	case "file_server":
		root, _ := h["root"].(string)
		return model.HandlerConf{Type: "file_server", Root: root}
	default:
		return model.HandlerConf{Type: handlerType}
	}
}

func (m *Manager) ensureServerMutable(ctx context.Context, serverID string) error {
	srv, err := m.getRawServer(ctx, serverID)
	if err != nil {
		return err
	}
	if m.isProtectedServer(serverID, srv) {
		return fmt.Errorf("server %q is managed by Caddyfile/system config and is read-only: %w", serverID, model.ErrReadOnly)
	}
	return nil
}

func (m *Manager) listCaddyRoutes(ctx context.Context, serverID string) ([]caddyRoute, error) {
	srv, err := m.getRawServer(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return srv.Routes, nil
}

func (m *Manager) getRawServer(ctx context.Context, serverID string) (*caddyServer, error) {
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

func (m *Manager) isProtectedServer(id string, srv *caddyServer) bool {
	if srv == nil {
		return false
	}
	if m.isReadOnlyServerID(id) {
		return true
	}
	for _, route := range srv.Routes {
		if routeContainsAdminHandler(route) {
			return true
		}
		if route.Group == "" {
			return true
		}
	}
	return false
}

func (m *Manager) isReadOnlyServerID(id string) bool {
	if m == nil || id == "" || len(m.readOnlyServerIDs) == 0 {
		return false
	}
	_, ok := m.readOnlyServerIDs[id]
	return ok
}

func serverHasCaddyfileRoutes(srv *caddyServer) bool {
	if srv == nil {
		return false
	}
	for _, route := range srv.Routes {
		if route.Group == "" {
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

func (m *Manager) caddyGET(ctx context.Context, path string) (json.RawMessage, error) {
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
		return nil, model.ErrNotFound
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("caddy admin error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (m *Manager) caddyPUT(ctx context.Context, path string, val any) error {
	if err := m.checkAllowed(path); err != nil {
		return err
	}
	cfgMap, err := m.getFullConfigMap(ctx)
	if err != nil {
		return err
	}
	valJSON, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("marshal value: %w", err)
	}
	var valData any
	if err := json.Unmarshal(valJSON, &valData); err != nil {
		return fmt.Errorf("unmarshal value: %w", err)
	}
	if err := setAtJSONPath(cfgMap, path, valData); err != nil {
		return fmt.Errorf("set config at path %q: %w", path, err)
	}
	return m.postFullConfig(ctx, cfgMap)
}

func (m *Manager) caddyDELETE(ctx context.Context, path string) error {
	if err := m.checkAllowed(path); err != nil {
		return err
	}
	cfgMap, err := m.getFullConfigMap(ctx)
	if err != nil {
		return err
	}
	if err := deleteAtJSONPath(cfgMap, path); err != nil {
		return fmt.Errorf("delete config at path %q: %w", path, err)
	}
	return m.postFullConfig(ctx, cfgMap)
}

func (m *Manager) getFullConfigMap(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.adminAddr+"/config/", nil)
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
		return nil, fmt.Errorf("read caddy config: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("caddy admin error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var cfgMap map[string]any
	if err := json.Unmarshal(body, &cfgMap); err != nil {
		return nil, fmt.Errorf("parse caddy config: %w", err)
	}
	return cfgMap, nil
}

func (m *Manager) postFullConfig(ctx context.Context, cfgMap map[string]any) error {
	body, err := json.Marshal(cfgMap)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.adminAddr+"/config/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("caddy admin unreachable: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("caddy admin error %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func setAtJSONPath(cfgMap map[string]any, path string, val any) error {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("empty path")
	}
	var curr any = cfgMap
	for _, part := range parts[:len(parts)-1] {
		m, ok := curr.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object at %q, got %T", part, curr)
		}
		if _, exists := m[part]; !exists {
			m[part] = make(map[string]any)
		}
		curr = m[part]
	}
	m, ok := curr.(map[string]any)
	if !ok {
		return fmt.Errorf("expected object at parent of %q", parts[len(parts)-1])
	}
	m[parts[len(parts)-1]] = val
	return nil
}

func deleteAtJSONPath(cfgMap map[string]any, path string) error {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("empty path")
	}
	var curr any = cfgMap
	for _, part := range parts[:len(parts)-1] {
		m, ok := curr.(map[string]any)
		if !ok {
			return nil
		}
		next, exists := m[part]
		if !exists {
			return nil
		}
		curr = next
	}
	m, ok := curr.(map[string]any)
	if !ok {
		return nil
	}
	delete(m, parts[len(parts)-1])
	return nil
}

func (m *Manager) checkAllowed(path string) error {
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return nil
		}
	}
	return fmt.Errorf("config path %q is not allowed", path)
}
