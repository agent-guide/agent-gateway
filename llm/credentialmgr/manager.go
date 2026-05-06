package credentialmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agent-guide/caddy-agent-gateway/configstore/intf"
	"github.com/agent-guide/caddy-agent-gateway/llm/credentialmgr/model"
	"github.com/google/uuid"
)

const (
	SourceAPIKey       = "api_key"
	SourceCLIAuthToken = "cliauth_token"

	CredentialScopeProviderTypePrefix = "type:"
	CredentialScopeProviderIDPrefix   = "id:"
)

type Credential = model.Credential
type ManagedCredential = model.ManagedCredential
type QuotaState = model.QuotaState
type ModelState = model.ModelState
type Error = model.Error

func ProviderTypeCredentialScope(providerType string) string {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	if providerType == "" {
		return ""
	}
	return CredentialScopeProviderTypePrefix + providerType
}

func ProviderIDCredentialScope(providerID string) string {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID == "" {
		return ""
	}
	return CredentialScopeProviderIDPrefix + providerID
}

type CredentialLifecycleListener interface {
	OnCredentialRegistered(ctx context.Context, cred *ManagedCredential)
	OnCredentialUpdated(ctx context.Context, cred *ManagedCredential)
	OnCredentialDeregistered(ctx context.Context, cred *ManagedCredential)
	OnCredentialsReplaced(ctx context.Context, creds []*ManagedCredential)
}

type Manager struct {
	store intf.CredentialStorer

	mu               sync.RWMutex
	creds            map[string]*ManagedCredential
	listeners        []CredentialLifecycleListener
	manualRefreshers map[string]ManualRefresher
}

func NewManager(store intf.CredentialStorer) *Manager {
	m := &Manager{
		store:            store,
		creds:            make(map[string]*ManagedCredential),
		manualRefreshers: make(map[string]ManualRefresher),
	}
	return m
}

func (m *Manager) AddListener(listener CredentialLifecycleListener) {
	if m == nil || listener == nil {
		return
	}
	m.mu.Lock()
	m.listeners = append(m.listeners, listener)
	m.mu.Unlock()
}

func DecodeCredential(data []byte) (any, error) {
	var c Credential
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode credential object: %w", err)
	}
	return c.Normalize(), nil
}

func (m *Manager) SetManualRefresher(manualRefreshName string, refresher ManualRefresher) {
	if m == nil {
		return
	}
	key := normalizeManualRefreshName(manualRefreshName)
	if key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if refresher == nil {
		delete(m.manualRefreshers, key)
		return
	}
	m.manualRefreshers[key] = refresher
}

func (m *Manager) RemoveManualRefresher(manualRefreshName string) {
	if m == nil {
		return
	}
	key := normalizeManualRefreshName(manualRefreshName)
	if key == "" {
		return
	}
	m.mu.Lock()
	delete(m.manualRefreshers, key)
	m.mu.Unlock()
}

func (m *Manager) ManualRefresher(manualRefreshName string) ManualRefresher {
	if m == nil {
		return nil
	}
	key := normalizeManualRefreshName(manualRefreshName)
	if key == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.manualRefreshers[key]
}

func (m *Manager) Load(ctx context.Context) error {
	if m == nil || m.store == nil {
		return nil
	}
	items, err := m.store.ListByProviderType(ctx, "")
	if err != nil {
		return fmt.Errorf("credential manager: load from store: %w", err)
	}
	for _, item := range items {
		cred, ok := item.(*Credential)
		if !ok || cred == nil {
			return fmt.Errorf("credential manager: unexpected credential type %T", item)
		}
		if err := m.RegisterCredential(WithSkipPersist(ctx), cred); err != nil {
			return fmt.Errorf("credential manager: register credential %s: %w", cred.ID, err)
		}
	}
	return nil
}

func (m *Manager) ReloadFromStore(ctx context.Context) error {
	if m == nil || m.store == nil {
		return nil
	}
	items, err := m.store.ListByProviderType(ctx, "")
	if err != nil {
		return fmt.Errorf("credential manager: reload from store: %w", err)
	}
	reloaded := make(map[string]*ManagedCredential, len(items))
	for _, item := range items {
		cred, ok := item.(*Credential)
		if !ok || cred == nil {
			return fmt.Errorf("credential manager: unexpected credential type %T", item)
		}
		normalized := cred.Normalize().Clone()
		if normalized.ID == "" {
			return fmt.Errorf("credential manager: credential has empty id")
		}
		reloaded[normalized.ID] = &ManagedCredential{Credential: *normalized}
	}

	m.mu.Lock()
	m.creds = reloaded
	m.mu.Unlock()

	m.notifyReplaced(ctx, managedCredentialMapSnapshot(reloaded))
	return nil
}

func (m *Manager) RegisterCredential(ctx context.Context, cred *Credential) error {
	if m == nil {
		return fmt.Errorf("credential manager: manager is nil")
	}
	if cred == nil {
		return fmt.Errorf("credential manager: credential is nil")
	}
	cred = cred.Normalize()
	if cred.ProviderType == "" || cred.ProviderID == "" {
		return fmt.Errorf("credential manager: credential has no provider type/id")
	}

	original := cred
	cred = cred.Clone()
	if cred.ID == "" {
		cred.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	if cred.CreatedAt.IsZero() {
		cred.CreatedAt = now
	}
	cred.UpdatedAt = now

	if !shouldSkipPersist(ctx) {
		if err := m.createOrUpdate(ctx, cred); err != nil {
			return err
		}
	}

	managed := &ManagedCredential{Credential: *cred}
	m.mu.Lock()
	m.creds[cred.ID] = managed
	m.mu.Unlock()

	m.notifyRegistered(ctx, managed)
	original.ID = cred.ID
	original.CreatedAt = cred.CreatedAt
	original.UpdatedAt = cred.UpdatedAt
	return nil
}

func (m *Manager) UpdateCredential(ctx context.Context, cred *Credential) error {
	if m == nil {
		return fmt.Errorf("credential manager: manager is nil")
	}
	if cred == nil {
		return fmt.Errorf("credential manager: credential is nil")
	}

	cred = cred.Clone().Normalize()
	cred.UpdatedAt = time.Now().UTC()
	if !shouldSkipPersist(ctx) {
		if err := m.update(ctx, cred); err != nil {
			return err
		}
	}

	m.mu.Lock()
	managed := mergeManagedCredentialLocked(m.creds[cred.ID], cred)
	m.creds[cred.ID] = managed
	m.mu.Unlock()

	m.notifyUpdated(ctx, managed)
	return nil
}

func (m *Manager) DeregisterCredential(ctx context.Context, id string) error {
	if m == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("credential manager: id is empty")
	}

	m.mu.Lock()
	cred := m.creds[id]
	_, ok := m.creds[id]
	delete(m.creds, id)
	m.mu.Unlock()
	if !ok {
		return nil
	}

	if !shouldSkipPersist(ctx) && m.store != nil {
		if err := m.store.Delete(ctx, id); err != nil {
			return fmt.Errorf("credential manager: delete from store: %w", err)
		}
	}
	if cred != nil {
		m.notifyDeregistered(ctx, cred)
	}
	return nil
}

func (m *Manager) GetCredential(id string) *ManagedCredential {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if cred := m.creds[id]; cred != nil {
		return cred.Clone()
	}
	return nil
}

func (m *Manager) ListCredentials(filter Filter) []*ManagedCredential {
	if m == nil {
		return nil
	}
	providerType := strings.ToLower(strings.TrimSpace(filter.ProviderType))
	providerID := strings.ToLower(strings.TrimSpace(filter.ProviderID))
	source := strings.ToLower(strings.TrimSpace(filter.Source))

	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*ManagedCredential, 0, len(m.creds))
	for _, cred := range m.creds {
		if cred == nil {
			continue
		}
		if providerType != "" && strings.ToLower(cred.ProviderType) != providerType {
			continue
		}
		if providerID != "" && strings.ToLower(cred.ProviderID) != providerID {
			continue
		}
		if source != "" && strings.ToLower(cred.Source) != source {
			continue
		}
		out = append(out, cred.Clone())
	}
	return out
}

func (m *Manager) RefreshCredentialIfNeeded(ctx context.Context, credID string) (*ManagedCredential, error) {
	if m == nil {
		return nil, &Error{Code: "manager_nil", Message: "credential manager not initialized"}
	}
	credID = strings.TrimSpace(credID)
	if credID == "" {
		return nil, &Error{Code: "credential_id_empty", Message: "credential id is empty"}
	}

	m.mu.RLock()
	stored := m.creds[credID]
	m.mu.RUnlock()
	if stored == nil {
		return nil, &Error{Code: "credential_not_found", Message: "credential not found"}
	}

	current := stored.Clone()
	if current.Source != SourceCLIAuthToken {
		return current, nil
	}

	manualRefreshName := current.ManualRefreshName()
	if manualRefreshName == "" {
		return current, nil
	}
	refresher := m.ManualRefresher(manualRefreshName)
	if refresher == nil || !credentialNeedsManualRefresh(&current.Credential, time.Now().UTC()) {
		return current, nil
	}

	updated, err := refresher.Refresh(ctx, current.Credential.Clone())
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return current, nil
	}

	updated = updated.Clone().Normalize()
	if updated.ID == "" {
		updated.ID = current.ID
	}
	if updated.ProviderType == "" {
		updated.ProviderType = current.ProviderType
	}
	if updated.ProviderID == "" {
		updated.ProviderID = current.ProviderID
	}
	if updated.Source == "" {
		updated.Source = current.Source
	}
	if updated.Label == "" {
		updated.Label = current.Label
	}
	if updated.Metadata == nil {
		updated.Metadata = make(map[string]any)
	}
	if updated.ManualRefreshName() == "" && manualRefreshName != "" {
		updated.Metadata[MetadataManualRefreshNameKey] = manualRefreshName
	}
	if err := m.UpdateCredential(ctx, updated); err != nil {
		return nil, err
	}
	return m.GetCredential(updated.ID), nil
}

func (m *Manager) create(ctx context.Context, cred *Credential) error {
	if m.store == nil {
		return nil
	}
	if _, err := m.store.Create(ctx, cred.ID, cred.ProviderType, cred); err != nil {
		return fmt.Errorf("credential manager: create credential %s: %w", cred.ID, err)
	}
	return nil
}

func (m *Manager) createOrUpdate(ctx context.Context, cred *Credential) error {
	if m.store == nil {
		return nil
	}
	if err := m.create(ctx, cred); err == nil {
		return nil
	}
	return m.update(ctx, cred)
}

func (m *Manager) update(ctx context.Context, cred *Credential) error {
	if m.store == nil {
		return nil
	}
	if err := m.store.Update(ctx, cred.ID, cred); err != nil {
		return fmt.Errorf("credential manager: update credential %s: %w", cred.ID, err)
	}
	return nil
}

func mergeManagedCredentialLocked(existing *ManagedCredential, spec *Credential) *ManagedCredential {
	managed := &ManagedCredential{Credential: *spec.Clone()}
	if existing == nil {
		return managed
	}
	managed.Unavailable = existing.Unavailable
	managed.NextRetryAfter = existing.NextRetryAfter
	managed.Quota = existing.Quota
	managed.AuthInvalid = existing.AuthInvalid
	managed.StateUpdatedAt = existing.StateUpdatedAt
	if existing.LastError != nil {
		managed.LastError = &Error{
			Code:       existing.LastError.Code,
			Message:    existing.LastError.Message,
			Retryable:  existing.LastError.Retryable,
			HTTPStatus: existing.LastError.HTTPStatus,
		}
	}
	if len(existing.ModelStates) > 0 {
		managed.ModelStates = make(map[string]*ModelState, len(existing.ModelStates))
		for k, v := range existing.ModelStates {
			managed.ModelStates[k] = v.Clone()
		}
	}
	return managed
}

func (m *Manager) listenersSnapshot() []CredentialLifecycleListener {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.listeners) == 0 {
		return nil
	}
	out := make([]CredentialLifecycleListener, len(m.listeners))
	copy(out, m.listeners)
	return out
}

func (m *Manager) notifyRegistered(ctx context.Context, cred *ManagedCredential) {
	for _, listener := range m.listenersSnapshot() {
		listener.OnCredentialRegistered(ctx, cred)
	}
}

func (m *Manager) notifyUpdated(ctx context.Context, cred *ManagedCredential) {
	for _, listener := range m.listenersSnapshot() {
		listener.OnCredentialUpdated(ctx, cred)
	}
}

func (m *Manager) notifyDeregistered(ctx context.Context, cred *ManagedCredential) {
	for _, listener := range m.listenersSnapshot() {
		listener.OnCredentialDeregistered(ctx, cred)
	}
}

func (m *Manager) notifyReplaced(ctx context.Context, creds []*ManagedCredential) {
	for _, listener := range m.listenersSnapshot() {
		listener.OnCredentialsReplaced(ctx, creds)
	}
}

func managedCredentialMapSnapshot(creds map[string]*ManagedCredential) []*ManagedCredential {
	if len(creds) == 0 {
		return nil
	}
	out := make([]*ManagedCredential, 0, len(creds))
	for _, cred := range creds {
		if cred == nil {
			continue
		}
		out = append(out, cred.Clone())
	}
	return out
}
