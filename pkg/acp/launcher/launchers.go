// Package launcher defines launch settings for ACP subprocesses.
package launcher

import (
	"fmt"
	"strings"

	"github.com/agent-guide/agent-gateway/internal/strictjson"
)

// Launcher is a deployment-owned executable and argument vector.
type Launcher struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	CWDFlag string   `json:"cwd_flag,omitempty"`
}

type Launchers map[string]Launcher

// Definition stores a deployment launcher under its reference name.
type Definition struct {
	ID string `json:"id"`
	Launcher
}

func DecodeDefinition(data []byte) (Definition, error) {
	var def Definition
	if err := strictjson.Decode(data, &def, 4<<20, 16); err != nil {
		return Definition{}, fmt.Errorf("decode ACP launcher: %w", err)
	}
	def.Command = strings.TrimSpace(def.Command)
	return def, def.Validate()
}

func (def Definition) Validate() error {
	if err := ValidateLauncherName(def.ID); err != nil {
		return err
	}
	return def.Launcher.Validate()
}

// DefaultLaunchers describes the ACP executables bundled in the distribution.
func DefaultLaunchers() Launchers {
	return Launchers{
		"codex":    codexLauncher(),
		"opencode": openCodeLauncher(),
	}
}

// ValidateLauncherName checks a deployment launcher ID.
func ValidateLauncherName(name string) error {
	if name == "" || strings.TrimSpace(name) != name || strings.ContainsRune(name, 0) {
		return fmt.Errorf("ACP launcher name must be nonempty and contain no surrounding whitespace or NUL")
	}
	return nil
}

// Validate checks the executable and argument vector.
func (l Launcher) Validate() error {
	if strings.TrimSpace(l.Command) == "" || strings.ContainsRune(l.Command, 0) {
		return fmt.Errorf("command must be nonempty and contain no NUL")
	}
	if strings.ContainsRune(l.CWDFlag, 0) {
		return fmt.Errorf("cwd_flag must contain no NUL")
	}
	for _, arg := range l.Args {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("arguments must contain no NUL")
		}
	}
	return nil
}
