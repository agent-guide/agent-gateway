// Package mcp executes MCP connector operations and owns connector-scoped upstream sessions.
// Control-plane admission and mutation exclusion are owned by the composing Manager.
package mcp

import (
	"fmt"
	"strings"

	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
)

type Config struct {
	Transport  basemcp.TransportType `json:"transport"`
	Command    string                `json:"command,omitempty"`  // stdio only
	Args       []string              `json:"args,omitempty"`     // stdio only
	URL        string                `json:"url,omitempty"`      // remote transports
	PostURL    string                `json:"post_url,omitempty"` // SSE only; if empty, derived from url
	Env        map[string]string     `json:"env,omitempty"`
	AutoAuth   bool                  `json:"auto_auth,omitempty"`
	AuthConfig *AuthConfig           `json:"auth,omitempty"`
}

// AuthConfig contains MCP authentication configuration.
type AuthConfig struct {
	Type     string `json:"type"` // api_key, oauth2, basic
	APIKey   string `json:"api_key,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func (c *Config) Normalize() {
	if c == nil {
		return
	}
	c.Command = strings.TrimSpace(c.Command)
	c.URL = strings.TrimSpace(c.URL)
	c.Transport = basemcp.TransportType(strings.TrimSpace(string(c.Transport)))
	if c.AuthConfig != nil {
		c.AuthConfig.Type = strings.TrimSpace(c.AuthConfig.Type)
		c.AuthConfig.Username = strings.TrimSpace(c.AuthConfig.Username)
	}
}

func (c Config) Validate() error {
	switch c.Transport {
	case basemcp.TransportStdio:
		if c.Command == "" {
			return fmt.Errorf("command is required for stdio transport")
		}
	case basemcp.TransportSSE, basemcp.TransportStreamableHTTP:
		if c.URL == "" {
			return fmt.Errorf("url is required for transport %q", c.Transport)
		}
	default:
		return fmt.Errorf("unsupported transport %q", c.Transport)
	}
	if c.AuthConfig != nil {
		switch c.AuthConfig.Type {
		case "", "api_key", "basic", "bearer", "oauth2":
		default:
			return fmt.Errorf("unsupported auth type %q", c.AuthConfig.Type)
		}
	}
	return nil
}
