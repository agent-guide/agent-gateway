package localapikey

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	configstoreintf "github.com/agent-guide/caddy-agent-gateway/configstore/intf"
)

var (
	ErrLocalAPIKeyNotCarried     = errors.New("local api key is not carried")
	ErrLocalAPIKeyNotConfigured  = errors.New("local api key is not configured")
	ErrStaticLocalAPIKeyReadOnly = errors.New("static local api key is read-only")
)

type LocalAPIKeyListOptions struct {
	UserID string
}

type LocalAPIKeyManager struct {
	mu sync.RWMutex

	staticKeys   map[string]LocalAPIKey
	dynamicCache map[string]LocalAPIKey

	store configstoreintf.LocalAPIKeyStorer
}

func NewLocalAPIKeyManager(store configstoreintf.LocalAPIKeyStorer) *LocalAPIKeyManager {
	return &LocalAPIKeyManager{
		staticKeys:   map[string]LocalAPIKey{},
		dynamicCache: map[string]LocalAPIKey{},
		store:        store,
	}
}

func (m *LocalAPIKeyManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticKeys = map[string]LocalAPIKey{}
	m.dynamicCache = map[string]LocalAPIKey{}
}

func (m *LocalAPIKeyManager) InitStaticKeys(keys []LocalAPIKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.staticKeys = make(map[string]LocalAPIKey, len(keys))
	for _, key := range keys {
		if key.Key == "" {
			continue
		}
		m.staticKeys[key.Key] = key
	}
}

func (m *LocalAPIKeyManager) IsStatic(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.staticKeys[key]
	return ok
}

func (m *LocalAPIKeyManager) Resolve(ctx context.Context, httpReq *http.Request) (LocalAPIKey, error) {
	rawKey := ExtractAPIKey(httpReq)
	if rawKey == "" {
		return LocalAPIKey{}, ErrLocalAPIKeyNotCarried
	}
	key, err := m.Get(ctx, rawKey)
	if err != nil {
		return LocalAPIKey{}, err
	}
	return key, nil
}

func (m *LocalAPIKeyManager) Get(ctx context.Context, key string) (LocalAPIKey, error) {
	if key == "" {
		return LocalAPIKey{}, ErrLocalAPIKeyNotCarried
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
		return LocalAPIKey{}, fmt.Errorf("%w: %q", ErrLocalAPIKeyNotConfigured, key)
	}

	item, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, configstoreintf.ErrNotFound) {
			return LocalAPIKey{}, fmt.Errorf("%w: %q", ErrLocalAPIKeyNotConfigured, key)
		}
		return LocalAPIKey{}, fmt.Errorf("load local api key %q: %w", key, err)
	}

	localKey, err := decodeLocalAPIKeyItem(key, item)
	if err != nil {
		return LocalAPIKey{}, err
	}
	m.cacheDynamicKey(localKey)
	return localKey, nil
}

func (m *LocalAPIKeyManager) List(ctx context.Context, opts LocalAPIKeyListOptions) ([]LocalAPIKey, error) {
	m.mu.RLock()
	store := m.store
	staticKeys := make(map[string]LocalAPIKey, len(m.staticKeys))
	for key, item := range m.staticKeys {
		staticKeys[key] = item
	}
	m.mu.RUnlock()

	out := make(map[string]LocalAPIKey, len(staticKeys))
	for key, item := range staticKeys {
		if opts.UserID != "" && item.UserID != opts.UserID {
			continue
		}
		out[key] = item
	}

	if store == nil {
		return mapLocalAPIKeys(out), nil
	}

	items, err := store.ListByUserID(ctx, opts.UserID)
	if err != nil {
		return nil, err
	}

	cached := make(map[string]LocalAPIKey, len(items))
	for _, item := range items {
		localKey, err := decodeLocalAPIKeyItem("", item)
		if err != nil {
			return nil, err
		}
		cached[localKey.Key] = localKey
		if _, ok := out[localKey.Key]; !ok {
			out[localKey.Key] = localKey
		}
	}
	m.cacheDynamicKeys(cached)
	return mapLocalAPIKeys(out), nil
}

func (m *LocalAPIKeyManager) Create(ctx context.Context, key LocalAPIKey) error {
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
		return fmt.Errorf("local api key store is not configured")
	}
	if err := store.Create(ctx, key.Key, key.UserID, &key); err != nil {
		return err
	}

	m.cacheDynamicKey(key)
	return nil
}

func (m *LocalAPIKeyManager) Update(ctx context.Context, keyID string, key LocalAPIKey) error {
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
		return fmt.Errorf("local api key store is not configured")
	}
	if err := store.Update(ctx, keyID, &key); err != nil {
		return err
	}

	m.cacheDynamicKey(key)
	return nil
}

func (m *LocalAPIKeyManager) Delete(ctx context.Context, key string) error {
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
		return fmt.Errorf("local api key store is not configured")
	}
	if err := store.Delete(ctx, key); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dynamicCache, key)
	return nil
}

func (m *LocalAPIKeyManager) ensureWritable(key string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.staticKeys[key]; ok {
		return fmt.Errorf("%w: %q", ErrStaticLocalAPIKeyReadOnly, key)
	}
	return nil
}

func (m *LocalAPIKeyManager) cacheDynamicKey(key LocalAPIKey) {
	if key.Key == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]LocalAPIKey{}
	}
	m.dynamicCache[key.Key] = key
}

func (m *LocalAPIKeyManager) cacheDynamicKeys(keys map[string]LocalAPIKey) {
	if len(keys) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamicCache == nil {
		m.dynamicCache = map[string]LocalAPIKey{}
	}
	for id, key := range keys {
		m.dynamicCache[id] = key
	}
}

func decodeLocalAPIKeyItem(keyID string, item any) (LocalAPIKey, error) {
	localKey, ok := item.(*LocalAPIKey)
	if !ok || localKey == nil || localKey.Key == "" {
		if keyID == "" {
			keyID = "<unknown>"
		}
		return LocalAPIKey{}, fmt.Errorf("local api key %q has unexpected type %T", keyID, item)
	}

	cloned := *localKey
	return cloned, nil
}

func mapLocalAPIKeys(keys map[string]LocalAPIKey) []LocalAPIKey {
	out := make([]LocalAPIKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, key)
	}
	return out
}
