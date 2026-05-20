package virtualkey

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/configmgr"
	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

var (
	ErrVirtualKeyNotCarried    = errors.New("virtual key is not carried")
	ErrVirtualKeyNotConfigured = errors.New("virtual key is not configured")
)

type VirtualKeyListOptions struct {
	Tag string
}

type VirtualKeyManager struct {
	base  *configmgr.BaseConfigManager[VirtualKey]
	store configstore.ConfigStore

	mu sync.RWMutex

	dynamicKeyIndex map[string]string
}

func NewVirtualKeyManager(store configstore.ConfigStore) *VirtualKeyManager {
	return &VirtualKeyManager{
		base: configmgr.NewBaseConfigManager(store, configmgr.Definition[VirtualKey]{
			GetID:  virtualKeyID,
			Decode: decodeVirtualKeyItem,
			Clone:  cloneVirtualKey,
			PrepareCreate: func(key VirtualKey) (any, VirtualKey, error) {
				if key.ID == "" {
					return nil, VirtualKey{}, fmt.Errorf("id is required")
				}
				if key.Key == "" {
					return nil, VirtualKey{}, fmt.Errorf("key is required")
				}
				key.NormalizeTimestamps(time.Now().UTC())
				return storedVirtualKey{key: &key, tag: key.Tag}, key, nil
			},
			PrepareUpdate: func(id string, current VirtualKey, key VirtualKey) (any, VirtualKey, error) {
				key.ID = id
				key.Key = current.Key
				key.CreatedAt = current.CreatedAt
				key.UpdatedAt = time.Now().UTC()
				return &key, key, nil
			},
			MatchesListQuery: func(key VirtualKey, query configmgr.ListQuery) bool {
				return query.Tag == "" || key.Tag == query.Tag
			},
			NotConfiguredErr: func(string) error {
				return ErrVirtualKeyNotConfigured
			},
			StoreNilErr: func() error {
				return fmt.Errorf("virtual key store is not configured")
			},
		}),
		store:           store,
		dynamicKeyIndex: map[string]string{},
	}
}

func (m *VirtualKeyManager) Reset() {
	m.base.Reset()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.dynamicKeyIndex = map[string]string{}
}

func (m *VirtualKeyManager) GetByKey(ctx context.Context, key string) (VirtualKey, error) {
	if key == "" {
		return VirtualKey{}, ErrVirtualKeyNotCarried
	}

	m.mu.RLock()
	dynamicID, ok := m.dynamicKeyIndex[key]
	store := m.store
	m.mu.RUnlock()

	if ok {
		virtualKey, err := m.base.Get(ctx, dynamicID)
		if err == nil {
			return virtualKey, nil
		}
	}

	if store == nil {
		return VirtualKey{}, ErrVirtualKeyNotConfigured
	}

	item, err := store.GetByIndex(ctx, "key", key)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return VirtualKey{}, ErrVirtualKeyNotConfigured
		}
		return VirtualKey{}, fmt.Errorf("load virtual key %q: %w", key, err)
	}

	virtualKey, err := decodeVirtualKeyItem("", item)
	if err != nil {
		return VirtualKey{}, err
	}
	m.base.Cache(virtualKey)
	m.cacheDynamicKey(virtualKey)
	return virtualKey, nil
}

func (m *VirtualKeyManager) GetByID(ctx context.Context, id string) (VirtualKey, error) {
	if id == "" {
		return VirtualKey{}, fmt.Errorf("id is required")
	}

	virtualKey, err := m.base.Get(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return VirtualKey{}, ErrVirtualKeyNotConfigured
		}
		if errors.Is(err, ErrVirtualKeyNotConfigured) {
			return VirtualKey{}, err
		}
		return VirtualKey{}, fmt.Errorf("load virtual key %q: %w", id, err)
	}
	m.cacheDynamicKey(virtualKey)
	return virtualKey, nil
}

func (m *VirtualKeyManager) List(ctx context.Context, opts VirtualKeyListOptions) ([]VirtualKey, error) {
	keys, err := m.base.List(ctx, configmgr.ListQuery{Tag: opts.Tag})
	if err != nil {
		return nil, err
	}

	cached := make(map[string]VirtualKey, len(keys))
	for _, key := range keys {
		cached[key.ID] = key
	}
	m.cacheDynamicKeys(cached)
	return keys, nil
}

func (m *VirtualKeyManager) Create(ctx context.Context, key VirtualKey) error {
	if err := m.base.Create(ctx, key); err != nil {
		return err
	}
	m.cacheDynamicKey(key)
	return nil
}

func (m *VirtualKeyManager) Update(ctx context.Context, id string, key VirtualKey) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}

	current, err := m.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := m.base.Update(ctx, id, key); err != nil {
		return err
	}

	key.ID = id
	key.Key = current.Key
	key.CreatedAt = current.CreatedAt
	key.UpdatedAt = time.Now().UTC()
	m.cacheDynamicKey(key)
	return nil
}

type storedVirtualKey struct {
	key any
	tag string
}

func (k storedVirtualKey) ConfigStoreObject() any {
	return k.key
}

func (k storedVirtualKey) ConfigStoreTag() string {
	return k.tag
}

func (m *VirtualKeyManager) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}

	current, err := m.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := m.base.Delete(ctx, id); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dynamicKeyIndex, current.Key)
	return nil
}

func (m *VirtualKeyManager) cacheDynamicKey(key VirtualKey) {
	if key.ID == "" || key.Key == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicKeyIndex == nil {
		m.dynamicKeyIndex = map[string]string{}
	}
	m.dynamicKeyIndex[key.Key] = key.ID
}

func (m *VirtualKeyManager) cacheDynamicKeys(keys map[string]VirtualKey) {
	if len(keys) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicKeyIndex == nil {
		m.dynamicKeyIndex = map[string]string{}
	}
	for _, key := range keys {
		if key.ID == "" || key.Key == "" {
			continue
		}
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
	if len(cloned.AllowedRouteIDs) > 0 {
		cloned.AllowedRouteIDs = append([]string(nil), cloned.AllowedRouteIDs...)
	}
	return cloned, nil
}

func cloneVirtualKey(key VirtualKey) VirtualKey {
	if len(key.AllowedRouteIDs) > 0 {
		key.AllowedRouteIDs = append([]string(nil), key.AllowedRouteIDs...)
	}
	return key
}

func virtualKeyID(key VirtualKey) string {
	return key.ID
}
