// Package distribution supplies public value options for reusable binary entries.
// Edition, Assembly, Assembly.Clone, MetricsConfig and OTLPConfig are designated
// reusable entry values; release protection follows the enterprise extension contract.
// Extension descriptor validation and runtime discovery remain F2 work.
package distribution

import (
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/spf13/cobra"
	"slices"
)

// Edition is distribution-owned identification, never an entitlement decision.
type Edition struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Assembly records explicitly linked modules. Metadata does not link factories;
// main must import the implementations it declares.
type Assembly struct {
	Edition Edition                `json:"edition"`
	Modules []extension.Descriptor `json:"modules,omitempty"`
}

// Clone takes ownership of a metadata snapshot without retaining caller slices.
func (a Assembly) Clone() Assembly {
	a.Modules = slices.Clone(a.Modules)
	for i := range a.Modules {
		m := &a.Modules[i]
		m.Capabilities = slices.Clone(m.Capabilities)
		m.Dependencies = slices.Clone(m.Dependencies)
		m.Roles = slices.Clone(m.Roles)
	}
	return a
}

// Annotate attaches a serialized snapshot to a command, without process globals.
// F2 will use this assembly input for validated runtime manifest projection.
func Annotate(cmd *cobra.Command, a Assembly) {
	data, _ := json.Marshal(a.Clone())
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations["agent-gateway.assembly"] = string(data)
}

// CommunityRuntime is the metadata shared by both Community server entries.
func CommunityRuntime() Assembly {
	const prefix = "online.agentguide.community."
	a := Assembly{Edition: Edition{Name: "community"}}
	a.Modules = append(a.Modules, extension.Descriptor{ID: prefix + "gateway", Version: "0.7.0-dev", SchemaVersion: 1, Capabilities: []string{prefix + "llm", prefix + "mcp", prefix + "agent", prefix + "metrics", prefix + "admin"}, Roles: []string{"runtime", "validation", "admin"}})
	for _, name := range []string{"anthropic", "anthropicbase", "claudecode", "codex", "deepseek", "gemini", "grok", "ollama", "openai", "openrouter", "qwen", "zhipu"} {
		a.Modules = append(a.Modules, extension.Descriptor{ID: prefix + "provider." + name, Version: "0.7.0-dev", SchemaVersion: 1, Capabilities: []string{prefix + "provider." + name}, Roles: []string{"runtime", "validation"}})
	}
	for _, name := range []string{"codex", "opencode"} {
		a.Modules = append(a.Modules, extension.Descriptor{ID: prefix + "acp." + name, Version: "0.7.0-dev", SchemaVersion: 1, Capabilities: []string{prefix + "acp." + name}, Roles: []string{"runtime"}})
	}
	return a
}

// CommunityValidation declares CLI factories; it does not claim server execution.
func CommunityValidation() Assembly {
	a := CommunityRuntime()
	modules := a.Modules[:0]
	for _, m := range a.Modules {
		if slices.Contains(m.Roles, "validation") {
			m.Roles = []string{"validation"}
			if m.ID == "online.agentguide.community.gateway" {
				m.Capabilities = []string{"online.agentguide.community.bundle-validation"}
			}
			modules = append(modules, m)
		}
	}
	a.Modules = modules
	return a
}
