package mcproute

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type RouteMatch struct {
	Host       string   `json:"host,omitempty"`
	PathPrefix string   `json:"path_prefix,omitempty"`
	Methods    []string `json:"methods,omitempty"`
}

type RouteAuthPolicy struct {
	RequireVirtualKey bool `json:"require_virtual_key,omitempty"`
}

type MCPRoute struct {
	ID         string          `json:"id,omitempty"`
	Match      RouteMatch      `json:"match,omitempty"`
	ServiceID  string          `json:"service_id"`
	Disabled   bool            `json:"disabled,omitempty"`
	AuthPolicy RouteAuthPolicy `json:"auth_policy,omitempty"`
	CreatedAt  time.Time       `json:"created_at,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at,omitempty"`
}

func (r *MCPRoute) Normalize() {
	if r == nil {
		return
	}
	r.ServiceID = strings.TrimSpace(r.ServiceID)
	r.ID = strings.TrimSpace(r.ID)
	r.Match.Host = strings.TrimSpace(r.Match.Host)
	r.Match.PathPrefix = strings.TrimSpace(r.Match.PathPrefix)
	for i := range r.Match.Methods {
		r.Match.Methods[i] = strings.TrimSpace(r.Match.Methods[i])
	}
	if r.ID == "" && r.ServiceID != "" {
		path := r.Match.PathPrefix
		if path == "" {
			path = "/"
		}
		r.ID = "mcp:" + r.ServiceID + ":" + path
	}
}

func (r *MCPRoute) NormalizeTimestamps(now time.Time) {
	if r == nil {
		return
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
}

func DecodeStoredMCPRoute(data []byte) (any, error) {
	var route MCPRoute
	if err := json.Unmarshal(data, &route); err != nil {
		return nil, err
	}
	route.Normalize()
	if route.ID == "" || route.ServiceID == "" {
		return nil, fmt.Errorf("mcp route requires id and service_id")
	}
	return &route, nil
}
