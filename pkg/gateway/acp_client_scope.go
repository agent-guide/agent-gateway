package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	agentruntime "github.com/agent-guide/agent-gateway/pkg/agent/runtime"
	"sync"
)

func acpClientThread(scope, thread string) string {
	if scope == "" {
		return thread
	}
	raw, _ := json.Marshal([]string{scope, thread})
	digest := sha256.Sum256(raw)
	return "client:" + hex.EncodeToString(digest[:])
}

type acpScopeKey struct{ agent, id string }
type acpSessionOwner struct {
	scope  string
	access uint64
}

// Bind native identifiers to the authenticated dispatcher identity. Ownership
// is process-local; an identifier alone cannot restore it after restart or LRU
// eviction. Reuse HTTP's existing bounded client-binding budget, without adding
// a separate expiry policy or replacing native session listing/pagination.
type acpClientScopes struct {
	mu          sync.Mutex
	sessions    map[acpScopeKey]acpSessionOwner
	permissions map[acpScopeKey]string
	sequence    uint64
}

func (s *acpClientScopes) rememberSession(agent, id, scope string) error {
	if scope == "" || id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[acpScopeKey]acpSessionOwner)
	}
	key := acpScopeKey{agent, id}
	if old, ok := s.sessions[key]; ok && old.scope != scope {
		return agentruntime.NewError(agentruntime.ErrorSessionNotFound, "session is not available to this client")
	}
	if _, exists := s.sessions[key]; !exists && len(s.sessions) >= httpBindingCap {
		var oldest acpScopeKey
		var access uint64
		first := true
		for candidate, entry := range s.sessions {
			if first || entry.access < access {
				oldest, access, first = candidate, entry.access, false
			}
		}
		delete(s.sessions, oldest)
	}
	s.sequence++
	s.sessions[key] = acpSessionOwner{scope, s.sequence}
	return nil
}
func (s *acpClientScopes) ownsSession(agent, id, scope string) bool {
	if scope == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := acpScopeKey{agent, id}
	entry, ok := s.sessions[key]
	if !ok || entry.scope != scope {
		return false
	}
	s.sequence++
	entry.access = s.sequence
	s.sessions[key] = entry
	return true
}
func (s *acpClientScopes) bindPermission(agent, id, scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.permissions == nil {
		s.permissions = make(map[acpScopeKey]string)
	}
	s.permissions[acpScopeKey{agent, id}] = scope
}
func (s *acpClientScopes) forgetPermission(agent, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.permissions, acpScopeKey{agent, id})
}
func (s *acpClientScopes) ownsPermission(agent, id, scope string) bool {
	if scope == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	owner, ok := s.permissions[acpScopeKey{agent, id}]
	return ok && owner == scope
}
