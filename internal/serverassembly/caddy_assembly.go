package serverassembly

import (
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"sync"
)

var caddyAssemblySlot struct {
	sync.RWMutex
	assembly *distribution.Assembly
}

// InstallCaddyAssembly validates and stores a process-scoped assembly snapshot.
// Only the Caddy executable entry installs it; configuration cannot replace it.
func InstallCaddyAssembly(a distribution.Assembly) error {
	if _, err := a.Registry(); err != nil {
		return err
	}
	a = a.Clone()
	caddyAssemblySlot.Lock()
	defer caddyAssemblySlot.Unlock()
	caddyAssemblySlot.assembly = &a
	return nil
}

// CurrentCaddyAssembly returns an independent snapshot, defaulting to Community.
// Standalone and CLI pass their assembly explicitly instead of using this slot.
func CurrentCaddyAssembly() distribution.Assembly {
	caddyAssemblySlot.RLock()
	defer caddyAssemblySlot.RUnlock()
	if caddyAssemblySlot.assembly == nil {
		return distribution.CommunityRuntime()
	}
	return caddyAssemblySlot.assembly.Clone()
}
