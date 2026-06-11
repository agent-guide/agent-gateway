package service

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
)

var ErrServiceNotConfigured = fmt.Errorf("acp service is not configured")

const (
	CodexModeAdapter   = "adapter"
	CodexModeAppServer = "app_server"
)

type ServiceConfig struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	AgentType       string            `json:"agent_type"`
	CWD             string            `json:"cwd"`
	AllowedRoots    []string          `json:"allowed_roots,omitempty"`
	DefaultModel    string            `json:"default_model,omitempty"`
	ConfigOverrides map[string]string `json:"config_overrides,omitempty"`
	IdleTTL         time.Duration     `json:"idle_ttl,omitempty"`
	PermissionMode  string            `json:"permission_mode,omitempty"`
	Disabled        bool              `json:"disabled"`
	Description     string            `json:"description,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Codex           *CodexConfig      `json:"codex,omitempty"`
}

type CodexConfig struct {
	Mode             string   `json:"mode,omitempty"`
	AdapterCommand   string   `json:"adapter_command,omitempty"`
	AdapterArgs      []string `json:"adapter_args,omitempty"`
	AppServerCommand string   `json:"app_server_command,omitempty"`
	AppServerArgs    []string `json:"app_server_args,omitempty"`
	DefaultProfile   string   `json:"default_profile,omitempty"`
	InitialAuthMode  string   `json:"initial_auth_mode,omitempty"`
	TraceJSON        bool     `json:"trace_json,omitempty"`
	RetryTurnOnCrash bool     `json:"retry_turn_on_crash,omitempty"`
}

func DecodeStoredServiceConfig(data []byte) (any, error) {
	var cfg ServiceConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *ServiceConfig) Normalize() {
	if c == nil {
		return
	}
	c.ID = strings.TrimSpace(c.ID)
	c.Name = strings.TrimSpace(c.Name)
	c.AgentType = strings.TrimSpace(c.AgentType)
	c.CWD = strings.TrimSpace(c.CWD)
	c.DefaultModel = strings.TrimSpace(c.DefaultModel)
	c.PermissionMode = strings.TrimSpace(c.PermissionMode)
	if c.PermissionMode == "" {
		c.PermissionMode = baseacp.PermissionModeDeny
	}
	c.Description = strings.TrimSpace(c.Description)
	for i := range c.AllowedRoots {
		c.AllowedRoots[i] = strings.TrimSpace(c.AllowedRoots[i])
	}
	if len(c.AllowedRoots) == 0 && c.CWD != "" {
		c.AllowedRoots = []string{c.CWD}
	}
	if c.Codex != nil {
		c.Codex.Mode = strings.TrimSpace(c.Codex.Mode)
		if c.Codex.Mode == "" {
			c.Codex.Mode = CodexModeAdapter
		}
		c.Codex.AdapterCommand = strings.TrimSpace(c.Codex.AdapterCommand)
		if c.Codex.AdapterCommand == "" && c.Codex.Mode == CodexModeAdapter {
			c.Codex.AdapterCommand = "codex-acp"
		}
		c.Codex.AppServerCommand = strings.TrimSpace(c.Codex.AppServerCommand)
		c.Codex.DefaultProfile = strings.TrimSpace(c.Codex.DefaultProfile)
		c.Codex.InitialAuthMode = strings.TrimSpace(c.Codex.InitialAuthMode)
	}
}

func (c ServiceConfig) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("id is required")
	}
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch c.AgentType {
	case baseacp.AgentTypeCodex, baseacp.AgentTypeOpencode:
	default:
		return fmt.Errorf("unsupported acp agent_type %q", c.AgentType)
	}
	if c.CWD == "" {
		return fmt.Errorf("cwd is required")
	}
	if !filepath.IsAbs(c.CWD) {
		return fmt.Errorf("cwd must be absolute")
	}
	if len(c.AllowedRoots) == 0 {
		return fmt.Errorf("allowed_roots is required")
	}
	if err := ValidateCWDAllowed(c.CWD, c.AllowedRoots); err != nil {
		return err
	}
	switch c.PermissionMode {
	case "", baseacp.PermissionModeDeny, baseacp.PermissionModeAutoApprove, baseacp.PermissionModeInteractive:
	default:
		return fmt.Errorf("unsupported permission_mode %q", c.PermissionMode)
	}
	if c.AgentType == baseacp.AgentTypeCodex && c.Codex != nil {
		switch c.Codex.Mode {
		case "", CodexModeAdapter:
			if c.Codex.AdapterCommand != "" && filepath.Base(c.Codex.AdapterCommand) != "codex-acp" {
				return fmt.Errorf("codex adapter_command must be codex-acp")
			}
		case CodexModeAppServer:
			return fmt.Errorf("codex mode %q is not implemented", c.Codex.Mode)
		default:
			return fmt.Errorf("unsupported codex mode %q", c.Codex.Mode)
		}
	}
	return nil
}

func (c *ServiceConfig) NormalizeTimestamps(now time.Time) {
	if c == nil {
		return
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
}

func ValidateCWDAllowed(cwd string, roots []string) error {
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("cwd must be absolute")
	}
	for _, root := range roots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			return fmt.Errorf("allowed root %q must be absolute", root)
		}
		if cwd == root {
			return nil
		}
		rel, err := filepath.Rel(root, cwd)
		if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return nil
		}
	}
	return fmt.Errorf("cwd %q is outside allowed_roots", cwd)
}
