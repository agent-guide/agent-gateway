package runtimebackend

import (
	"context"
	"errors"
	"fmt"
	"sync"

	acphost "github.com/agent-guide/agent-gateway/pkg/acp/host"
	agentpkg "github.com/agent-guide/agent-gateway/pkg/agent"
	builtinhost "github.com/agent-guide/agent-gateway/pkg/agent/builtin"
	agentruntime "github.com/agent-guide/agent-gateway/pkg/agent/runtime"
	"github.com/agent-guide/agent-gateway/pkg/credential"
)

// AssemblyOptions injects application capabilities without depending on Gateway.
// Registry, Runs and Permissions remain owned by the application lifecycle.
type AssemblyOptions struct {
	Agents      *agentpkg.Manager
	Credentials *credential.Manager
	Builtin     builtinhost.Config
	Registry    *agentruntime.Registry
	Controls    RuntimeControls
	ACPRuntime  ACPTurnServer
	HTTPBackend agentruntime.Backend
}

// Set owns concrete runtime resources and their single definition listeners.
// Native proxy and common HTTP turns use this same HTTP manager.
type Set struct {
	ACP     *acphost.Manager
	HTTP    *HTTPRuntimeManager
	Builtin *builtinhost.Host
	once    sync.Once
	err     error
}

func Assemble(ctx context.Context, opts AssemblyOptions) (_ *Set, result error) {
	set := &Set{ACP: acphost.NewManager(), HTTP: NewHTTPRuntimeManager(opts.Agents, opts.Credentials, nil, opts.Controls.Logger)}
	defer func() {
		if result != nil {
			_ = set.CloseContext(context.Background())
		}
	}()
	if opts.Agents != nil {
		opts.Builtin.Agents = opts.Agents
		set.Builtin = builtinhost.NewHost(opts.Builtin)
		opts.Controls.CheckExecution = opts.Agents.CheckExecution
	}
	server := ACPTurnServer(set.ACP)
	if opts.ACPRuntime != nil {
		server = opts.ACPRuntime
	}
	var builtinBackend *BuiltinBackend
	acp := NewACPBackend(server, opts.Controls)
	backends := []agentruntime.Backend{acp}
	if set.Builtin != nil {
		builtinBackend = NewBuiltinBackend(set.Builtin, opts.Controls)
		backends = append(backends, builtinBackend)
	}
	http := agentruntime.Backend(NewHTTPBackend(set.HTTP, opts.Controls))
	if opts.HTTPBackend != nil {
		http = opts.HTTPBackend
	}
	backends = append(backends, http)
	if opts.Registry == nil {
		return nil, fmt.Errorf("agent runtime registry is required")
	}
	if err := opts.Registry.RegisterAll(backends...); err != nil {
		return nil, fmt.Errorf("register agent runtime backends: %w", err)
	}
	if opts.Agents != nil {
		if set.Builtin != nil {
			previous := opts.Agents.Snapshot()
			listener := func(ctx context.Context, agents []agentpkg.Agent) agentpkg.DefinitionCommit {
				commit := set.Builtin.PrepareDefinitions(ctx, agents)
				var changed []string
				for _, old := range previous {
					if old.Runtime.Type != agentpkg.RuntimeTypeBuiltin {
						continue
					}
					same := false
					for _, next := range agents {
						if next.ID == old.ID && next.Runtime.Type == agentpkg.RuntimeTypeBuiltin && !next.Disabled && next.UpdatedAt.Equal(old.UpdatedAt) {
							same = true
							break
						}
					}
					if !same {
						changed = append(changed, old.ID)
					}
				}
				return func() agentpkg.DefinitionCleanup {
					cleanup := commit()
					var claimed []func(context.Context) int
					var continuations []func()
					for _, id := range changed {
						if opts.Controls.Permissions != nil {
							claimed = append(claimed, opts.Controls.Permissions.ClaimAgent(id))
						}
						continuations = append(continuations, builtinBackend.claimAgentContinuations(id))
					}
					previous = agents
					return func(ctx context.Context) {
						for _, expire := range claimed {
							expire(agentruntime.WithPermissionSource(ctx, "definition_update"))
						}
						for _, expire := range continuations {
							expire()
						}

						if cleanup != nil {
							cleanup(ctx)
						}
					}
				}
			}
			opts.Agents.AddDefinitionListener(listener)
			cleanup := listener(ctx, previous)()
			if cleanup != nil {
				cleanup(ctx)
			}
		}
		opts.Agents.AddDefinitionListener(acp.PrepareRuntimeConfigs)
		acp.RefreshRuntimeConfigs(ctx, opts.Agents.Snapshot())
		opts.Agents.AddDefinitionListener(set.HTTP.PrepareRuntimeConfigs)
		set.HTTP.RefreshRuntimeConfigs(ctx, opts.Agents.Snapshot())
		if opts.Credentials != nil {
			opts.Credentials.AddListener(set.HTTP)
		}
	}
	return set, nil
}

// CloseContext is called after shared run/permission controls stop admission.
func (s *Set) CloseContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		if s.Builtin != nil {
			s.err = s.Builtin.CloseContext(ctx)
		}
		s.ACP.Close()
		s.err = errors.Join(s.err, s.HTTP.CloseContext(ctx))
	})
	return s.err
}
