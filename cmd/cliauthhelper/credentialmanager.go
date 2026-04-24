package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr"
	"github.com/google/uuid"
)

type credentialFile struct {
	Credentials []*credentialmgr.Credential `json:"credentials"`
}

// CredentialManager stores credentials in a local JSON file.
type CredentialManager struct {
	path string

	mu    sync.RWMutex
	creds map[string]*credentialmgr.Credential
}

func NewCredentialManager(path string) (*CredentialManager, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("credential manager: path is empty")
	}

	mgr := &CredentialManager{
		path:  path,
		creds: make(map[string]*credentialmgr.Credential),
	}
	if err := mgr.load(); err != nil {
		return nil, err
	}
	return mgr, nil
}

func (m *CredentialManager) GetCredential(id string) *credentialmgr.Credential {
	if m == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if cred := m.creds[id]; cred != nil {
		return cred.Clone()
	}
	return nil
}

func (m *CredentialManager) ListCredentials(filter credentialmgr.Filter) []*credentialmgr.Credential {
	if m == nil {
		return nil
	}

	filter = normalizeFilter(filter)

	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.creds))
	for id := range m.creds {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	out := make([]*credentialmgr.Credential, 0, len(ids))
	for _, id := range ids {
		cred := m.creds[id]
		if cred == nil || !matchesFilter(cred, filter) {
			continue
		}
		out = append(out, cred.Clone())
	}
	return out
}

func (m *CredentialManager) RegisterCredential(_ context.Context, cred *credentialmgr.Credential) error {
	if m == nil {
		return fmt.Errorf("credential manager: manager is nil")
	}
	if cred == nil {
		return fmt.Errorf("credential manager: credential is nil")
	}

	normalized := cred.Clone().Normalize()
	if normalized.ID == "" {
		normalized.ID = uuid.New().String()
	}
	if normalized.ProviderType == "" {
		return fmt.Errorf("credential manager: provider type is required")
	}
	if normalized.ProviderID == "" {
		normalized.ProviderID = normalized.ProviderType
	}

	now := time.Now().UTC()
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now

	m.mu.Lock()
	defer m.mu.Unlock()
	m.creds[normalized.ID] = normalized
	return m.persistLocked()
}

func (m *CredentialManager) UpdateCredential(_ context.Context, cred *credentialmgr.Credential) error {
	if m == nil {
		return fmt.Errorf("credential manager: manager is nil")
	}
	if cred == nil {
		return fmt.Errorf("credential manager: credential is nil")
	}

	normalized := cred.Clone().Normalize()
	if normalized.ID == "" {
		return fmt.Errorf("credential manager: credential id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing := m.creds[normalized.ID]
	if existing == nil {
		return configstoreintf.ErrNotFound
	}

	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = existing.CreatedAt
	}
	if normalized.ProviderID == "" {
		normalized.ProviderID = existing.ProviderID
	}
	if normalized.ProviderType == "" {
		normalized.ProviderType = existing.ProviderType
	}
	normalized.UpdatedAt = time.Now().UTC()

	m.creds[normalized.ID] = normalized
	return m.persistLocked()
}

func (m *CredentialManager) DeregisterCredential(_ context.Context, id string) error {
	if m == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("credential manager: credential id is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.creds[id]; !ok {
		return nil
	}
	delete(m.creds, id)
	return m.persistLocked()
}

func (m *CredentialManager) load() error {
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("credential manager: read %s: %w", m.path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}

	items, err := decodeCredentialsFile(data)
	if err != nil {
		return fmt.Errorf("credential manager: decode %s: %w", m.path, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cred := range items {
		if cred == nil {
			continue
		}
		normalized := cred.Clone().Normalize()
		if normalized.ID == "" {
			normalized.ID = uuid.New().String()
		}
		if normalized.ProviderType == "" {
			continue
		}
		if normalized.ProviderID == "" {
			normalized.ProviderID = normalized.ProviderType
		}
		m.creds[normalized.ID] = normalized
	}
	return nil
}

func decodeCredentialsFile(data []byte) ([]*credentialmgr.Credential, error) {
	var wrapper credentialFile
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Credentials != nil {
		return wrapper.Credentials, nil
	}

	var items []*credentialmgr.Credential
	if err := json.Unmarshal(data, &items); err == nil {
		return items, nil
	}

	var single credentialmgr.Credential
	if err := json.Unmarshal(data, &single); err == nil && single.ID != "" {
		return []*credentialmgr.Credential{&single}, nil
	}

	return nil, fmt.Errorf("unsupported credential file format")
}

func (m *CredentialManager) persistLocked() error {
	dir := filepath.Dir(m.path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("credential manager: create dir %s: %w", dir, err)
	}

	ids := make([]string, 0, len(m.creds))
	for id := range m.creds {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	payload := credentialFile{
		Credentials: make([]*credentialmgr.Credential, 0, len(ids)),
	}
	for _, id := range ids {
		payload.Credentials = append(payload.Credentials, m.creds[id].Clone())
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("credential manager: marshal: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, filepath.Base(m.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("credential manager: create temp file: %w", err)
	}

	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("credential manager: write temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("credential manager: chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("credential manager: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, m.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("credential manager: replace %s: %w", m.path, err)
	}

	return nil
}

func normalizeFilter(filter credentialmgr.Filter) credentialmgr.Filter {
	filter.Source = strings.ToLower(strings.TrimSpace(filter.Source))
	filter.ProviderType = strings.ToLower(strings.TrimSpace(filter.ProviderType))
	filter.ProviderID = strings.ToLower(strings.TrimSpace(filter.ProviderID))
	filter.Model = strings.TrimSpace(filter.Model)
	return filter
}

func matchesFilter(cred *credentialmgr.Credential, filter credentialmgr.Filter) bool {
	if cred == nil {
		return false
	}
	if filter.Source != "" && strings.ToLower(cred.Source) != filter.Source {
		return false
	}
	if filter.ProviderType != "" && strings.ToLower(cred.ProviderType) != filter.ProviderType {
		return false
	}
	if filter.ProviderID != "" && strings.ToLower(cred.ProviderID) != filter.ProviderID {
		return false
	}
	if filter.Model != "" {
		model := ""
		if cred.Attributes != nil {
			model = strings.TrimSpace(cred.Attributes["model"])
		}
		if model == "" || model != filter.Model {
			return false
		}
	}
	return true
}
