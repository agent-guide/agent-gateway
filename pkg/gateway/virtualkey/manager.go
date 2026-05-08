package virtualkey

import (
	"context"
	"errors"
	"fmt"
	"sync"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/pkg/configstore/intf"
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

	staticKeys   map[string]VirtualKey
	dynamicCache map[string]VirtualKey

	store configstoreintf.VirtualKeyStorer
}

func NewVirtualKeyManager(store configstoreintf.VirtualKeyStorer) *VirtualKeyManager {
	return &VirtualKeyManager{
		staticKeys:   map[string]VirtualKey{},
		dynamicCache: map[string]VirtualKey{},
		store:        store,
	}
}

func (m *VirtualKeyManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticKeys = map[string]VirtualKey{}
	m.dynamicCache = map[string]VirtualKey{}
}

func (m *VirtualKeyManager) InitStaticKeys(keys []VirtualKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticKeys = make(map[string]VirtualKey, len(keys))
	for _, key := range keys {
		if key.Key == "" {
			continue
		}
		m.staticKeys[key.Key] = key
	}
}

func (m *VirtualKeyManager) IsStatic(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.staticKeys[key]
	return ok
}

func (m *VirtualKeyManager) Get(ctx context.Context, key string) (VirtualKey, error) {
	if key == "" {
		return VirtualKey{}, ErrVirtualKeyNotCarried
	}

	m.mu.RLock()
	staticKey, ok := m.staticKeys[key]
	m.mu.RUnlock()
	if ok {
		return staticKey, nil
	}

	m.mu.RLock()
	cachedKey, ok := m.dynamicCache[key]
	store := m.store
	m.mu.RUnlock()
	if ok {
		return cachedKey, nil
	}

	if store == nil {
		return VirtualKey{}, ErrVirtualKeyNotConfigured
	}

	item, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, configstoreintf.ErrNotFound) {
			return VirtualKey{}, ErrVirtualKeyNotConfigured
		}
		return VirtualKey{}, fmt.Errorf("load virtual key %q: %w", key, err)
	}

	virtualKey, err := decodeVirtualKeyItem(key, item)
	if err != nil {
		return VirtualKey{}, err
	}
	m.cacheDynamicKey(virtualKey)
	return virtualKey, nil
}

func (m *VirtualKeyManager) List(ctx context.Context, opts VirtualKeyListOptions) ([]VirtualKey, error) {
	m.mu.RLock()
	store := m.store
	staticKeys := make(map[string]VirtualKey, len(m.staticKeys))
	for key, item := range m.staticKeys {
		staticKeys[key] = item
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
		cached[virtualKey.Key] = virtualKey
		if _, ok := out[virtualKey.Key]; !ok {
			out[virtualKey.Key] = virtualKey
		}
	}
	m.cacheDynamicKeys(cached)
	return mapVirtualKeys(out), nil
}

func (m *VirtualKeyManager) Create(ctx context.Context, key VirtualKey) error {
	if key.Key == "" {
		return fmt.Errorf("key is required")
	}
	if err := m.ensureWritable(key.Key); err != nil {
		return err
	}

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("virtual key store is not configured")
	}
	if err := store.Create(ctx, key.Key, key.Tag, &key); err != nil {
		return err
	}

	m.cacheDynamicKey(key)
	return nil
}

func (m *VirtualKeyManager) Update(ctx context.Context, keyID string, key VirtualKey) error {
	if keyID == "" {
		return fmt.Errorf("key is required")
	}
	if err := m.ensureWritable(keyID); err != nil {
		return err
	}

	key.Key = keyID

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("virtual key store is not configured")
	}
	if err := store.Update(ctx, keyID, &key); err != nil {
		return err
	}

	m.cacheDynamicKey(key)
	return nil
}

func (m *VirtualKeyManager) Delete(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if err := m.ensureWritable(key); err != nil {
		return err
	}

	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return fmt.Errorf("virtual key store is not configured")
	}
	if err := store.Delete(ctx, key); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dynamicCache, key)
	return nil
}

func (m *VirtualKeyManager) ensureWritable(key string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.staticKeys[key]; ok {
		return fmt.Errorf("%w: %q", ErrStaticVirtualKeyReadOnly, key)
	}
	return nil
}

func (m *VirtualKeyManager) cacheDynamicKey(key VirtualKey) {
	if key.Key == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]VirtualKey{}
	}
	m.dynamicCache[key.Key] = key
}

func (m *VirtualKeyManager) cacheDynamicKeys(keys map[string]VirtualKey) {
	if len(keys) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]VirtualKey{}
	}
	for id, key := range keys {
		m.dynamicCache[id] = key
	}
}

func decodeVirtualKeyItem(keyID string, item any) (VirtualKey, error) {
	virtualKey, ok := item.(*VirtualKey)
	if !ok || virtualKey == nil || virtualKey.Key == "" {
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
