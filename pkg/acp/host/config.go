package host

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"time"

	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
	codexconfig "github.com/agent-guide/agent-gateway/pkg/acp/agent/codex/config"
	"github.com/agent-guide/agent-gateway/pkg/acp/agentspi"
)

// Config is the process-facing ACP runtime configuration. OwnerID is assigned
// by the runtime adapter from the owning Agent and is never serialized as an
// independent management identity.
type Config struct {
	OwnerID         string            `json:"-"`
	AgentType       string            `json:"agent_type"`
	CWD             string            `json:"cwd"`
	AllowedRoots    []string          `json:"allowed_roots,omitempty"`
	DefaultModel    string            `json:"default_model,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	ConfigOverrides map[string]string `json:"config_overrides,omitempty"`
	IdleTTL         time.Duration     `json:"idle_ttl,omitempty"`
	MaxInstances    int               `json:"max_instances,omitempty"`
	PermissionMode  string            `json:"permission_mode,omitempty"`
	Command         string            `json:"command,omitempty"`
	Args            []string          `json:"args,omitempty"`
}

// Clone returns a copy with independent maps and slices.
func (c Config) Clone() Config {
	c.AllowedRoots = append([]string(nil), c.AllowedRoots...)
	c.Env = maps.Clone(c.Env)
	c.ConfigOverrides = maps.Clone(c.ConfigOverrides)
	c.Args = append([]string(nil), c.Args...)
	return c
}

// Fingerprint returns a canonical process-config fingerprint. It requires an
// owner ID, which is excluded from the fingerprint.
func (c Config) Fingerprint(ownerID string) (string, error) {
	if strings.TrimSpace(ownerID) == "" {
		return "", fmt.Errorf("runtime owner id is required")
	}
	cfg, err := ownerRuntimeConfig(c, ownerID)
	if err != nil {
		return "", err
	}
	return configFingerprint(cfg)
}

func ownerRuntimeConfig(c Config, ownerID string) (Config, error) {
	cfg := c.Clone()
	cfg.OwnerID = ownerID
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// configFingerprint hashes the effective execution config used by the pool.
func configFingerprint(cfg Config) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (c Config) openRequest(cwd string) agentspi.OpenRequest {
	return agentspi.OpenRequest{
		CWD:     cwd,
		Env:     maps.Clone(c.Env),
		Command: c.Command,
		Args:    append([]string(nil), c.Args...),
	}
}

func (c *Config) Normalize() {
	if c == nil {
		return
	}
	c.OwnerID = strings.TrimSpace(c.OwnerID)
	c.AgentType = strings.TrimSpace(c.AgentType)
	c.CWD = strings.TrimSpace(c.CWD)
	c.DefaultModel = strings.TrimSpace(c.DefaultModel)
	c.Command = strings.TrimSpace(c.Command)
	if c.AgentType == baseacp.AgentTypeCodex && c.Command == "" {
		c.Command = codexconfig.DefaultAdapterCommand
	}
	c.PermissionMode = strings.TrimSpace(c.PermissionMode)
	if c.PermissionMode == "" {
		c.PermissionMode = baseacp.PermissionModeDeny
	}
	for i := range c.AllowedRoots {
		c.AllowedRoots[i] = strings.TrimSpace(c.AllowedRoots[i])
	}
	if len(c.AllowedRoots) == 0 && c.CWD != "" {
		c.AllowedRoots = []string{c.CWD}
	}
	if len(c.Env) > 0 {
		normalized := make(map[string]string, len(c.Env))
		for key, value := range c.Env {
			normalized[strings.TrimSpace(key)] = value
		}
		c.Env = normalized
	}
}

func (c Config) Validate() error {
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
	if err := validateCWDAllowed(c.CWD, c.AllowedRoots); err != nil {
		return err
	}
	switch mode := strings.TrimSpace(c.PermissionMode); mode {
	case "", baseacp.PermissionModeDeny, baseacp.PermissionModeAutoApprove, baseacp.PermissionModeInteractive:
	default:
		return fmt.Errorf("unsupported permission_mode %q", mode)
	}
	if c.MaxInstances < 0 {
		return fmt.Errorf("max_instances must be non-negative")
	}
	for key, value := range c.Env {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("env key must not be empty")
		}
		if strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("env key %q must not contain '=' or NUL", key)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("env value for key %q must not contain NUL", key)
		}
	}
	if c.AgentType == baseacp.AgentTypeCodex {
		return codexconfig.ValidateAdapterCommand(c.Command)
	}
	return nil
}

func validateCWDAllowed(cwd string, roots []string) error {
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
