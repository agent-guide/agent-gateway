// Package distribution supplies public value options for reusable binary entries.
// Edition, Assembly, Assembly.Clone, MetricsConfig and OTLPConfig are designated
// reusable entry values; release protection follows the enterprise extension contract.
// Assembly.Registry and Assembly.Manifest freeze linked registrations and metadata.
// Assembly.AttributionPolicy validates the assembly-owned attribute allowlist.
package distribution

import (
	"encoding/json"
	"github.com/agent-guide/agent-gateway/pkg/attribution"
	"github.com/agent-guide/agent-gateway/pkg/event"
	"github.com/agent-guide/agent-gateway/pkg/extension"
	"github.com/spf13/cobra"
	"slices"
	"time"
)

// Edition is distribution-owned identification, never an entitlement decision.
type Edition struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Assembly records explicitly linked modules. Metadata does not link factories;
// main must import the implementations it declares. Factories are explicit
// executable registrations and never appear in serialized metadata.
type Assembly struct {
	Edition             Edition                       `json:"edition"`
	Modules             []extension.Descriptor        `json:"modules,omitempty"`
	Factories           map[string]*extension.Factory `json:"-"`
	AttributeKeys       []string                      `json:"-"`
	AttributionProducer attribution.Producer          `json:"-"`
	EventSinks          []event.Registration          `json:"-"`
}

// Clone takes ownership of a metadata snapshot without retaining caller slices.
func (a Assembly) Clone() Assembly {
	a.AttributeKeys = slices.Clone(a.AttributeKeys)
	a.EventSinks = slices.Clone(a.EventSinks)
	var factories map[string]*extension.Factory
	if a.Factories != nil {
		factories = make(map[string]*extension.Factory, len(a.Factories))
	}
	for id, f := range a.Factories {
		if f != nil {
			owned := *f
			owned.AdminRoutes = slices.Clone(f.AdminRoutes)
			owned.SecretFields = slices.Clone(f.SecretFields)
			factories[id] = &owned
		} else {
			factories[id] = nil
		}
	}
	a.Factories = factories
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
// Annotations are diagnostic transport; serving uses the validated registry.
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

// Registry freezes the explicitly linked metadata and configuration factories.
// Factory IDs must be declared in Modules; no implicit registration is allowed.
func (a Assembly) Registry() (*extension.Registry, error) {
	if _, err := a.AttributionPolicy(); err != nil {
		return nil, err
	}
	sinkIDs := map[string]bool{}
	linked := map[string]bool{}
	for _, m := range a.Modules {
		linked[m.ID] = true
	}
	for _, r := range a.EventSinks {
		if !linked[r.ModuleID] || r.New == nil || sinkIDs[r.ModuleID] || r.QueueSize < 0 || r.QueueSize > 4096 || r.WriteBudget < 0 || r.WriteBudget > time.Second {
			return nil, &event.Error{Code: "invalid_sink_registration"}
		}
		sinkIDs[r.ModuleID] = true
	}
	registrations := make([]extension.Registration, 0, len(a.Modules))
	seen := map[string]bool{}
	for _, d := range a.Modules {
		seen[d.ID] = true
		registrations = append(registrations, extension.Registration{Descriptor: d, Factory: a.Factories[d.ID]})
	}
	for id, f := range a.Factories {
		if !seen[id] || f == nil {
			return nil, &extension.Error{Code: "invalid_registration", Field: "factories"}
		}
	}
	return extension.NewRegistry(registrations...)
}

// Manifest returns a canonical, pointer-free assembly for serialization.
func (a Assembly) Manifest() (Assembly, error) {
	r, err := a.Registry()
	if err != nil {
		return Assembly{}, err
	}
	return Assembly{Edition: a.Edition, Modules: r.Manifest().Modules}, nil
}

// AttributionPolicy validates the exact assembly-owned attribute allowlist.
func (a Assembly) AttributionPolicy() (attribution.Policy, error) {
	modules := make([]string, len(a.Modules))
	for i, m := range a.Modules {
		modules[i] = m.ID
	}
	return attribution.NewPolicy(modules, a.AttributeKeys)
}
