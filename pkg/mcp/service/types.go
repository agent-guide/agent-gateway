package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	basemcp "github.com/agent-guide/agent-gateway/pkg/mcp"
)

type TransportType = basemcp.TransportType
type ClientStatus = basemcp.ClientStatus

const (
	TransportStdio          = basemcp.TransportStdio
	TransportSSE            = basemcp.TransportSSE
	TransportStreamableHTTP = basemcp.TransportStreamableHTTP
)

// MCPServiceConfig is the configuration for one upstream MCP service.
type MCPServiceConfig struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Transport   TransportType     `json:"transport"`
	Command     string            `json:"command,omitempty"` // stdio only
	Args        []string          `json:"args,omitempty"`    // stdio only
	URL         string            `json:"url,omitempty"`     // remote transports
	PostURL     string            `json:"post_url,omitempty"` // SSE only; if empty, derived from url
	Env         map[string]string `json:"env,omitempty"`
	AutoAuth    bool              `json:"auto_auth,omitempty"`
	AuthConfig  *AuthConfig       `json:"auth,omitempty"`
	Disabled    bool              `json:"disabled"`
	Description string            `json:"description,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// AuthConfig contains MCP authentication configuration.
type AuthConfig struct {
	Type     string `json:"type"` // api_key, oauth2, basic
	APIKey   string `json:"api_key,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func DecodeStoredMCPServiceConfig(data []byte) (any, error) {
	var cfg MCPServiceConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *MCPServiceConfig) Normalize() {
	if c == nil {
		return
	}
	c.ID = strings.TrimSpace(c.ID)
	c.Name = strings.TrimSpace(c.Name)
	c.Command = strings.TrimSpace(c.Command)
	c.URL = strings.TrimSpace(c.URL)
	c.Description = strings.TrimSpace(c.Description)
	c.Transport = TransportType(strings.TrimSpace(string(c.Transport)))
	if c.AuthConfig != nil {
		c.AuthConfig.Type = strings.TrimSpace(c.AuthConfig.Type)
		c.AuthConfig.Username = strings.TrimSpace(c.AuthConfig.Username)
	}
}

func (c MCPServiceConfig) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("id is required")
	}
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch c.Transport {
	case TransportStdio:
		if c.Command == "" {
			return fmt.Errorf("command is required for stdio transport")
		}
	case TransportSSE, TransportStreamableHTTP:
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

func (c *MCPServiceConfig) NormalizeTimestamps(now time.Time) {
	if c == nil {
		return
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
}
