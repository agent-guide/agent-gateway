package runtimebackend

import (
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	agentexecution "github.com/agent-guide/agent-gateway/pkg/agent/execution"
)

func TestAssemblySharesControlsAndHTTPGeneration(t *testing.T) {
	registry := agentexecution.NewRegistry()
	controls := RuntimeControls{Runs: agentexecution.NewRunRegistry(), Permissions: agentexecution.NewPermissionBroker()}
	set, err := Assemble(t.Context(), AssemblyOptions{Agents: agent.NewManager(nil), Registry: registry, Controls: controls})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.CloseContext(t.Context()) })
	native, err := registry.Resolve(agent.RuntimeTypeACP)
	if err != nil {
		t.Fatal(err)
	}
	builtin, err := registry.Resolve(agent.RuntimeTypeBuiltin)
	if err != nil {
		t.Fatal(err)
	}
	http, err := registry.Resolve(agent.RuntimeTypeHTTP)
	if err != nil {
		t.Fatal(err)
	}
	acpBackend, builtinBackend, httpBackend := native.(*ACPBackend), builtin.(*BuiltinBackend), http.(*HTTPBackend)
	if acpBackend.runs != controls.Runs || builtinBackend.runs != controls.Runs || httpBackend.runs != controls.Runs {
		t.Fatal("assembly duplicated run controls")
	}
	if acpBackend.permissions != controls.Permissions || builtinBackend.permissions != controls.Permissions {
		t.Fatal("assembly duplicated permission broker")
	}
	if httpBackend.manager != set.HTTP {
		t.Fatal("common and native HTTP have different generations")
	}
	if acpBackend.runtime != set.ACP || builtinBackend.host != set.Builtin {
		t.Fatal("assembly lost runtime ownership")
	}
}
