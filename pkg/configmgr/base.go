package configmgr

import (
	"context"
	"fmt"
	"sync"

	"github.com/agent-guide/agent-gateway/pkg/configstore"
)

type ListQuery struct {
	Tag       string
	TagPrefix string
}

type Definition[T any] struct {
	GetID  func(T) string
	Decode func(id string, item any) (T, error)

	Clone func(T) T

	PrepareCreate func(T) (storedObj any, cached T, err error)
	PrepareUpdate func(id string, current T, next T) (storedObj any, cached T, err error)

	ShouldIncludeStatic func(ListQuery) bool
	MatchesListQuery    func(T, ListQuery) bool

	NotConfiguredErr func(id string) error
	ReadOnlyErr      func(id string) error
	StoreNilErr      func() error
}

type BaseConfigManager[T any] struct {
	mu sync.RWMutex

	static  map[string]T
	dynamic map[string]T

	store configstore.ConfigStore
	def   Definition[T]
}

func NewBaseConfigManager[T any](store configstore.ConfigStore, def Definition[T]) *BaseConfigManager[T] {
	return &BaseConfigManager[T]{
		static:  map[string]T{},
		dynamic: map[string]T{},
		store:   store,
		def:     def,
	}
}

func (m *BaseConfigManager[T]) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.static = map[string]T{}
	m.dynamic = map[string]T{}
}

func (m *BaseConfigManager[T]) InitStatic(items []T) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.static = make(map[string]T, len(items))
	for _, item := range items {
		id := m.def.GetID(item)
		if id == "" {
			continue
		}
		if m.def.Clone != nil {
			item = m.def.Clone(item)
		}
		m.static[id] = item
	}
}

func (m *BaseConfigManager[T]) IsStatic(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.static[id]
	return ok
}

func (m *BaseConfigManager[T]) Get(ctx context.Context, id string) (T, error) {
	var zero T
	if id == "" {
		return zero, fmt.Errorf("id is required")
	}

	m.mu.RLock()
	if item, ok := m.static[id]; ok {
		m.mu.RUnlock()
		return item, nil
	}
	if item, ok := m.dynamic[id]; ok {
		m.mu.RUnlock()
		return item, nil
	}
	store := m.store
	m.mu.RUnlock()

	if store == nil {
		return zero, m.def.NotConfiguredErr(id)
	}

	raw, err := store.Get(ctx, id)
	if err != nil {
		return zero, err
	}

	item, err := m.def.Decode(id, raw)
	if err != nil {
		return zero, err
	}
	m.cache(item)
	return item, nil
}

func (m *BaseConfigManager[T]) GetFresh(ctx context.Context, id string) (T, error) {
	var zero T
	if id == "" {
		return zero, fmt.Errorf("id is required")
	}

	m.mu.RLock()
	if item, ok := m.static[id]; ok {
		m.mu.RUnlock()
		return item, nil
	}
	store := m.store
	m.mu.RUnlock()

	if store == nil {
		return zero, m.def.NotConfiguredErr(id)
	}

	raw, err := store.Get(ctx, id)
	if err != nil {
		return zero, err
	}

	item, err := m.def.Decode(id, raw)
	if err != nil {
		return zero, err
	}
	m.cache(item)
	return item, nil
}

func (m *BaseConfigManager[T]) List(ctx context.Context, query ListQuery) ([]T, error) {
	m.mu.RLock()
	staticItems := make(map[string]T, len(m.static))
	for id, item := range m.static {
		staticItems[id] = item
	}
	store := m.store
	m.mu.RUnlock()

	out := make(map[string]T, len(staticItems))
	if m.def.ShouldIncludeStatic == nil || m.def.ShouldIncludeStatic(query) {
		for id, item := range staticItems {
			if m.def.MatchesListQuery != nil && !m.def.MatchesListQuery(item, query) {
				continue
			}
			out[id] = item
		}
	}

	if store == nil {
		return mapValues(out), nil
	}

	var (
		raws []any
		err  error
	)
	if query.TagPrefix != "" {
		raws, err = store.ListByTagPrefix(ctx, query.TagPrefix)
	} else {
		raws, err = store.ListByTag(ctx, query.Tag)
	}
	if err != nil {
		return nil, err
	}

	cached := make(map[string]T, len(raws))
	for _, raw := range raws {
		item, err := m.def.Decode("", raw)
		if err != nil {
			return nil, err
		}
		id := m.def.GetID(item)
		cached[id] = item
		if _, ok := out[id]; !ok {
			out[id] = item
		}
	}
	m.cacheMany(cached)
	return mapValues(out), nil
}

func (m *BaseConfigManager[T]) Create(ctx context.Context, item T) error {
	id := m.def.GetID(item)
	if id == "" {
		return fmt.Errorf("id is required")
	}
	stored, cached, err := m.def.PrepareCreate(item)
	if err != nil {
		return err
	}
	return m.CreatePrepared(ctx, id, stored, cached)
}

func (m *BaseConfigManager[T]) CreatePrepared(ctx context.Context, id string, stored any, cached T) error {
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
		return m.def.StoreNilErr()
	}
	if err := store.Create(ctx, stored); err != nil {
		return err
	}
	m.cache(cached)
	return nil
}

func (m *BaseConfigManager[T]) Cache(item T) {
	m.cache(item)
}

func (m *BaseConfigManager[T]) CacheMany(items map[string]T) {
	m.cacheMany(items)
}

func (m *BaseConfigManager[T]) Update(ctx context.Context, id string, next T) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if err := m.ensureWritable(id); err != nil {
		return err
	}

	current, err := m.Get(ctx, id)
	if err != nil {
		return err
	}

	stored, cached, err := m.def.PrepareUpdate(id, current, next)
	if err != nil {
		return err
	}
	return m.UpdatePrepared(ctx, id, stored, cached)
}

func (m *BaseConfigManager[T]) UpdatePrepared(ctx context.Context, id string, stored any, cached T) error {
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
		return m.def.StoreNilErr()
	}
	if err := store.Update(ctx, stored); err != nil {
		return err
	}
	m.cache(cached)
	return nil
}

func (m *BaseConfigManager[T]) Delete(ctx context.Context, id string) error {
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
		return m.def.StoreNilErr()
	}
	if err := store.Delete(ctx, id); err != nil {
		return err
	}

	m.mu.Lock()
	delete(m.dynamic, id)
	m.mu.Unlock()
	return nil
}

func (m *BaseConfigManager[T]) ensureWritable(id string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.static[id]; ok {
		return m.def.ReadOnlyErr(id)
	}
	return nil
}

func (m *BaseConfigManager[T]) cache(item T) {
	id := m.def.GetID(item)
	if id == "" {
		return
	}

	if m.def.Clone != nil {
		item = m.def.Clone(item)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dynamic == nil {
		m.dynamic = map[string]T{}
	}
	m.dynamic[id] = item
}

func (m *BaseConfigManager[T]) cacheMany(items map[string]T) {
	if len(items) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dynamic == nil {
		m.dynamic = map[string]T{}
	}
	for id, item := range items {
		if m.def.Clone != nil {
			item = m.def.Clone(item)
		}
		m.dynamic[id] = item
	}
}

func mapValues[T any](in map[string]T) []T {
	out := make([]T, 0, len(in))
	for _, item := range in {
		out = append(out, item)
	}
	return out
}
