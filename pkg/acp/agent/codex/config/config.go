// Package config defines Codex ACP adapter settings for Agent definitions.
package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	ModeAdapter           = "adapter"
	ModeAppServer         = "app_server"
	DefaultAdapterCommand = "codex-acp"
)

// Config is the schema for the Agent's persisted runtime.acp.codex block.
type Config struct {
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

// Normalized returns an independent copy of the Codex settings with defaults applied.
func (c *Config) Normalized() *Config {
	if c == nil {
		return nil
	}
	cfg := *c
	cfg.Mode = strings.TrimSpace(cfg.Mode)
	if cfg.Mode == "" {
		cfg.Mode = ModeAdapter
	}
	cfg.AdapterCommand = strings.TrimSpace(cfg.AdapterCommand)
	if cfg.AdapterCommand == "" && cfg.Mode == ModeAdapter {
		cfg.AdapterCommand = DefaultAdapterCommand
	}
	cfg.AdapterArgs = append([]string(nil), cfg.AdapterArgs...)
	cfg.AppServerCommand = strings.TrimSpace(cfg.AppServerCommand)
	cfg.AppServerArgs = append([]string(nil), cfg.AppServerArgs...)
	cfg.DefaultProfile = strings.TrimSpace(cfg.DefaultProfile)
	cfg.InitialAuthMode = strings.TrimSpace(cfg.InitialAuthMode)
	return &cfg
}

func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	switch c.Mode {
	case "", ModeAdapter:
		return ValidateAdapterCommand(c.AdapterCommand)
	case ModeAppServer:
		return fmt.Errorf("codex mode %q is not implemented", c.Mode)
	default:
		return fmt.Errorf("unsupported codex mode %q", c.Mode)
	}
}

// ValidateAdapterCommand accepts the default command or a path to codex-acp.
func ValidateAdapterCommand(command string) error {
	if command != "" && filepath.Base(command) != DefaultAdapterCommand {
		return fmt.Errorf("codex adapter_command must be %s", DefaultAdapterCommand)
	}
	return nil
}
