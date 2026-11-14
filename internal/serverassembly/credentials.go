// Package serverassembly constructs services shared by the Caddy and standalone
// servers and carries the process-scoped Caddy distribution assembly.
// Each service constructor creates resources owned by one server lifecycle.
package serverassembly

import (
	"fmt"
	"strings"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
	"github.com/agent-guide/agent-gateway/pkg/configstore/schema"
	"github.com/agent-guide/agent-gateway/pkg/credential"
	"github.com/agent-guide/agent-gateway/pkg/credential/scheduler"
)

type Credentials struct {
	Manager   *credential.Manager
	Scheduler scheduler.CredentialScheduler
}

// NewCredentials wires the scheduler before loading. Hosts load after static
// provider provisioning, which may itself register credentials with the manager.
func NewCredentials(backend configstore.ConfigStoreBackend, command string, args []string) (Credentials, error) {
	store, err := backend.Get(schema.StoreCredentials)
	if err != nil {
		return Credentials{}, fmt.Errorf("get credential store: %w", err)
	}
	command, args = RefreshCommand(command, args)
	manager := credential.NewManager(store)
	sched := scheduler.NewScheduler(nil)
	manager.SetRefresher(credential.NewCommandRefresher(command, args...))
	if listener, ok := sched.(credential.CredentialLifecycleListener); ok {
		manager.AddListener(listener)
	}
	return Credentials{Manager: manager, Scheduler: sched}, nil
}

// RefreshCommand owns its arguments and applies the same defaults in both hosts.
func RefreshCommand(command string, args []string) (string, []string) {
	command = strings.TrimSpace(command)
	args = append([]string(nil), args...)
	if command == "" {
		command = credential.DefaultRefreshCommand
		if len(args) == 0 {
			args = []string{credential.DefaultRefreshCommandArg}
		}
	}
	return command, args
}
