package execution_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/agent-guide/agent-gateway/pkg/agent"
	agentexecution "github.com/agent-guide/agent-gateway/pkg/agent/execution"
	"github.com/agent-guide/agent-gateway/pkg/agent/execution/executiontest"
)

func TestBuiltInRuntimeTypeConstantsRemainRegistryKeys(t *testing.T) {
	t.Parallel()

	got := []string{agent.RuntimeTypeACP, agent.RuntimeTypeBuiltin, agent.RuntimeTypeHTTP}
	want := []string{"acp", "builtin", "http"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime type constants = %v, want registry keys %v", got, want)
	}
}

func TestRegistryRegisterResolveAndList(t *testing.T) {
	t.Parallel()

	registry := agentexecution.NewRegistry()
	acp := executiontest.NewBackend("acp")
	builtin := executiontest.NewBackend("builtin")
	if err := registry.RegisterAll(builtin, acp); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	got, err := registry.Resolve("acp")
	if err != nil {
		t.Fatalf("Resolve(acp) error = %v", err)
	}
	if got != acp {
		t.Fatal("Resolve(acp) returned a different backend")
	}
	if want := []string{"acp", "builtin"}; !reflect.DeepEqual(registry.RuntimeTypes(), want) {
		t.Fatalf("RuntimeTypes() = %v, want %v", registry.RuntimeTypes(), want)
	}
	if err := registry.ValidateRequired("acp", "builtin"); err != nil {
		t.Fatalf("ValidateRequired() error = %v", err)
	}
}

func TestRegistryDuplicateRegistrationIsAtomic(t *testing.T) {
	t.Parallel()

	registry := agentexecution.NewRegistry()
	acp := executiontest.NewBackend("acp")
	if err := registry.Register(acp); err != nil {
		t.Fatalf("Register(acp) error = %v", err)
	}
	err := registry.RegisterAll(
		executiontest.NewBackend("builtin"),
		executiontest.NewBackend("acp"),
	)
	if err == nil {
		t.Fatal("RegisterAll() = nil, want duplicate registration error")
	}
	if registry.Has("builtin") {
		t.Fatal("RegisterAll() partially registered builtin after duplicate failure")
	}
	if got, resolveErr := registry.Resolve("acp"); resolveErr != nil || got != acp {
		t.Fatalf("existing acp backend changed: backend=%v err=%v", got, resolveErr)
	}
}

func TestRegistryRejectsDuplicateWithinBatch(t *testing.T) {
	t.Parallel()

	registry := agentexecution.NewRegistry()
	err := registry.RegisterAll(
		executiontest.NewBackend("same"),
		executiontest.NewBackend("same"),
	)
	if err == nil {
		t.Fatal("RegisterAll() = nil, want duplicate registration error")
	}
	if got := registry.RuntimeTypes(); len(got) != 0 {
		t.Fatalf("RuntimeTypes() = %v, want empty registry", got)
	}
}

func TestRegistryUnknownRuntimeFailsClosed(t *testing.T) {
	t.Parallel()

	registry := agentexecution.NewRegistry()
	for _, runtimeType := range []string{"http", "", " http "} {
		backend, err := registry.Resolve(runtimeType)
		if backend != nil {
			t.Fatalf("Resolve(%q) backend = %v, want nil", runtimeType, backend)
		}
		if !errors.Is(err, agentexecution.ErrRuntimeNotExecutable) {
			t.Fatalf("Resolve(%q) error = %v, want runtime_not_executable", runtimeType, err)
		}
		if code, ok := agentexecution.ErrorCodeOf(err); !ok || code != agentexecution.ErrorRuntimeNotExecutable {
			t.Fatalf("Resolve(%q) code = %q, %v", runtimeType, code, ok)
		}
	}
}

func TestRegistryRejectsInvalidBackends(t *testing.T) {
	t.Parallel()

	var typedNil *executiontest.Backend
	tests := []struct {
		name    string
		backend agentexecution.Backend
	}{
		{name: "nil"},
		{name: "typed nil", backend: typedNil},
		{name: "empty type", backend: executiontest.NewBackend("")},
		{name: "whitespace", backend: executiontest.NewBackend(" acp")},
		{name: "slash", backend: executiontest.NewBackend("native/acp")},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			registry := agentexecution.NewRegistry()
			if err := registry.Register(tt.backend); err == nil {
				t.Fatal("Register() = nil, want validation error")
			}
			if len(registry.RuntimeTypes()) != 0 {
				t.Fatal("invalid backend modified registry")
			}
		})
	}
}
