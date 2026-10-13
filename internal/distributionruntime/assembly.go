// Package distributionruntime carries the process-scoped Caddy assembly.
// Installation occurs only in the executable entry, never via configuration.
// Standalone and CLI pass their assembly explicitly without using this slot.
package distributionruntime

import (
	"github.com/agent-guide/agent-gateway/pkg/distribution"
	"sync"
)

var slot struct {
	sync.RWMutex
	assembly *distribution.Assembly
}

func Install(a distribution.Assembly) error {
	if _, err := a.Registry(); err != nil {
		return err
	}
	a = a.Clone()
	slot.Lock()
	defer slot.Unlock()
	slot.assembly = &a
	return nil
}
func Current() distribution.Assembly {
	slot.RLock()
	defer slot.RUnlock()
	if slot.assembly == nil {
		return distribution.CommunityRuntime()
	}
	return slot.assembly.Clone()
}
