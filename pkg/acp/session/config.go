package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	baseacp "github.com/agent-guide/agent-gateway/pkg/acp"
	acpclient "github.com/agent-guide/agent-gateway/pkg/acp/client"
	acplauncher "github.com/agent-guide/agent-gateway/pkg/acp/launcher"
)

// Config is the process-facing ACP runtime configuration. OwnerID is assigned
// by the runtime adapter from the owning Agent and is never serialized as an
// independent management identity.
type Config struct {
	OwnerID         string            `json:"-"`
	Launcher        string            `json:"launcher"`
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
	CWDFlag         string            `json:"cwd_flag,omitempty"`
	factory         acpclient.Factory
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

// ResolveLauncher applies the selected command and copies its arguments.
func (c Config) ResolveLauncher(launchers acplauncher.Launchers) (Config, error) {
	launcher, ok := launchers[c.Launcher]
	if !ok {
		return Config{}, fmt.Errorf("ACP launcher %q is not registered", c.Launcher)
	}
	c.Command, c.Args, c.CWDFlag = launcher.Command, slices.Clone(launcher.Args), launcher.CWDFlag
	return c, nil
}

func (c Config) newAgent(cwd string) (acpclient.Agent, error) {
	req := c.openRequest(cwd)
	if c.factory != nil {
		return c.factory(req)
	}
	return acpclient.NewStdio(c.Launcher, req), nil
}

func (c Config) openRequest(cwd string) acpclient.OpenRequest {
	args := append([]string(nil), c.Args...)
	if c.CWDFlag != "" {
		args = append(args, c.CWDFlag, cwd)
	}
	return acpclient.OpenRequest{
		CWD:     cwd,
		Env:     maps.Clone(c.Env),
		Command: c.Command,
		Args:    args,
	}
}

func (c *Config) Normalize() {
	if c == nil {
		return
	}
	c.OwnerID = strings.TrimSpace(c.OwnerID)
	c.Launcher = strings.TrimSpace(c.Launcher)
	c.CWD = strings.TrimSpace(c.CWD)
	c.DefaultModel = strings.TrimSpace(c.DefaultModel)
	c.Command = strings.TrimSpace(c.Command)
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
	if err := c.ValidateDefinition(); err != nil {
		return err
	}
	if c.factory != nil {
		return nil
	}
	return (acplauncher.Launcher{Command: c.Command, Args: c.Args, CWDFlag: c.CWDFlag}).Validate()
}

// ValidateDefinition checks Agent-owned settings before deployment launch resolution.
func (c Config) ValidateDefinition() error {
	if err := acplauncher.ValidateLauncherName(c.Launcher); err != nil {
		return err
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
