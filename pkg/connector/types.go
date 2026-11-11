package connector

import (
	"fmt"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/internal/strictjson"
	"github.com/agent-guide/agent-gateway/pkg/connector/httpapi"
	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
)

type TransportType = basemcp.TransportType
type ClientStatus = basemcp.ClientStatus

const (
	TransportStdio          = basemcp.TransportStdio
	TransportSSE            = basemcp.TransportSSE
	TransportStreamableHTTP = basemcp.TransportStreamableHTTP
)

// ConnectorConfig defines one MCP or HTTP API connector.
type ConnectorConfig struct {
	// ID is the case-sensitive, immutable identifier used by references and API paths.
	ID string `json:"id"`
	// Name is the mutable display name; it is neither unique nor a reference key.
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	MCP         *MCPConfig      `json:"mcp,omitempty"`
	HTTPAPI     *httpapi.Config `json:"http_api,omitempty"`
	Audit       AuditConfig     `json:"audit,omitempty"`
	Disabled    bool            `json:"disabled"`
	Description string          `json:"description,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type MCPConfig struct {
	Transport  TransportType     `json:"transport"`
	Command    string            `json:"command,omitempty"`  // stdio only
	Args       []string          `json:"args,omitempty"`     // stdio only
	URL        string            `json:"url,omitempty"`      // remote transports
	PostURL    string            `json:"post_url,omitempty"` // SSE only; if empty, derived from url
	Env        map[string]string `json:"env,omitempty"`
	AutoAuth   bool              `json:"auto_auth,omitempty"`
	AuthConfig *AuthConfig       `json:"auth,omitempty"`
}

type AuditConfig struct {
	CaptureToolArgs bool `json:"capture_tool_args,omitempty"`
}

// AuthConfig contains MCP authentication configuration.
type AuthConfig struct {
	Type     string `json:"type"` // api_key, oauth2, basic
	APIKey   string `json:"api_key,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func DecodeStoredConnectorConfig(data []byte) (any, error) {
	var cfg ConnectorConfig
	if err := strictjson.Decode(data, &cfg, 4<<20, 128); err != nil {
		return nil, err
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *ConnectorConfig) Normalize() {
	if c == nil {
		return
	}
	c.ID = strings.TrimSpace(c.ID)
	c.Name = strings.TrimSpace(c.Name)
	if c.MCP != nil {
		c.MCP.Command = strings.TrimSpace(c.MCP.Command)
		c.MCP.URL = strings.TrimSpace(c.MCP.URL)
	}
	c.Description = strings.TrimSpace(c.Description)
	if c.MCP == nil {
		return
	}
	c.MCP.Transport = TransportType(strings.TrimSpace(string(c.MCP.Transport)))
	if c.MCP.AuthConfig != nil {
		c.MCP.AuthConfig.Type = strings.TrimSpace(c.MCP.AuthConfig.Type)
		c.MCP.AuthConfig.Username = strings.TrimSpace(c.MCP.AuthConfig.Username)
	}
}

func (c ConnectorConfig) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("id is required")
	}
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.ContainsAny(c.ID, "/\\") {
		return fmt.Errorf("invalid connector id")
	}
	switch c.Type {
	case "http_api":
		if c.HTTPAPI == nil || c.MCP != nil {
			return fmt.Errorf("http_api requires only http_api branch")
		}
		if c.Audit.CaptureToolArgs {
			return fmt.Errorf("HTTP argument capture is unsupported")
		}
		return c.HTTPAPI.Validate()
	case "mcp":
		if c.MCP == nil || c.HTTPAPI != nil {
			return fmt.Errorf("mcp requires only mcp branch")
		}
	default:
		return fmt.Errorf("unsupported connector type")
	}
	switch c.MCP.Transport {
	case TransportStdio:
		if c.MCP.Command == "" {
			return fmt.Errorf("command is required for stdio transport")
		}
	case TransportSSE, TransportStreamableHTTP:
		if c.MCP.URL == "" {
			return fmt.Errorf("url is required for transport %q", c.MCP.Transport)
		}
	default:
		return fmt.Errorf("unsupported transport %q", c.MCP.Transport)
	}
	if c.MCP.AuthConfig != nil {
		switch c.MCP.AuthConfig.Type {
		case "", "api_key", "basic", "bearer", "oauth2":
		default:
			return fmt.Errorf("unsupported auth type %q", c.MCP.AuthConfig.Type)
		}
	}
	return nil
}

// ValidateToolSelection checks names resolvable from a configured HTTP catalog.
// MCP catalogs remain remote and are checked at materialization time.
func (c ConnectorConfig) ValidateToolSelection(names []string) error {
	if c.HTTPAPI == nil {
		return nil
	}
	available := make(map[string]bool, len(c.HTTPAPI.Tools))
	for _, tool := range c.HTTPAPI.Tools {
		available[tool.Name] = true
	}
	for _, name := range names {
		if !available[strings.TrimSpace(name)] {
			return fmt.Errorf("connector %q has no selected tool %q", c.ID, name)
		}
	}
	return nil
}

func (c *ConnectorConfig) NormalizeTimestamps(now time.Time) {
	if c == nil {
		return
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
}

func (c *ConnectorConfig) UnmarshalJSON(data []byte) error {
	type plain ConnectorConfig
	v, err := strictjson.Parse(data, 4<<20, 128)
	if err != nil {
		return err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("connector must be an object")
	}
	typ, _ := obj["type"].(string)
	if typ == "mcp" {
		if _, ok := obj["http_api"]; ok {
			return fmt.Errorf("http_api branch forbidden")
		}
	} else if typ == "http_api" {
		if _, ok := obj["mcp"]; ok {
			return fmt.Errorf("mcp branch forbidden")
		}
	}
	var p plain
	if err := strictjson.Decode(data, &p, 4<<20, 128); err != nil {
		return err
	}
	*c = ConnectorConfig(p)
	return nil
}
