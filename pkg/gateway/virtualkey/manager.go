package virtualkey

import (
	"context"
	"errors"
	"fmt"
	"sync"

	configstoreintf "github.com/agent-guide/agent-gateway/pkg/configstore/intf"
)

var (
	ErrVirtualKeyNotCarried     = errors.New("virtual key is not carried")
	ErrVirtualKeyNotConfigured  = errors.New("virtual key is not configured")
	ErrStaticVirtualKeyReadOnly = errors.New("static virtual key is read-only")
)

type VirtualKeyListOptions struct {
	Tag string
}

type VirtualKeyManager struct {
	mu sync.RWMutex

	staticKeysByID   map[string]VirtualKey
	staticKeyIndex   map[string]string
	dynamicCacheByID map[string]VirtualKey
	dynamicKeyIndex  map[string]string

	store configstoreintf.VirtualKeyStorer
}

func NewVirtualKeyManager(store configstoreintf.VirtualKeyStorer) *VirtualKeyManager {
	return &VirtualKeyManager{
		staticKeysByID:   map[string]VirtualKey{},
		staticKeyIndex:   map[string]string{},
		dynamicCacheByID: map[string]VirtualKey{},
		dynamicKeyIndex:  map[string]string{},
		store:            store,
	}
}

func (m *VirtualKeyManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticKeysByID = map[string]VirtualKey{}
	m.staticKeyIndex = map[string]string{}
	m.dynamicCacheByID = map[string]VirtualKey{}
	m.dynamicKeyIndex = map[string]string{}
}

func (m *VirtualKeyManager) InitStaticKeys(keys []VirtualKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticKeysByID = make(map[string]VirtualKey, len(keys))
	m.staticKeyIndex = make(map[string]string, len(keys))
	for _, key := range keys {
		if key.ID == "" || key.Key == "" {
			continue
		}
		m.staticKeysByID[key.ID] = key
		m.staticKeyIndex[key.Key] = key.ID
	}
}

func (m *VirtualKeyManager) IsStatic(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.staticKeysByID[id]
	return ok
}

func (m *VirtualKeyManager) GetByKey(ctx context.Context, key string) (VirtualKey, error) {
	if key == "" {
		return VirtualKey{}, ErrVirtualKeyNotCarried
	}

	m.mu.RLock()
	staticID, ok := m.staticKeyIndex[key]
	if ok {
		staticKey := m.staticKeysByID[staticID]
		m.mu.RUnlock()
		return staticKey, nil
	}
	dynamicID, ok := m.dynamicKeyIndex[key]
	if ok {
		cachedKey := m.dynamicCacheByID[dynamicID]
		m.mu.RUnlock()
		return cachedKey, nil
	}
	store := m.store
	m.mu.RUnlock()

	if store == nil {
		return VirtualKey{}, ErrVirtualKeyNotConfigured
	}

	item, err := store.GetByKey(ctx, key)
	if err != nil {
		if errors.Is(err, configstoreintf.ErrNotFound) {
			return VirtualKey{}, ErrVirtualKeyNotConfigured
		}
		return VirtualKey{}, fmt.Errorf("load virtual key %q: %w", key, err)
	}

	virtualKey, err := decodeVirtualKeyItem("", item)
	if err != nil {
		return VirtualKey{}, err
	}
	m.cacheDynamicKey(virtualKey)
	return virtualKey, nil
}

func (m *VirtualKeyManager) GetByID(ctx context.Context, id string) (VirtualKey, error) {
	if id == "" {
		return VirtualKey{}, fmt.Errorf("id is required")
	}

	m.mu.RLock()
	staticKey, ok := m.staticKeysByID[id]
	if ok {
		m.mu.RUnlock()
		return staticKey, nil
	}
	cachedKey, ok := m.dynamicCacheByID[id]
	store := m.store
	m.mu.RUnlock()
	if ok {
		return cachedKey, nil
	}

	if store == nil {
		return VirtualKey{}, ErrVirtualKeyNotConfigured
	}

	item, err := store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, configstoreintf.ErrNotFound) {
			return VirtualKey{}, ErrVirtualKeyNotConfigured
		}
		return VirtualKey{}, fmt.Errorf("load virtual key %q: %w", id, err)
	}

	virtualKey, err := decodeVirtualKeyItem(id, item)
	if err != nil {
		return VirtualKey{}, err
	}
	m.cacheDynamicKey(virtualKey)
	return virtualKey, nil
}

func (m *VirtualKeyManager) List(ctx context.Context, opts VirtualKeyListOptions) ([]VirtualKey, error) {
	m.mu.RLock()
	store := m.store
	staticKeys := make(map[string]VirtualKey, len(m.staticKeysByID))
	for id, item := range m.staticKeysByID {
		staticKeys[id] = item
	}
	m.mu.RUnlock()

	out := make(map[string]VirtualKey, len(staticKeys))
	for key, item := range staticKeys {
		if opts.Tag != "" && item.Tag != opts.Tag {
			continue
		}
		out[key] = item
	}

	if store == nil {
		return mapVirtualKeys(out), nil
	}

	items, err := store.ListByTag(ctx, opts.Tag)
	if err != nil {
		return nil, err
	}

	cached := make(map[string]VirtualKey, len(items))
	for _, item := range items {
		virtualKey, err := decodeVirtualKeyItem("", item)
		if err != nil {
			return nil, err
		}
		cached[virtualKey.ID] = virtualKey
		if _, ok := out[virtualKey.ID]; !ok {
			out[virtualKey.ID] = virtualKey
		}
	}
	m.cacheDynamicKeys(cached)
	return mapVirtualKeys(out), nil
}

func (m *VirtualKeyManager) Create(ctx context.Context, key VirtualKey) error {
	if key.ID == "" {
		return fmt.Errorf("id is required")
	}
	if key.Key == "" {
		return fmt.Errorf("key is required")
	}
	if err := m.ensureWritable(key.ID); err != nil {
		return err
	}

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("virtual key store is not configured")
	}
	if err := store.Create(ctx, key.ID, key.Tag, &key); err != nil {
		return err
	}

	m.cacheDynamicKey(key)
	return nil
}

func (m *VirtualKeyManager) Update(ctx context.Context, id string, key VirtualKey) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if err := m.ensureWritable(id); err != nil {
		return err
	}

	current, err := m.GetByID(ctx, id)
	if err != nil {
		return err
	}
	key.ID = id
	key.Key = current.Key

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("virtual key store is not configured")
	}
	if err := store.Update(ctx, id, &key); err != nil {
		return err
	}

	m.cacheDynamicKey(key)
	return nil
}

func (m *VirtualKeyManager) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if err := m.ensureWritable(id); err != nil {
		return err
	}

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("virtual key store is not configured")
	}
	current, err := m.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := store.Delete(ctx, id); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dynamicCacheByID, id)
	delete(m.dynamicKeyIndex, current.Key)
	return nil
}

func (m *VirtualKeyManager) ensureWritable(id string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.staticKeysByID[id]; ok {
		return fmt.Errorf("%w: %q", ErrStaticVirtualKeyReadOnly, id)
	}
	return nil
}

func (m *VirtualKeyManager) cacheDynamicKey(key VirtualKey) {
	if key.ID == "" || key.Key == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCacheByID == nil {
		m.dynamicCacheByID = map[string]VirtualKey{}
	}
	if m.dynamicKeyIndex == nil {
		m.dynamicKeyIndex = map[string]string{}
	}
	m.dynamicCacheByID[key.ID] = key
	m.dynamicKeyIndex[key.Key] = key.ID
}

func (m *VirtualKeyManager) cacheDynamicKeys(keys map[string]VirtualKey) {
	if len(keys) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCacheByID == nil {
		m.dynamicCacheByID = map[string]VirtualKey{}
	}
	if m.dynamicKeyIndex == nil {
		m.dynamicKeyIndex = map[string]string{}
	}
	for _, key := range keys {
		m.dynamicCacheByID[key.ID] = key
		m.dynamicKeyIndex[key.Key] = key.ID
	}
}

func decodeVirtualKeyItem(keyID string, item any) (VirtualKey, error) {
	virtualKey, ok := item.(*VirtualKey)
	if !ok || virtualKey == nil || virtualKey.ID == "" || virtualKey.Key == "" {
		if keyID == "" {
			keyID = "<unknown>"
		}
		return VirtualKey{}, fmt.Errorf("virtual key %q has unexpected type %T", keyID, item)
	}

	cloned := *virtualKey
	return cloned, nil
}

func mapVirtualKeys(keys map[string]VirtualKey) []VirtualKey {
	out := make([]VirtualKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, key)
	}
	return out
}
